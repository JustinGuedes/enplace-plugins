package cycle

import (
	"strings"
	"testing"
	"time"
)

func TestBump(t *testing.T) {
	for _, c := range []struct{ v, scheme, how, ship, want string }{
		{"1.2.3", "short", "minor", "", "1.3"},
		{"1.2.3", "short", "patch", "", "1.2.4"},
		{"1.4", "short", "major", "", "2.0"},
		{"", "short", "minor", "", "1.0"},
		{"1.4", "semver", "minor", "", "1.5.0"},
		{"1.4.2", "semver", "patch", "", "1.4.3"},
		{"1.4.2", "semver", "major", "", "2.0.0"},
		{"14", "number", "minor", "", "15"},
		{"1.4", "calver", "minor", "2026-11-20", "2026.11"},
		{"2026.11", "calver", "minor", "2026-11-27", "2026.11.1"},
		{"2026.11.1", "calver", "minor", "2026-11-27", "2026.11.2"},
		{"2026.11.2", "calver", "minor", "2026-12-04", "2026.12"},
	} {
		if got := Bump(c.v, c.scheme, c.how, c.ship); got != c.want {
			t.Errorf("Bump(%q, %s, %s) = %q, want %q", c.v, c.scheme, c.how, got, c.want)
		}
	}
	if !VersionLess("1.9", "1.10") {
		t.Fatal("VersionLess")
	}
}

func TestPlanAndTick(t *testing.T) {
	today := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC) // a Thursday
	n := 0
	id := func() string { n++; return "r" + string(rune('0'+n)) }
	none := func(string) bool { return false }
	s := Defaults()
	rels := Plan(nil, s, today, "1.4", none, id)
	if len(rels) < 3 || rels[0].Version != "1.5" || rels[1].Version != "1.6" || rels[0].ShipDate != "2026-11-20" || rels[0].Cutoff != "2026-11-18" {
		t.Fatalf("plan %+v", rels)
	}
	// Planning again adds nothing.
	if again := Plan(rels, s, today, "1.4", none, id); len(again) != len(rels) {
		t.Fatalf("planned twice: %d then %d", len(rels), len(again))
	}

	// At cut-off: what's done ships, the rest moves on, and must-ship work asks.
	items := []Item{{ID: "a", Release: rels[0].ID, Done: true}, {ID: "b", Release: rels[0].ID}}
	Count(rels, items, s.Rules)
	if rels[0].Total != 2 || rels[0].Done != 1 || rels[0].Verdict != "ship_done" {
		t.Fatalf("count %+v", rels[0])
	}
	moves, _ := Tick(rels, items, "2026-11-19")
	if len(moves) != 1 || moves[0].Work != "b" || moves[0].To != rels[1].ID {
		t.Fatalf("moves %+v", moves)
	}
	must := []Item{{ID: "a", Release: rels[1].ID, MustShip: true}, {ID: "d", Release: rels[1].ID, Done: true}}
	Count(rels, must, s.Rules)
	if rels[1].Verdict != "ask" || rels[1].MustShip != 1 {
		t.Fatalf("must ship %+v", rels[1])
	}
	// Nothing done by the ship day: the release skips itself.
	empty := []Release{{ID: "x", Version: "2.0", ShipDate: "2026-11-10", Cutoff: "2026-11-08", State: Planned}, {ID: "y", Version: "2.1", ShipDate: "2026-11-24", State: Planned}}
	late := []Item{{ID: "c", Release: "x"}}
	Count(empty, late, s.Rules)
	moves, _ = Tick(empty, late, "2026-11-12")
	if empty[0].State != Skipped || len(moves) != 1 || moves[0].To != "y" {
		t.Fatalf("skip %+v %+v", empty[0], moves)
	}
}

func TestSkipByHand(t *testing.T) {
	today := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	n := 0
	id := func() string { n++; return "r" + string(rune('0'+n)) }
	none := func(string) bool { return false }
	s := Defaults()
	rels := Plan(nil, s, today, "1.4", none, id)
	first, second := rels[0], rels[1]
	items := []Item{{ID: "a", Release: first.ID, Done: true}, {ID: "b", Release: first.ID}, {ID: "c", Release: second.ID}}
	next, moves, ok := Skip(rels, items, first.ID)
	if !ok || next != second.ID || len(moves) != 1 || moves[0].Work != "b" || moves[0].To != second.ID {
		t.Fatalf("skip: %v %v %+v", ok, next, moves)
	}
	if rels[0].State != Skipped {
		t.Fatalf("not skipped: %+v", rels[0])
	}
	// The skipped day stays skipped: nothing slides into it, and a release
	// is planned further on to keep three ahead.
	rels = Plan(rels, s, today, "1.4", none, id)
	planned := 0
	for _, r := range rels {
		if r.State == Planned {
			planned++
			if r.ShipDate == first.ShipDate {
				t.Fatalf("a release slid into the skipped day: %+v", rels)
			}
		}
	}
	if planned != 3 || rels[1].ShipDate != second.ShipDate {
		t.Fatalf("after skipping: %+v", rels)
	}
	// A release in a store can't be skipped here.
	rels[1].State = InReview
	if _, _, ok := Skip(rels, nil, rels[1].ID); ok {
		t.Fatal("skipped a release in review")
	}
}

func TestMoveDates(t *testing.T) {
	today := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	n := 0
	id := func() string { n++; return "r" + string(rune('0'+n)) }
	none := func(string) bool { return false }
	s := Defaults()
	rels := Plan(nil, s, today, "1.0", none, id)
	first := rels[0].ID // 1.1, Fri 20 Nov, then 4 Dec and 18 Dec

	// A week later, with the releases after it following along.
	moved, ok := MoveDates(rels, first, "2026-11-27", true)
	if !ok || len(moved) != 2 {
		t.Fatalf("move: %v %+v", ok, moved)
	}
	rels = Plan(rels, s, today, "1.0", none, id)
	got := []string{}
	for _, r := range rels {
		got = append(got, r.Version+" "+r.ShipDate+" "+r.Cutoff)
	}
	want := []string{"1.1 2026-11-27 2026-11-25", "1.2 2026-12-11 2026-12-09", "1.3 2026-12-25 2026-12-23"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("after moving:\n%v\nwant\n%v", got, want)
	}

	// Without following, the others keep their days, and planning doesn't
	// snap the moved one back to the cadence.
	rels = Plan(nil, s, today, "1.0", none, id)
	MoveDates(rels, rels[0].ID, "2026-11-23", false)
	rels = Plan(rels, s, today, "1.0", none, id)
	if rels[0].ShipDate != "2026-11-23" || rels[0].Cutoff != "2026-11-21" || rels[1].ShipDate != "2026-12-04" {
		t.Fatalf("not following: %+v", rels)
	}

	// An empty release moved past the next one on its own pushes the
	// rest of the cadence on from it.
	rels = Plan(nil, s, today, "1.0", none, id)
	rels[0].ShipDate, rels[0].Pinned = "2026-12-11", true
	rels = Plan(rels, s, today, "1.0", none, id)
	last := rels[len(rels)-1]
	if last.ShipDate <= "2026-12-11" {
		t.Fatalf("cadence didn't follow: %+v", rels)
	}

	// A cadence change lets them follow it again.
	Unpin(rels)
	rels = Plan(rels, s, today, "1.0", none, id)
	if rels[0].ShipDate != "2026-11-20" {
		t.Fatalf("unpinned: %+v", rels)
	}
}

func dates(rels []Release) string {
	got := []string{}
	for _, r := range rels {
		if r.State == Planned {
			got = append(got, r.ShipDate)
		}
	}
	return strings.Join(got, ",")
}

// Two cycles with the same settings, started a week apart, ship on the same
// weeks once they share an anchor.
func TestAnchorSameWeeks(t *testing.T) {
	n := 0
	id := func() string { n++; return "r" + string(rune('0'+n)) }
	none := func(string) bool { return false }
	s := Defaults()
	s.Anchor = "2026-11-20"
	a := Plan(nil, s, time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC), "1.0", none, id)
	b := Plan(nil, s, time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC), "1.0", none, id)
	if got, want := dates(a), "2026-11-20,2026-12-04,2026-12-18"; got != want {
		t.Fatalf("a: %s, want %s", got, want)
	}
	// Too close to 20 Nov for its cut-off, so it starts on the next one.
	if got, want := dates(b), "2026-12-04,2026-12-18,2027-01-01"; got != want {
		t.Fatalf("b: %s, want %s", got, want)
	}
	// An anchor long ago, or still to come, gives the same weeks.
	for _, anchor := range []string{"2025-01-03", "2027-03-12"} {
		s.Anchor = anchor
		if got := dates(Plan(nil, s, time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC), "1.0", none, id)); got != dates(a) {
			t.Fatalf("anchor %s: %s, want %s", anchor, got, dates(a))
		}
	}
	// An anchor off the ship day moves on to it.
	s.Anchor = "2026-11-18"
	if got := dates(Plan(nil, s, time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC), "1.0", none, id)); got != dates(a) {
		t.Fatalf("anchor off the weekday: %s", got)
	}
}

// Once a release's day has passed, the empty slots keep the cadence's gap
// instead of snapping to the next ship weekday.
func TestAnchorAfterShipDay(t *testing.T) {
	n := 0
	id := func() string { n++; return "r" + string(rune('0'+n)) }
	none := func(string) bool { return false }
	s := Defaults()
	start := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	s.Anchor = DefaultAnchor(nil, s, start)
	if s.Anchor != "2026-11-20" {
		t.Fatalf("default anchor %s", s.Anchor)
	}
	rels := Plan(nil, s, start, "1.0", none, id)
	rels[0].State = Live // 20 Nov shipped
	later := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	rels = Plan(rels, s, later, "1.0", none, id)
	if got, want := dates(rels), "2026-12-04,2026-12-18,2027-01-01"; got != want {
		t.Fatalf("after shipping: %s, want %s", got, want)
	}
	// Picked again from the releases there are, it's the same anchor.
	if a := DefaultAnchor(rels, s, later); a != "2026-11-20" {
		t.Fatalf("anchor from releases %s", a)
	}
	// Without the anchor, planning from today is what drifted.
	s.Anchor = ""
	if got := dates(Plan(nil, s, later, "1.0", none, id)); got != "2026-11-27,2026-12-11,2026-12-25" {
		t.Fatalf("unanchored: %s", got)
	}
}
