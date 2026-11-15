// Command logic is the Release lanes plugin: a work view with a lane per
// upcoming release. It reads the releases Release cycle publishes and
// groups the project's work by its Release field; Enplace draws the lanes
// and sets the field when a card is dropped in one.
package main

import (
	"fmt"
	"sort"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

func main() { plugin.Run(handle) }

// The plugin this one builds on, and its Release field on work.
const (
	cycle     = "releases"
	field     = cycle + ".release"
	unplanned = "unplanned"
	deploy    = "deploy" // ships as soon as it's done
)

// release is what Release cycle publishes for each release.
type release struct {
	ID       string `json:"id"`
	Version  string `json:"version"`
	Cutoff   string `json:"cutoff"`
	ShipDate string `json:"ship_date"`
	State    string `json:"state"`
	Total    int    `json:"total"`
	Done     int    `json:"done"`
	Verdict  string `json:"verdict"`
}

func (r release) open() bool { return r.State != "live" && r.State != "skipped" }

func handle(req *plugin.Request) (*plugin.Response, error) {
	res := &plugin.Response{}
	if req.Hook != plugin.HookView || req.View != "lanes" || req.ProjectID == "" {
		return res, nil
	}
	var rels []release
	req.Peers[cycle].GetProject(req.ProjectID, "public.releases", &rels)
	sort.SliceStable(rels, func(i, j int) bool { return rels[i].ShipDate < rels[j].ShipDate })
	noun := "Ships"
	for _, id := range []string{"app", "website"} {
		var k string
		if req.Peers[id].GetProject(req.ProjectID, "public.kind", &k) {
			if n, ok := map[string]string{"app": "App work", "site": "Site work"}[k]; ok {
				noun = n
			}
			break
		}
	}
	// How the project ships: on the cadence, as work is done, or both.
	mode := struct {
		Continuous bool `json:"continuous"`
		Cadence    bool `json:"cadence"`
	}{Cadence: true}
	req.Peers[cycle].GetProject(req.ProjectID, "public.mode", &mode)
	by := map[string][]string{}
	lane := map[string]bool{unplanned: mode.Cadence, deploy: mode.Continuous}
	for _, r := range rels {
		if r.open() {
			lane[r.ID] = true
		}
	}
	shipped := map[string]bool{}
	for _, r := range rels {
		if !r.open() {
			shipped[r.ID] = true
		}
	}
	for _, w := range req.Work {
		if w.ProjectID != req.ProjectID {
			continue
		}
		v := w.Fields[field]
		switch {
		case shipped[v]:
			continue // it went out with its release
		case !lane[v]:
			v = ""
		}
		by[v] = append(by[v], w.ID)
	}
	view := &plugin.View{Groups: []plugin.Group{}}
	shown := 0
	for _, r := range rels {
		if !r.open() || (shown >= 4 && len(by[r.ID]) == 0) {
			continue
		}
		shown++
		g := plugin.Group{Key: r.ID, Title: r.Version, Sub: "Ships " + short(r.ShipDate), Work: ids(by[r.ID]), Set: map[string]string{field: r.ID},
			Empty: "Nothing planned. Drop work here to ship it in " + r.Version + ".", Add: "Add to " + r.Version, Accent: shown == 1}
		if r.Cutoff != "" {
			g.Sub += " · cut-off " + short(r.Cutoff)
		}
		if r.Total > 0 {
			g.Badge = fmt.Sprintf("%d of %d", r.Done, r.Total)
		}
		if line := verdict(r.Verdict); line != "" {
			g.Sub += " · " + line
		}
		view.Groups = append(view.Groups, g)
	}
	if mode.Continuous {
		title := "Ships when done"
		if noun == "Site work" || !mode.Cadence {
			title = "Site work, ships when done"
		}
		view.Groups = append(view.Groups, plugin.Group{Key: deploy, Title: title, Sub: "Goes out as soon as it's done", Accent: shown == 0, Work: ids(by[deploy]), Set: map[string]string{field: deploy},
			Empty: "Fixes that deploy as soon as they're done wait here.", Add: "Add a fix"})
	}
	if mode.Cadence {
		view.Groups = append(view.Groups, plugin.Group{Key: unplanned, Title: noun + ", not in a release yet", Muted: true, Work: ids(by[unplanned]), Set: map[string]string{field: unplanned},
			Empty: "Work that ships but isn't planned into a release waits here.", Add: "Add for a later release"})
	}
	view.Groups = append(view.Groups,
		plugin.Group{Key: "general", Title: "General", Sub: "Project work that isn't part of a release", Muted: true, Work: ids(by[""]), Set: map[string]string{field: ""},
			Empty: "Work that never ships, like planning or marketing.", Add: "Add general work"})
	res.View = view
	return res, nil
}

func ids(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func short(date string) string {
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	return d.Format("Mon 2 Jan")
}

func verdict(v string) string {
	switch v {
	case "skip":
		return "skips itself if nothing is done"
	case "ship_done":
		return "ships what's done"
	case "hold":
		return "holds for must-ship work"
	case "hotfix":
		return "ships, with a hotfix to follow"
	case "wait":
		return "waits until everything is done"
	case "ask":
		return "you'll be asked at cut-off"
	}
	return ""
}
