package main

import (
	"strings"
	"testing"

	"github.com/JustinGuedes/enplace-plugins/plugins/releases/cycle"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin/plugintest"
)

// shippingTogether is a project ("Enplace") whose two products have
// cycles of their own, both shipping on Thursday 5 March, and the web
// one again a week later. The project has a cycle too, with a release
// on the Thursday.
func shippingTogether(hook string) *plugin.Request {
	req := plugintest.Request(hook)
	req.Plugin.ID = "releases"
	p := &req.Projects[0]
	p.Key, p.Name, p.Colour = "ENP", "Enplace", "#ff8c42"
	p.Products = []plugin.ProductInfo{{Key: "web", Name: "Enplace Web", Type: "website", On: true}, {Key: "apple", Name: "Enplace Apple", Type: "app", On: true}}
	req.Projects = append(req.Projects,
		plugin.Project{ID: "p1/web", Key: "ENP", Name: "Enplace · Enplace Web", Colour: "#5ee0c0", On: true, Product: "web", ProductName: "Enplace Web", ProductType: "website", Parent: "p1"},
		plugin.Project{ID: "p1/apple", Key: "ENP", Name: "Enplace · Enplace Apple", Colour: "#8fb4ff", On: true, Product: "apple", ProductName: "Enplace Apple", ProductType: "app", Parent: "p1"},
		plugin.Project{ID: "p2", Key: "TOC", Name: "Tocka", Colour: "#ffc069", On: true},
	)
	req.ProjectSettings = map[string]map[string]any{"p1": {"enabled": false}, "p1/web": {"enabled": false}, "p1/apple": {"enabled": false}, "p2": {"enabled": false}}
	put := func(pid string, rels ...cycle.Release) { req.State.PutProject(pid, keyReleases, rels) }
	put("p1", cycle.Release{ID: "a", Version: "2.0", ShipDate: "2026-03-05", State: cycle.Planned})
	put("p1/web", cycle.Release{ID: "w1", Version: "1.6", ShipDate: "2026-03-05", State: cycle.Planned, Total: 5, Done: 3},
		cycle.Release{ID: "w2", Version: "1.7", ShipDate: "2026-03-12", State: cycle.Planned, Total: 1})
	put("p1/apple", cycle.Release{ID: "x1", Version: "1.4", ShipDate: "2026-03-05", Cutoff: "2026-03-03", State: cycle.Planned, Total: 4, Done: 2})
	put("p2", cycle.Release{ID: "t1", Version: "0.3", ShipDate: "2026-03-04", State: cycle.Planned, Total: 2, Done: 1})
	return req
}

func TestBriefSaysOneLineForADayProductsShare(t *testing.T) {
	req := shippingTogether(plugin.HookRender)
	req.Projects[3].On = false // only Enplace ships this week
	req.Slots = []string{plugin.SlotBrief}
	res := plugintest.Call(t, handle, req)
	if res.Brief == nil {
		t.Fatal("no brief")
	}
	want := "Enplace ships Thu: Apple 1.4 and Web 1.6, with 5 of 9 done."
	if res.Brief.Text != want {
		t.Fatalf("brief = %q, want %q", res.Brief.Text, want)
	}
	if res.Brief.Button == nil || res.Brief.Button.Href != "/p/enp/x/releases/releases" {
		t.Fatalf("button = %+v, want the project's Releases page", res.Brief.Button)
	}
}

func TestShippingSoonHasOneRowADay(t *testing.T) {
	req := shippingTogether(plugin.HookRender)
	req.Slots = []string{"card:soon"}
	res := plugintest.Call(t, handle, req)
	items := res.Cards["card:soon"][0].Items
	var got []string
	for _, it := range items {
		got = append(got, it.Title+" | "+it.Sub+" | "+it.Value)
	}
	want := []string{
		"Tocka 0.3 | Ships Wed · " + cycle.VerdictLine(cycle.Release{State: cycle.Planned, Total: 2, Done: 1}) + " | 1 of 2",
		"Enplace | Ships Thu · Apple 1.4 · Web 1.6 | 5 of 9",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("rows:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestWorkspaceReleasesGroupTheDay(t *testing.T) {
	req := shippingTogether(plugin.HookPage)
	req.Page, req.ProjectID = "all", ""
	res := plugintest.Call(t, handle, req)
	var titles []string
	for _, b := range res.Page.Blocks {
		for _, it := range b.Items {
			titles = append(titles, it.Title+" | "+it.Sub)
			if it.Title == "Enplace · Enplace Web 1.7" && it.Button.Href != "/p/enp/x/releases/releases?product=web" {
				t.Fatalf("a product's own release opens %q", it.Button.Href)
			}
		}
	}
	got := strings.Join(titles, "\n")
	if !strings.Contains(got, "Enplace | Ships Thu 5 Mar · Apple 1.4 · Enplace 2.0 · Web 1.6") {
		t.Fatalf("no shared row for 5 March:\n%s", got)
	}
	if !strings.Contains(got, "Enplace · Enplace Web 1.7") {
		t.Fatalf("no row for Web 1.7:\n%s", got)
	}
	if strings.Count(got, "5 Mar") != 1 {
		t.Fatalf("5 March shows more than once:\n%s", got)
	}
}

func TestProjectPageHasASectionADay(t *testing.T) {
	req := shippingTogether(plugin.HookPage)
	req.Page = "releases"
	res := plugintest.Call(t, handle, req)
	var day *plugin.Block
	var later bool
	for i, b := range res.Page.Blocks {
		if b.Type != plugin.BlockSection {
			continue
		}
		switch b.Text {
		case "Thu 5 Mar":
			day = &res.Page.Blocks[i]
		case "Thu 12 Mar":
			later = true
		case "2.0":
			t.Fatal("the project's own 2.0 shows apart from the day it shares")
		}
	}
	if day == nil || !later {
		t.Fatalf("want sections for 5 and 12 March, got %+v", res.Page.Blocks)
	}
	if day.Sub != "Apple 1.4 · Enplace 2.0 · Web 1.6" || day.Value != "5/9" {
		t.Fatalf("5 March section: sub %q value %q", day.Sub, day.Value)
	}
	var rows []string
	own := false
	for _, b := range day.Blocks {
		for _, it := range b.Items {
			rows = append(rows, it.Title)
		}
		if b.Type == plugin.BlockSection && b.Text == "2.0" {
			own = len(b.Fields) > 0
		}
	}
	if strings.Join(rows, ",") != "Apple 1.4,Web 1.6" || !own {
		t.Fatalf("rows %v, own release editable: %v", rows, own)
	}
}
