package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/plugins/releases/cycle"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

func (c *cycleRun) action(p *project, a *plugin.Action) error {
	args := map[string]string{}
	_ = json.Unmarshal(a.Args, &args)
	find := func() *cycle.Release {
		for i := range p.rels {
			if p.rels[i].ID == args["id"] {
				return &p.rels[i]
			}
		}
		return nil
	}
	date := func(k string, empty bool) (string, error) {
		v := strings.TrimSpace(args[k])
		if v == "" && empty {
			return "", nil
		}
		if _, err := time.Parse(day, v); err != nil {
			return "", fmt.Errorf("That date should look like 2026-11-20")
		}
		return v, nil
	}
	switch a.Name {
	case "add":
		v := strings.TrimSpace(args["version"])
		if v == "" || len(v) > 20 {
			return fmt.Errorf("A version looks like 1.4")
		}
		ship, err := date("ship_date", false)
		if err != nil {
			return err
		}
		t, _ := time.Parse(day, ship)
		p.rels = append(p.rels, cycle.Release{ID: c.newID(), Version: v, ShipDate: ship, Cutoff: t.AddDate(0, 0, -p.set.CutoffDays).Format(day), State: cycle.Planned, CreatedAt: c.req.Now})
		c.res.Log = fmt.Sprintf("Added %s %s shipping %s", p.Name, v, ship)
		c.res.Toast = "Added " + v
	case "save":
		r := find()
		if r == nil {
			return fmt.Errorf("That release is gone")
		}
		before := *r
		if _, ok := args["ship_date"]; ok {
			ship, err := date("ship_date", false)
			if err != nil {
				return err
			}
			if ship != r.ShipDate {
				id := r.ID
				moved, ok := cycle.MoveDates(p.rels, id, ship, args["follow"] != "no")
				if !ok {
					return fmt.Errorf("%s has shipped, so its dates stay as they are", r.Version)
				}
				r = find()
				// The cut-off is carried along; one sent unchanged stays carried.
				if args["cutoff"] == before.Cutoff {
					delete(args, "cutoff")
				}
				before.Cutoff = r.Cutoff
				if len(moved) > 0 {
					vs := []string{}
					for _, m := range moved {
						vs = append(vs, m.Version)
					}
					c.res.Log = fmt.Sprintf(", %s moved along", strings.Join(vs, " and "))
				}
			}
		}
		if _, ok := args["cutoff"]; ok {
			cut, err := date("cutoff", true)
			if err != nil {
				return err
			}
			r.Cutoff = cut
		}
		if v := strings.TrimSpace(args["version"]); v != "" && len(v) <= 20 {
			r.Version = v
		}
		for st, name := range cycle.StateNames {
			if args["state"] == name || args["state"] == st {
				r.State = st
			}
		}
		if wn, ok := args["whats_new"]; ok {
			if len(wn) > 4000 {
				return fmt.Errorf("What's new is limited to 4000 characters")
			}
			if wn != r.WhatsNew {
				r.WhatsNewApproved = false
			}
			r.WhatsNew = wn
		}
		c.res.Log = p.Name + " " + summary(before, *r) + c.res.Log
		c.res.Toast = "Saved " + r.Version
	case "draft":
		r := find()
		if r == nil {
			return fmt.Errorf("That release is gone")
		}
		done := []plugin.Work{}
		for _, w := range p.work {
			if w.Done() && w.Fields[c.field("release")] == r.ID {
				done = append(done, w)
			}
		}
		if len(done) == 0 {
			return fmt.Errorf("Nothing is done in %s yet", r.Version)
		}
		sort.SliceStable(done, func(i, j int) bool { return done[i].Priority > done[j].Priority })
		titles := []string{}
		for _, w := range done {
			titles = append(titles, w.Title)
		}
		r.WhatsNew, r.WhatsNewApproved = cycle.DraftWhatsNew(r.Version, titles), false
		c.res.Toast = "Drafted from what's done. Edit it, then approve."
	case "skip":
		r := find()
		if r == nil {
			return fmt.Errorf("That release is gone")
		}
		version := r.Version
		next, moves, ok := cycle.Skip(p.rels, p.items, r.ID)
		if !ok {
			return fmt.Errorf("%s has gone to the store, so it can't be skipped here", version)
		}
		to, where := next, unplanned
		if to == "" {
			to = unplanned
		}
		for _, rel := range p.rels {
			if rel.ID == next {
				where = rel.Version
			}
		}
		for _, m := range moves {
			c.setRelease(p, m.Work, to)
		}
		c.res.Log = p.Name + " " + version + " skipped by hand"
		switch {
		case len(moves) == 0:
			c.res.Toast = "Skipped " + version
		case next == "":
			c.res.Toast = fmt.Sprintf("Skipped %s. %s now waiting for a release", version, plural(len(moves), "item is", "items are"))
		default:
			c.res.Toast = fmt.Sprintf("Skipped %s and moved %s to %s", version, plural(len(moves), "item", "items"), where)
		}
		if len(moves) > 0 {
			c.res.Log += fmt.Sprintf(", %d unfinished moved on", len(moves))
		}
	case "unskip":
		r := find()
		if r == nil || r.State != cycle.Skipped {
			return fmt.Errorf("That release isn't skipped")
		}
		r.State = cycle.Planned
		c.res.Log = p.Name + " " + r.Version + " brought back"
		c.res.Toast = "Brought back " + r.Version
	case "approve":
		r := find()
		if r == nil || strings.TrimSpace(r.WhatsNew) == "" {
			return fmt.Errorf("Write What's new first")
		}
		r.WhatsNewApproved = true
		c.res.Log = "What's new for " + p.Name + " " + r.Version + " approved"
		c.res.Toast = "Approved"
	default:
		return fmt.Errorf("Release cycle doesn't know how to %s", a.Name)
	}
	cycle.Sort(p.rels)
	return nil
}

// plural says n with the one or many form after it, like "3 items".
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func summary(before, after cycle.Release) string {
	switch {
	case before.State != after.State:
		return after.Version + " is now " + strings.ToLower(cycle.StateNames[after.State])
	case before.WhatsNew != after.WhatsNew:
		return "What's new for " + after.Version + " edited"
	case before.ShipDate != after.ShipDate:
		return after.Version + " moved to " + after.ShipDate
	case before.Version != after.Version:
		return before.Version + " renamed to " + after.Version
	}
	return after.Version + " updated"
}

// toolReleases answers the list_releases tool.
func (c *cycleRun) toolReleases() any {
	p := c.load(c.req.ProjectID)
	if p == nil {
		return map[string]any{"releases": []any{}}
	}
	type out struct {
		ID       string `json:"id"`
		Version  string `json:"version"`
		ShipDate string `json:"ship_date"`
		Cutoff   string `json:"cutoff,omitempty"`
		State    string `json:"state"`
		Total    int    `json:"work"`
		Done     int    `json:"done"`
		Rules    string `json:"at_cutoff,omitempty"`
		WhatsNew string `json:"whats_new,omitempty"`
	}
	list := []out{}
	for _, r := range p.rels {
		o := out{r.ID, r.Version, r.ShipDate, r.Cutoff, r.State, r.Total, r.Done, cycle.VerdictText(r), r.WhatsNew}
		if r.Deploy {
			o.Rules = "Deployed as the work was done"
		}
		list = append(list, o)
	}
	how := "Put work in a release by setting its field " + c.field("release") + " to the release's id, a version, or \"next\"; \"unplanned\" is work that ships but isn't planned yet, and empty is general work that never ships."
	if p.onlyContinuous() {
		how = "Work here ships as soon as it's done: set its field " + c.field("release") + " to \"deploy\", and each day's finished work is recorded as one deploy. Empty is general work that never ships."
	} else if p.continuous {
		how += " \"deploy\" is site work that ships as soon as it's done."
	}
	var code any
	if p.code != nil {
		code = map[string]any{"version": p.code.Version, "file": p.code.File, "repo": p.code.Repo, "branch": p.code.Branch, "latest_tag": p.code.Tag, "commits_since_tag": p.code.Ahead}
	}
	return map[string]any{"project": p.Key, "releases": list, "ships_when_done": p.continuous, "code": code,
		"how_to": how + " Set " + c.field("must_ship") + " to \"yes\" for work that must ship."}
}
