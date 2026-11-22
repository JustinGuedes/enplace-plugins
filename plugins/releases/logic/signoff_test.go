package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/JustinGuedes/enplace-plugins/plugins/releases/cycle"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin/plugintest"
)

// building is a project whose 1.4 passed its cut-off with one of its two
// items done, so it's building.
func building(hook string, settings map[string]any) *plugin.Request {
	req := plugintest.Request(hook)
	req.Plugin.ID = "releases"
	req.ProjectSettings = map[string]map[string]any{plugintest.ProjectID: settings}
	req.State.PutProject(plugintest.ProjectID, keyReleases, []cycle.Release{
		{ID: "r1", Version: "1.4", Cutoff: "2026-03-01", ShipDate: "2026-03-03", State: cycle.Building},
	})
	rel := map[string]string{"releases.release": "r1"}
	req.Work = []plugin.Work{
		{ID: "w1", Key: "ACM-1", ProjectID: plugintest.ProjectID, Title: "Finish a trip", StageKind: "done", Fields: rel},
		{ID: "w2", Key: "ACM-2", ProjectID: plugintest.ProjectID, Title: "Reuse a list", StageKind: "doing", Fields: rel},
	}
	return req
}

// A building release waits for the person's sign-off: the brief, the
// status and the release say so and link to what's in it, until Sign off.
// The setting turns it off (ENP-342).
func TestReleaseWaitsForSignOff(t *testing.T) {
	req := building(plugin.HookRender, map[string]any{"enabled": false})
	req.Slots = []string{plugin.SlotBrief}
	res := plugintest.Call(t, handle, req)
	if res.Brief == nil || !strings.Contains(res.Brief.Text, "Acme 1.4 is waiting for your sign-off") {
		t.Fatalf("brief = %+v", res.Brief)
	}
	if b := res.Brief.Button; b == nil || b.Href != "/p/acm/signoff/r1" {
		t.Fatalf("button = %+v, want the sign-off page", b)
	}

	req = building(plugin.HookPage, map[string]any{"enabled": false})
	req.Page = "releases"
	res = plugintest.Call(t, handle, req)
	page, _ := json.Marshal(res.Page)
	if !strings.Contains(string(page), `"action":"sign_off"`) || !strings.Contains(string(page), "/p/acm/signoff/r1") {
		t.Fatalf("release has no Sign off: %s", page)
	}

	req = building(plugin.HookAction, map[string]any{"enabled": false})
	req.Action = &plugin.Action{Name: "sign_off", Args: args("id", "r1")}
	res = plugintest.Call(t, handle, req)
	var rels []cycle.Release
	res.State.GetProject(plugintest.ProjectID, keyReleases, &rels)
	if len(rels) != 1 || !rels[0].SignedOff || rels[0].SignedAt == nil || res.Toast != "Signed off 1.4" {
		t.Fatalf("after Sign off: %+v, toast %q", rels, res.Toast)
	}
	if st := res.Status[plugintest.ProjectID]; st == nil || strings.Contains(st.Text, "sign-off") {
		t.Fatalf("status still asks: %+v", st)
	}

	req = building(plugin.HookRender, map[string]any{"enabled": false, "sign_off": "Ship without it"})
	req.Slots = []string{plugin.SlotBrief}
	if res = plugintest.Call(t, handle, req); res.Brief != nil && strings.Contains(res.Brief.Text, "sign-off") {
		t.Fatalf("asked with sign-off off: %+v", res.Brief)
	}
}

// Nothing done means nothing to sign off.
func TestSignOffNeedsDoneWork(t *testing.T) {
	req := building(plugin.HookAction, map[string]any{"enabled": false})
	req.Work[0].StageKind = "doing"
	req.Action = &plugin.Action{Name: "sign_off", Args: args("id", "r1")}
	res := plugintest.Call(t, handle, req)
	if res.Toast != "1.4 can't be signed off: nothing in it is done yet" {
		t.Fatalf("toast = %q", res.Toast)
	}
}
