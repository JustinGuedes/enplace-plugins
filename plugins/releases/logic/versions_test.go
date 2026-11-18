package main

import (
	"strings"
	"testing"

	"github.com/JustinGuedes/enplace-plugins/plugins/releases/cycle"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin/plugintest"
)

func planned(t *testing.T, req *plugin.Request) []cycle.Release {
	t.Helper()
	res := plugintest.Call(t, handle, req)
	var rels []cycle.Release
	if res.State == nil || !res.State.GetProject(plugintest.ProjectID, keyReleases, &rels) || len(rels) == 0 {
		t.Fatal("nothing planned")
	}
	return rels
}

func TestNumbersReleasesTheWayVersionsSays(t *testing.T) {
	req := plugintest.Request(plugin.HookSchedule)
	req.Plugin.ID = "releases"
	req.ProjectSettings = map[string]map[string]any{plugintest.ProjectID: {}}
	if rels := planned(t, req); rels[0].Version != "1.0" {
		t.Fatalf("on its own: %s", rels[0].Version)
	}

	v := plugin.State{}
	v.PutProject(plugintest.ProjectID, "public.versions", versionsInfo{Version: "0.3.0", File: "Version.xcconfig", Style: "semver", Bump: "minor", NextVersion: "0.4.0"})
	req.Peers = map[string]plugin.State{"versions": v}
	rels := planned(t, req)
	if rels[0].Version != "0.4.0" {
		t.Fatalf("with Versions: %s", rels[0].Version)
	}
	if ci := (&cycleRun{req: req}).code(plugintest.ProjectID); ci == nil || ci.Version != "0.3.0" || ci.File != "Version.xcconfig" {
		t.Fatalf("code %+v", ci)
	}
}

func TestPublishesItsOldNumbering(t *testing.T) {
	req := plugintest.Request(plugin.HookSchedule)
	req.Plugin.ID = "releases"
	req.ProjectSettings = map[string]map[string]any{plugintest.ProjectID: {"scheme": "Semantic (1.4.0)", "bump": "Patch"}}
	res := plugintest.Call(t, handle, req)
	var n numbering
	if !res.State.GetProject(plugintest.ProjectID, "public.numbering", &n) || n.Style != "semver" || n.Bump != "patch" {
		t.Fatalf("numbering %+v", n)
	}
	req.ProjectSettings[plugintest.ProjectID] = map[string]any{}
	res = plugintest.Call(t, handle, req)
	if raw := res.State.Projects[plugintest.ProjectID]["public.numbering"]; string(raw) != "null" {
		t.Fatalf("published %s with nothing set", raw)
	}
}

func TestReleaseChoicesCarryTheirShipDay(t *testing.T) {
	req := plugintest.Request(plugin.HookSchedule)
	req.Plugin.ID = "releases"
	req.ProjectSettings = map[string]map[string]any{plugintest.ProjectID: {}}
	res := plugintest.Call(t, handle, req)
	rels := planned(t, req)

	req.Hook = plugin.HookRender
	req.Slots = []string{plugin.SlotWorkFields}
	req.State = *res.State
	out := plugintest.Call(t, handle, req)
	for _, o := range out.Fields["release"].Options {
		if o.Value == rels[0].ID {
			if o.Day != rels[0].ShipDate || o.Day == "" {
				t.Fatalf("day %q, ships %q", o.Day, rels[0].ShipDate)
			}
			return
		}
	}
	t.Fatalf("no choice for %s in %+v", rels[0].ID, out.Fields)
}

func TestReleasesPageLeadsWithTheNextRelease(t *testing.T) {
	req := plugintest.Request(plugin.HookSchedule)
	req.Plugin.ID = "releases"
	req.ProjectSettings = map[string]map[string]any{plugintest.ProjectID: {}}
	res := plugintest.Call(t, handle, req)
	rels := planned(t, req)

	req.Hook, req.Page, req.State = plugin.HookPage, "releases", *res.State
	pg := plugintest.Call(t, handle, req).Page
	if pg == nil || !strings.HasPrefix(pg.Title, rels[0].Version+" ships ") {
		t.Fatalf("title %+v", pg)
	}
	if pg.Blocks[0].Type != plugin.BlockStats || pg.Blocks[0].Tone != "figs" || pg.Blocks[0].Items[0].Title != "done" {
		t.Fatalf("figures %+v", pg.Blocks[0])
	}
	heads := []string{}
	for _, b := range pg.Blocks {
		if b.Type == plugin.BlockHeading {
			heads = append(heads, b.Text)
		}
	}
	if len(heads) < 2 || heads[0] != "Next release" || heads[1] != "After that" {
		t.Fatalf("headings %v", heads)
	}
}
