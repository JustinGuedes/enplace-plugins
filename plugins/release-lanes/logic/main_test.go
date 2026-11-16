package main

import (
	"slices"
	"testing"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin/plugintest"
)

// lanes asks for the view with what Release cycle published and the work.
func lanes(t *testing.T, mode map[string]bool, rels []release, work ...plugin.Work) []plugin.Group {
	t.Helper()
	req := plugintest.Request(plugin.HookView)
	req.View = "lanes"
	var peer plugin.State
	peer.PutProject(plugintest.ProjectID, "public.releases", rels)
	if mode != nil {
		peer.PutProject(plugintest.ProjectID, "public.mode", mode)
	}
	req.Peers = map[string]plugin.State{cycle: peer}
	req.Work = work
	res := plugintest.Call(t, handle, req)
	if res.View == nil {
		t.Fatal("no view")
	}
	return res.View.Groups
}

func item(id, rel string) plugin.Work {
	return plugin.Work{ID: id, ProjectID: plugintest.ProjectID, Fields: map[string]string{field: rel}}
}

func keys(groups []plugin.Group) []string {
	var out []string
	for _, g := range groups {
		out = append(out, g.Key)
	}
	return out
}

func group(t *testing.T, groups []plugin.Group, key string) plugin.Group {
	t.Helper()
	for _, g := range groups {
		if g.Key == key {
			return g
		}
	}
	t.Fatalf("no %q lane in %v", key, keys(groups))
	return plugin.Group{}
}

func TestLanesFollowReleasesInShipOrder(t *testing.T) {
	rels := []release{
		{ID: "r2", Version: "1.3", ShipDate: "2026-03-20", State: "planned"},
		{ID: "r1", Version: "1.2", ShipDate: "2026-03-06", Cutoff: "2026-03-04", State: "planned", Total: 3, Done: 1},
		{ID: "r0", Version: "1.1", ShipDate: "2026-02-20", State: "live"},
	}
	groups := lanes(t, nil, rels, item("a", "r1"), item("b", "r2"), item("c", "r0"), item("d", ""), item("e", "gone"))

	if got, want := keys(groups), []string{"r1", "r2", unplanned, "general"}; !slices.Equal(got, want) {
		t.Fatalf("lanes %v, want %v", got, want)
	}
	first := group(t, groups, "r1")
	if !first.Accent || first.Badge != "1 of 3" || first.Sub != "Ships Fri 6 Mar · cut-off Wed 4 Mar" {
		t.Errorf("next release lane = %+v", first)
	}
	if group(t, groups, "r2").Accent {
		t.Error("only the next release is accented")
	}
	// Shipped work leaves the board; a release that no longer exists counts as general.
	if got := group(t, groups, "general").Work; !slices.Equal(got, []string{"d", "e"}) {
		t.Errorf("general work %v", got)
	}
	if got := group(t, groups, "r1").Set[field]; got != "r1" {
		t.Errorf("dropping in a lane sets %q", got)
	}
}

func TestLanesForWorkThatShipsWhenDone(t *testing.T) {
	groups := lanes(t, map[string]bool{"continuous": true, "cadence": false}, nil, item("a", deploy))
	if got, want := keys(groups), []string{deploy, "general"}; !slices.Equal(got, want) {
		t.Fatalf("lanes %v, want %v", got, want)
	}
	if g := group(t, groups, deploy); !g.Accent || !slices.Equal(g.Work, []string{"a"}) {
		t.Errorf("deploy lane = %+v", g)
	}
}

func TestLanesKeepQuietReleasesOffTheBoard(t *testing.T) {
	var rels []release
	for i, v := range []string{"1.0", "1.1", "1.2", "1.3", "1.4", "1.5"} {
		rels = append(rels, release{ID: v, Version: v, ShipDate: "2026-04-0" + string(rune('1'+i)), State: "planned"})
	}
	groups := lanes(t, nil, rels, item("a", "1.5"))
	// Four upcoming releases show; later ones only once work is planned into them.
	if got, want := keys(groups), []string{"1.0", "1.1", "1.2", "1.3", "1.5", unplanned, "general"}; !slices.Equal(got, want) {
		t.Fatalf("lanes %v, want %v", got, want)
	}
}

func TestOtherCallsDrawNothing(t *testing.T) {
	req := plugintest.Request(plugin.HookRender)
	if res := plugintest.Call(t, handle, req); res.View != nil {
		t.Errorf("render drew a view: %+v", res.View)
	}
}
