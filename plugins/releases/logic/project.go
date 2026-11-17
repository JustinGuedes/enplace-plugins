package main

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/JustinGuedes/enplace-plugins/plugins/releases/cycle"
)

// load reads a project's settings, releases and work.
func (c *cycleRun) load(pid string) *project {
	if pid == "" {
		return nil
	}
	p := &project{}
	for _, x := range c.req.Projects {
		if x.ID == pid {
			p.Project = x
		}
	}
	if p.ID == "" || !p.On {
		return nil
	}
	p.set = settings(c.req.ProjectSettings[pid])
	c.numberBy(p)
	p.follows = c.follow(p)
	c.req.State.GetProject(pid, keyReleases, &p.rels)
	cycle.Sort(p.rels)
	known := map[string]bool{}
	for _, r := range p.rels {
		known[r.ID] = true
	}
	for _, w := range c.req.Work {
		if w.ProjectID != pid {
			continue
		}
		p.work = append(p.work, w)
		rel := w.Fields[c.field("release")]
		if !known[rel] {
			rel = ""
		}
		p.items = append(p.items, cycle.Item{ID: w.ID, Release: rel, Done: w.Done(), Priority: w.Priority, MustShip: w.Fields[c.field("must_ship")] != ""})
	}
	// A store plugin on the project may know what's live.
	// (On the project, a product sharing its cycle may know.)
find:
	for _, scope := range c.riding(pid) {
		for _, id := range sortedKeys(c.req.Peers) {
			var v struct {
				Version string `json:"version"`
			}
			if c.req.Peers[id].GetProject(scope, "public.version", &v) && v.Version != "" {
				p.live = v.Version
				break find
			}
		}
	}
	p.continuous = c.req.ProjectSettings[pid]["ships"] == shipsAsDone
	for _, id := range sortedKeys(c.req.Peers) {
		var ships string
		if c.req.Peers[id].GetProject(pid, "public.ships", &ships) && ships == "continuous" {
			p.continuous = true
		}
	}
	p.cadence = p.set.Enabled && (!p.continuous || c.kinds(pid)["app"])
	p.code = c.code(pid)
	cycle.Count(p.rels, p.items, p.set.Rules)
	return p
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// kind is what ships: "app", "site" or "" when nothing says. A product's
// type says first, then what plugins published.
func (c *cycleRun) kind(pid string) string {
	if k := c.productKind(pid); k != "" {
		return k
	}
	// A project's cycle shared by products of one kind.
	if ks := c.kinds(pid); len(ks) == 1 {
		for k := range ks {
			return k
		}
	}
	for _, id := range sortedKeys(c.req.Peers) {
		var k string
		if c.req.Peers[id].GetProject(pid, "public.kind", &k) && k != "" {
			return k
		}
	}
	return ""
}

// riding is where what ships in a cycle is published: the cycle's own
// project or product, and on a project, the products that share its cycle
// (they have no cycle of their own).
func (c *cycleRun) riding(pid string) []string {
	out := []string{pid}
	for _, x := range c.req.Projects {
		if x.ID != pid {
			continue
		}
		for _, pr := range x.Products {
			if !pr.On {
				out = append(out, pid+"/"+pr.Key)
			}
		}
	}
	return out
}

// productKind is what a product's type says ships: "app", "site" or "".
func (c *cycleRun) productKind(pid string) string {
	for _, x := range c.req.Projects {
		if x.ID == pid {
			switch x.ProductType {
			case "app":
				return "app"
			case "website":
				return "site"
			}
		}
	}
	return ""
}

// kinds is everything that ships in a project, by its type and what
// plugins said.
func (c *cycleRun) kinds(pid string) map[string]bool {
	out := map[string]bool{}
	if k := c.productKind(pid); k != "" {
		out[k] = true
	}
	// On the project, the products that share its cycle ship here too.
	for _, x := range c.req.Projects {
		if x.ID != pid {
			continue
		}
		for _, pr := range x.Products {
			if pr.On {
				continue
			}
			switch pr.Type {
			case "app":
				out["app"] = true
			case "website":
				out["site"] = true
			}
		}
	}
	for _, id := range sortedKeys(c.req.Peers) {
		var k string
		if c.req.Peers[id].GetProject(pid, "public.kind", &k) && k != "" {
			out[k] = true
		}
	}
	return out
}

// settings reads a project's settings as the manifest declares them.
func settings(m map[string]any) cycle.Settings {
	s := cycle.Defaults()
	str := func(k string) string { v, _ := m[k].(string); return v }
	if v, ok := m["enabled"].(bool); ok {
		s.Enabled = v
	}
	if n, err := strconv.Atoi(str("every_weeks")); err == nil {
		s.EveryWeeks = n
	}
	for i, d := range weekdays {
		if d == str("weekday") {
			s.Weekday = i
		}
	}
	switch n := m["cutoff_days"].(type) {
	case float64:
		s.CutoffDays = int(n)
	case string:
		if v, err := strconv.Atoi(n); err == nil {
			s.CutoffDays = v
		}
	}
	s.NextVersion = str("next_version")
	s.Anchor = str("anchor")
	pick := func(k string, opts map[string]string, into *string) {
		if v, ok := opts[str(k)]; ok {
			*into = v
		}
	}
	pick("scheme", map[string]string{"Short (1.4)": "short", "Semantic (1.4.0)": "semver", "Calendar (2026.11)": "calver", "Number (14)": "number"}, &s.Scheme)
	pick("bump", map[string]string{"Major": "major", "Minor": "minor", "Patch": "patch"}, &s.Bump)
	pick("nothing_done", map[string]string{"Skip the release": "skip", "Ask me": "ask"}, &s.Rules.NothingDone)
	pick("partly_done", map[string]string{"Ship what's done": "ship_done", "Wait for everything": "wait", "Ask me": "ask"}, &s.Rules.PartlyDone)
	pick("must_ship_late", map[string]string{"Ask me": "ask", "Hold the release": "hold", "Skip the release": "skip", "Ship, then hotfix": "hotfix"}, &s.Rules.MustShipLate)
	switch str("must_ship_from") {
	case "High and up":
		s.Rules.MustShipFrom = 3
	case "Medium and up":
		s.Rules.MustShipFrom = 2
	case "Low and up":
		s.Rules.MustShipFrom = 1
	}
	return s.Tidy()
}

// follow gives a product that follows its project's cadence the
// project's dates: how often, the ship day, the cut-off and the day the
// cadence counts from, so their releases land on the same days. Its
// versions, ship rules and how its work ships stay its own. It says
// whether it does: the project needs a cycle of its own to follow.
func (c *cycleRun) follow(p *project) bool {
	if p.Parent == "" || c.req.ProjectSettings[p.ID]["follow_project"] != true {
		return false
	}
	on := false
	for _, x := range c.req.Projects {
		if x.ID == p.Parent {
			on = x.On
		}
	}
	if !on {
		return false
	}
	ps := settings(c.req.ProjectSettings[p.Parent])
	p.set.EveryWeeks, p.set.Weekday, p.set.CutoffDays = ps.EveryWeeks, ps.Weekday, ps.CutoffDays
	p.set.Anchor = c.parentAnchor(p.Parent, ps)
	return true
}

// parentAnchor is the day a project's cadence counts from: set by hand,
// worked out in this call, remembered for its cadence, or else picked from
// its releases the way the project itself would.
func (c *cycleRun) parentAnchor(pid string, s cycle.Settings) string {
	if s.Anchor != "" {
		return s.Anchor
	}
	if a, ok := c.anchors[pid]; ok {
		return a
	}
	var had, kept string
	if c.req.State.GetProject(pid, keyCadence, &had) && had == cadenceKey(s) && c.req.State.GetProject(pid, keyAnchor, &kept) {
		if _, err := time.Parse(day, kept); err == nil {
			return kept
		}
	}
	rels, ok := c.fresh[pid]
	if !ok {
		c.req.State.GetProject(pid, keyReleases, &rels)
		cycle.Sort(rels)
	}
	return cycle.DefaultAnchor(rels, s, c.today)
}

// cadenceKey sums up the dates of a cadence, to tell when they change.
func cadenceKey(s cycle.Settings) string {
	k := fmt.Sprintf("%d/%d/%d", s.EveryWeeks, s.Weekday, s.CutoffDays)
	if s.Anchor != "" {
		k += "/" + s.Anchor
	}
	return k
}

var weekdays = []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
