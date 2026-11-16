// Package cycle is the Release cycle plugin's rules, free of anything to do
// with Enplace's plugin calls so they can be tested plainly: version
// numbering, planning releases on a cadence, and what the ship rules say
// happens at cut-off.
package cycle

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Release states.
const (
	Planned   = "planned"
	Building  = "building"
	Submitted = "submitted"
	InReview  = "in_review"
	Live      = "live"
	Skipped   = "skipped"
)

// States in the order a release moves through them.
var States = []string{Planned, Building, Submitted, InReview, Live, Skipped}

// StateNames are the words shown for each state.
var StateNames = map[string]string{Planned: "Planned", Building: "Building", Submitted: "Submitted", InReview: "In review", Live: "Live", Skipped: "Skipped"}

// stateOrder says how far along a state is.
var stateOrder = map[string]int{Planned: 0, Building: 1, Submitted: 2, InReview: 3, Live: 4}

// Further reports whether state a is further along than b.
func Further(a, b string) bool {
	oa, ok := stateOrder[a]
	return ok && b != Skipped && oa > stateOrder[b]
}

// Release is one ship slot of a project.
type Release struct {
	ID               string `json:"id"`
	Version          string `json:"version"`
	Cutoff           string `json:"cutoff"`    // YYYY-MM-DD, "" for none
	ShipDate         string `json:"ship_date"` // YYYY-MM-DD
	State            string `json:"state"`
	WhatsNew         string `json:"whats_new,omitempty"`
	WhatsNewApproved bool   `json:"whats_new_approved,omitempty"`
	Notes            string `json:"notes,omitempty"`
	StoreState       string `json:"store_state,omitempty"`
	// Deploy marks a day's worth of work that shipped as soon as it was
	// done (a website deployed on fix), rather than a release on the cadence.
	Deploy bool `json:"deploy,omitempty"`
	// Pinned marks a ship day set by hand: planning keeps it, and the
	// releases after it follow on from it instead of the plain cadence.
	Pinned    bool      `json:"pinned,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	// Counted from the work in it, each time something changes.
	Total    int    `json:"total"`
	Done     int    `json:"done"`
	MustShip int    `json:"must_ship_open"`
	Verdict  string `json:"verdict,omitempty"`
}

// Open reports whether the release is still to ship.
func (r Release) Open() bool { return r.State != Live && r.State != Skipped }

// Rules decide what happens at cut-off.
type Rules struct {
	NothingDone  string // "skip" or "ask"
	PartlyDone   string // "ship_done", "wait" or "ask"
	MustShipLate string // "hold", "skip", "hotfix" or "ask"
	MustShipFrom int    // priority that counts as must-ship (4 = Urgent)
}

// Settings are a project's release settings.
type Settings struct {
	Enabled     bool
	EveryWeeks  int
	Weekday     int // 0 Sunday .. 6 Saturday
	CutoffDays  int
	NextVersion string
	// Anchor is a ship day on the cadence (YYYY-MM-DD): releases are planned
	// on Anchor + k·EveryWeeks weeks, so the days don't drift with today.
	// Empty: the cadence starts at the first ship day from today.
	Anchor string
	Scheme string // "short" (1.4), "semver" (1.4.0), "calver" (2026.11) or "number" (14)
	Bump   string // "major", "minor" or "patch"
	Rules  Rules
}

// Defaults are the settings a project starts with.
func Defaults() Settings {
	return Settings{Enabled: true, EveryWeeks: 2, Weekday: 5, CutoffDays: 2, Scheme: "short", Bump: "minor",
		Rules: Rules{NothingDone: "skip", PartlyDone: "ship_done", MustShipLate: "ask", MustShipFrom: 4}}
}

// Tidy puts anything out of range back to its default.
func (s Settings) Tidy() Settings {
	d := Defaults()
	if s.EveryWeeks < 1 || s.EveryWeeks > 12 {
		s.EveryWeeks = d.EveryWeeks
	}
	if s.Weekday < 0 || s.Weekday > 6 {
		s.Weekday = d.Weekday
	}
	if s.CutoffDays < 0 || s.CutoffDays > 14 {
		s.CutoffDays = d.CutoffDays
	}
	if s.Scheme != "short" && s.Scheme != "semver" && s.Scheme != "calver" && s.Scheme != "number" {
		s.Scheme = d.Scheme
	}
	if s.Bump != "major" && s.Bump != "minor" && s.Bump != "patch" {
		s.Bump = d.Bump
	}
	s.NextVersion = strings.TrimSpace(s.NextVersion)
	s.Anchor = strings.TrimSpace(s.Anchor)
	if _, err := time.Parse("2006-01-02", s.Anchor); err != nil {
		s.Anchor = ""
	}
	if s.Scheme == "semver" && len(strings.Split(s.NextVersion, ".")) == 2 {
		s.NextVersion += ".0"
	}
	r := &s.Rules
	if r.NothingDone != "skip" && r.NothingDone != "ask" {
		r.NothingDone = d.Rules.NothingDone
	}
	if r.PartlyDone != "ship_done" && r.PartlyDone != "wait" && r.PartlyDone != "ask" {
		r.PartlyDone = d.Rules.PartlyDone
	}
	if r.MustShipLate != "hold" && r.MustShipLate != "skip" && r.MustShipLate != "hotfix" && r.MustShipLate != "ask" {
		r.MustShipLate = d.Rules.MustShipLate
	}
	if r.MustShipFrom < 1 || r.MustShipFrom > 4 {
		r.MustShipFrom = d.Rules.MustShipFrom
	}
	return s
}

// Item is what the rules need to know about a piece of work.
type Item struct {
	ID       string
	Release  string // the release it's in
	Done     bool
	Priority int
	MustShip bool
}

// Count fills in each release's totals and verdict from the work in it.
func Count(rels []Release, items []Item, rules Rules) {
	idx := map[string]int{}
	for i := range rels {
		rels[i].Total, rels[i].Done, rels[i].MustShip = 0, 0, 0
		idx[rels[i].ID] = i
	}
	for _, it := range items {
		i, ok := idx[it.Release]
		if !ok {
			continue
		}
		r := &rels[i]
		r.Total++
		if it.Done {
			r.Done++
		} else if it.MustShip || it.Priority >= rules.MustShipFrom {
			r.MustShip++
		}
	}
	for i := range rels {
		rels[i].Verdict = Verdict(rules, rels[i])
	}
}

// Verdict applies the ship rules: what happens at cut-off as things stand.
func Verdict(rules Rules, r Release) string {
	if r.State != Planned && r.State != Building {
		return ""
	}
	switch {
	case r.Done == 0:
		if rules.NothingDone == "ask" {
			return "ask"
		}
		return "skip"
	case r.MustShip > 0:
		return rules.MustShipLate
	case r.Done < r.Total:
		return rules.PartlyDone
	default:
		return "ship"
	}
}

// VerdictText explains a verdict in a sentence.
func VerdictText(r Release) string {
	switch r.Verdict {
	case "skip":
		if r.Total == 0 {
			return "Nothing is planned for this release, so it will be skipped."
		}
		return "Nothing is done yet, so this release will be skipped at cut-off."
	case "ship":
		return "Everything is done. Ready to ship."
	case "ship_done":
		return fmt.Sprintf("Ships the %d done; the other %d move to the next release.", r.Done, r.Total-r.Done)
	case "wait":
		return "Waits until everything is done."
	case "hold":
		return "Held until the must-ship work is done."
	case "hotfix":
		return "Ships on time; the must-ship work follows as a hotfix."
	case "ask":
		return "You'll be asked what to do at cut-off."
	}
	return ""
}

// VerdictLine is the short form, for a card.
func VerdictLine(r Release) string {
	switch r.Verdict {
	case "skip":
		return "Skips itself if nothing is done."
	case "ship":
		return "On track."
	case "ship_done":
		return "Ships what's done; the rest moves on."
	case "hold":
		return "Holds for must-ship work."
	case "hotfix":
		return "Ships, with a hotfix to follow."
	case "wait":
		return "Waits until everything is done."
	case "ask":
		return "You'll be asked at cut-off."
	}
	return ""
}

// Bump returns the version after v.
//   - short:  "1.4" -> "2.0" (major), "1.5" (minor) or "1.4.1" (patch)
//   - semver: "1.4.0" -> "2.0.0", "1.5.0" or "1.4.1"
//   - calver: year.month of the ship date, then .1, .2 for more in that month
//   - number: "14" -> "15"
func Bump(v, scheme, how, ship string) string {
	v = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(v), "v"))
	if scheme == "calver" {
		t, err := time.Parse("2006-01-02", ship)
		if err != nil {
			return v + ".1"
		}
		month := fmt.Sprintf("%d.%d", t.Year(), int(t.Month()))
		if v == month {
			return month + ".1"
		}
		if rest, ok := strings.CutPrefix(v, month+"."); ok {
			if n, err := strconv.Atoi(rest); err == nil {
				return fmt.Sprintf("%s.%d", month, n+1)
			}
		}
		return month
	}
	nums := make([]int, 0, 3)
	if v != "" {
		for _, p := range strings.Split(v, ".") {
			n, err := strconv.Atoi(p)
			if err != nil {
				return v + ".1"
			}
			nums = append(nums, n)
		}
	}
	if scheme == "number" {
		if len(nums) == 0 {
			return "1"
		}
		return strconv.Itoa(nums[0] + 1)
	}
	if len(nums) == 0 {
		if scheme == "semver" {
			return "1.0.0"
		}
		return "1.0"
	}
	for len(nums) < 3 {
		nums = append(nums, 0)
	}
	switch how {
	case "major":
		nums = []int{nums[0] + 1, 0, 0}
	case "patch":
		nums[2]++
	default:
		nums = []int{nums[0], nums[1] + 1, 0}
	}
	if scheme == "semver" || nums[2] != 0 {
		return fmt.Sprintf("%d.%d.%d", nums[0], nums[1], nums[2])
	}
	return fmt.Sprintf("%d.%d", nums[0], nums[1])
}

// VersionLess compares dotted versions number by number.
func VersionLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x < y
		}
	}
	return false
}

// Sort orders releases by ship date, then version.
func Sort(rels []Release) {
	sort.SliceStable(rels, func(i, j int) bool {
		if rels[i].ShipDate != rels[j].ShipDate {
			return rels[i].ShipDate < rels[j].ShipDate
		}
		return VersionLess(rels[i].Version, rels[j].Version)
	})
}

// Plan keeps three upcoming release slots on the cadence, and returns the
// releases with any new ones added. Planned slots with nothing in them
// follow the cadence when it changes. today is midnight in the project's
// timezone; live is the version that's out now, when a store knows it;
// newID makes an id for a new release.
func Plan(rels []Release, s Settings, today time.Time, live string, hasWork func(id string) bool, newID func() string) []Release {
	if !s.Enabled {
		return rels
	}
	const day = "2006-01-02"
	// The first ship day: the next day on the cadence at least cut-off
	// days away.
	first := FirstShip(s, today)
	// Three ship days ahead, passing over any a release already holds
	// without being planned (one skipped by hand keeps its day).
	held := map[string]bool{}
	for _, r := range rels {
		if r.State != Planned {
			held[r.ShipDate] = true
		}
	}
	want := []string{}
	for i := 0; len(want) < 3 && i < 12; i++ {
		if d := first.AddDate(0, 0, 7*s.EveryWeeks*i).Format(day); !held[d] {
			want = append(want, d)
		}
	}
	cut := func(ship string) string {
		t, err := time.Parse(day, ship)
		if err != nil {
			return ""
		}
		return t.AddDate(0, 0, -s.CutoffDays).Format(day)
	}
	gap := s.gap()
	// after is the first ship day on the cadence that follows prev.
	after := func(prev, want string) string {
		t, err := time.Parse(day, prev)
		if err != nil {
			return want
		}
		d := t.AddDate(0, 0, gap).Format(day)
		for held[d] {
			t = t.AddDate(0, 0, gap)
			d = t.AddDate(0, 0, gap).Format(day)
		}
		if d > want {
			return d
		}
		return want
	}
	Sort(rels)
	kept := 0
	high := ""
	prev := ""
	for i := range rels {
		r := &rels[i]
		if high == "" || VersionLess(high, r.Version) {
			high = r.Version
		}
		if r.State != Planned || r.ShipDate < today.Format(day) {
			continue
		}
		if kept < len(want) {
			if !r.Pinned && !hasWork(r.ID) {
				// An empty slot sits on the cadence, but never before
				// the one ahead of it was moved to.
				if d := after(prev, want[kept]); r.ShipDate != d {
					r.ShipDate, r.Cutoff = d, cut(d)
				}
			}
			kept++
		}
		prev = r.ShipDate
	}
	if live != "" && (high == "" || VersionLess(high, live)) {
		high = live
	}
	for i := kept; i < len(want); i++ {
		d := after(prev, want[i])
		var v string
		if s.NextVersion != "" && (high == "" || VersionLess(high, s.NextVersion)) {
			v = s.NextVersion
		} else {
			v = Bump(high, s.Scheme, s.Bump, d)
		}
		high = v
		prev = d
		rels = append(rels, Release{ID: newID(), Version: v, ShipDate: d, Cutoff: cut(d), State: Planned, CreatedAt: today})
	}
	Sort(rels)
	return rels
}

// gap is the days between releases.
func (s Settings) gap() int {
	if s.EveryWeeks <= 0 {
		return 14
	}
	return 7 * s.EveryWeeks
}

// FirstShip is the first ship day on the cadence at least cut-off days from
// today: Anchor + k·EveryWeeks weeks with an anchor (moved on to the chosen
// weekday if it isn't on it), or else the next chosen weekday.
func FirstShip(s Settings, today time.Time) time.Time {
	earliest := today.AddDate(0, 0, s.CutoffDays)
	a, err := time.ParseInLocation("2006-01-02", s.Anchor, today.Location())
	if err != nil {
		a = earliest
	}
	for int(a.Weekday()) != s.Weekday {
		a = a.AddDate(0, 0, 1)
	}
	if err != nil {
		return a
	}
	gap := s.gap()
	// Whole days between them, safe across clock changes.
	days := int(math.Round(earliest.Sub(a).Hours() / 24))
	k := days / gap
	if days > 0 && days%gap != 0 {
		k++
	}
	if days < 0 {
		k = -((-days) / gap)
	}
	return a.AddDate(0, 0, k*gap)
}

// DefaultAnchor picks the anchor for a cadence when none is set: the latest
// release on the cadence that has already shipped by today, or else the
// earliest one coming, or else the first ship day from today. Deploys,
// releases moved by hand and ones off the chosen weekday don't count.
func DefaultAnchor(rels []Release, s Settings, today time.Time) string {
	const day = "2006-01-02"
	now := today.Format(day)
	past, coming := "", ""
	for _, r := range rels {
		if r.Deploy || r.Pinned {
			continue
		}
		t, err := time.Parse(day, r.ShipDate)
		if err != nil || int(t.Weekday()) != s.Weekday {
			continue
		}
		if r.ShipDate <= now {
			if r.ShipDate > past {
				past = r.ShipDate
			}
		} else if coming == "" || r.ShipDate < coming {
			coming = r.ShipDate
		}
	}
	switch {
	case past != "":
		return past
	case coming != "":
		return coming
	}
	s.Anchor = ""
	return FirstShip(s, today).Format(day)
}

// MoveDates sets a release's ship day by hand, carrying its cut-off along
// by the same number of days. With follow, the open releases after it move
// by the same amount, so the cadence keeps its rhythm; without, they stay
// where they are. It returns the releases that moved along with it.
func MoveDates(rels []Release, id, ship string, follow bool) (moved []Release, ok bool) {
	const day = "2006-01-02"
	var r *Release
	for i := range rels {
		if rels[i].ID == id {
			r = &rels[i]
		}
	}
	to, err := time.Parse(day, ship)
	if r == nil || err != nil || !r.Open() {
		return nil, false
	}
	from, err := time.Parse(day, r.ShipDate)
	if err != nil {
		from = to
	}
	delta := int(to.Sub(from).Hours() / 24)
	shift := func(d string) string {
		t, err := time.Parse(day, d)
		if err != nil || d == "" {
			return d
		}
		return t.AddDate(0, 0, delta).Format(day)
	}
	old := r.ShipDate
	r.ShipDate, r.Cutoff, r.Pinned = ship, shift(r.Cutoff), true
	if r.Cutoff > r.ShipDate {
		r.Cutoff = r.ShipDate
	}
	for i := range rels {
		n := &rels[i]
		if n.ID == id || n.State != Planned || n.ShipDate <= old {
			continue
		}
		if follow && delta != 0 {
			n.ShipDate, n.Cutoff = shift(n.ShipDate), shift(n.Cutoff)
			moved = append(moved, *n)
		}
		// Either way it keeps the day it now has.
		n.Pinned = true
	}
	Sort(rels)
	return moved, true
}

// Unpin lets planned releases follow the cadence again, after the cadence
// itself changes.
func Unpin(rels []Release) {
	for i := range rels {
		if rels[i].State == Planned {
			rels[i].Pinned = false
		}
	}
}

// Move is work to move from one release to another.
type Move struct {
	Work string
	To   string
}

// Tick applies the ship rules to planned releases whose cut-off has passed
// (rels must be counted). It changes states in place and returns the work
// that moves on to the next release, and a line for each thing it did.
func Tick(rels []Release, items []Item, today string) (moves []Move, log []string) {
	Sort(rels)
	for i := range rels {
		r := &rels[i]
		if r.State != Planned || r.Cutoff == "" || r.Cutoff >= today {
			continue
		}
		// What isn't done moves on to the next planned release.
		moveOn := func() {
			next := ""
			for _, n := range rels[i+1:] {
				if n.ShipDate > r.ShipDate && n.State == Planned {
					next = n.ID
					break
				}
			}
			if next == "" {
				return
			}
			for _, it := range items {
				if it.Release == r.ID && !it.Done {
					moves = append(moves, Move{Work: it.ID, To: next})
				}
			}
		}
		switch r.Verdict {
		case "skip":
			r.State = Skipped
			moveOn()
			log = append(log, r.Version+" was skipped: nothing was done at cut-off")
		case "ship", "ship_done", "hotfix":
			if r.Verdict != "ship" {
				moveOn()
			}
			r.State = Building
			log = append(log, r.Version+" is now building: its cut-off passed")
		}
	}
	return moves, log
}

// CanSkip reports whether a release can still be skipped by hand: it
// hasn't been sent to a store yet.
func (r Release) CanSkip() bool { return r.State == Planned || r.State == Building }

// Skip skips the release with this id by hand and returns where its
// unfinished work goes: the next planned release after it, or "" when
// there is none (the caller decides what that means).
func Skip(rels []Release, items []Item, id string) (next string, moves []Move, ok bool) {
	Sort(rels)
	at := -1
	for i := range rels {
		if rels[i].ID == id {
			at = i
		}
	}
	if at < 0 || !rels[at].CanSkip() {
		return "", nil, false
	}
	r := &rels[at]
	for _, n := range rels[at+1:] {
		if n.State == Planned && n.ShipDate >= r.ShipDate {
			next = n.ID
			break
		}
	}
	r.State = Skipped
	for _, it := range items {
		if it.Release == id && !it.Done {
			moves = append(moves, Move{Work: it.ID, To: next})
		}
	}
	return next, moves, true
}

// DraftWhatsNew writes a plain "What's new" from the titles of a release's
// done work, most important first.
func DraftWhatsNew(version string, titles []string) string {
	var b strings.Builder
	b.WriteString("What's new in " + version + ":\n")
	for _, t := range titles {
		b.WriteString("• " + t + "\n")
	}
	return strings.TrimSpace(b.String())
}
