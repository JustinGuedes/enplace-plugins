package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/plugins/releases/cycle"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

// refresh plans, applies the rules (when tick is set) and stores the
// project's releases, counts and status.
func (c *cycleRun) refresh(p *project, tick bool) {
	if p == nil {
		return
	}
	c.res.SetProject(p.ID, "public.numbering", oldNumbering(c.req.ProjectSettings[p.ID]))
	hasWork := func(id string) bool {
		for _, it := range p.items {
			if it.Release == id {
				return true
			}
		}
		return false
	}
	// A new version style renumbers the planned releases nothing is in yet.
	style := p.set.Scheme + "/" + p.set.Bump
	var was string
	stale := c.req.State.GetProject(p.ID, keyStyle, &was) && was != style
	if stale || p.onlyContinuous() {
		// With work shipping as it's done, slots planned on the cadence
		// that nothing is in yet go too.
		kept := p.rels[:0]
		for _, r := range p.rels {
			if r.State == cycle.Planned && r.ShipDate >= c.today.Format(day) && !hasWork(r.ID) && r.WhatsNew == "" {
				continue
			}
			kept = append(kept, r)
		}
		p.rels = kept
	}
	if was != style {
		c.res.SetProject(p.ID, keyStyle, style)
	}
	// A new cadence lets releases moved by hand follow it again.
	cadence := cadenceKey(p.set)
	var had string
	if c.req.State.GetProject(p.ID, keyCadence, &had) && had != cadence {
		cycle.Unpin(p.rels)
	}
	if had != cadence {
		c.res.SetProject(p.ID, keyCadence, cadence)
	}
	plan := p.set
	plan.Enabled = p.cadence
	plan.Anchor = c.anchor(p, had == cadence)
	if c.anchors == nil {
		c.anchors = map[string]string{}
	}
	c.anchors[p.ID] = plan.Anchor
	p.rels = cycle.Plan(p.rels, plan, c.today, p.live, hasWork, c.newID)
	c.tidyFields(p)
	c.deploys(p)
	cycle.Count(p.rels, p.items, p.set.Rules)
	if tick && p.set.Enabled {
		moves, _ := cycle.Tick(p.rels, p.items, c.today.Format(day))
		for _, m := range moves {
			c.setRelease(p, m.Work, m.To)
		}
		c.followStore(p)
		p.rels = cycle.Plan(p.rels, plan, c.today, p.live, hasWork, c.newID)
		cycle.Count(p.rels, p.items, p.set.Rules)
	}
	c.res.SetProject(p.ID, keyReleases, p.rels)
	c.res.SetProject(p.ID, keyMode, map[string]bool{"continuous": p.continuous, "cadence": p.cadence})
	if c.fresh == nil {
		c.fresh = map[string][]cycle.Release{}
	}
	c.fresh[p.ID] = p.rels
	if c.res.Status == nil {
		c.res.Status = map[string]*plugin.ProjectStatus{}
	}
	c.res.Status[p.ID] = c.status(p)
}

// anchor is the ship day the cadence counts from, so ship days stay put
// as the days go by: the one set by hand, or else the one remembered for
// this cadence (same says it hasn't changed), or else one picked now from
// the releases there are, and remembered.
func (c *cycleRun) anchor(p *project, same bool) string {
	if p.set.Anchor != "" {
		return p.set.Anchor
	}
	var kept string
	if same && c.req.State.GetProject(p.ID, keyAnchor, &kept) {
		if _, err := time.Parse(day, kept); err == nil {
			return kept
		}
	}
	a := cycle.DefaultAnchor(p.rels, p.set, c.today)
	if a != kept {
		c.res.SetProject(p.ID, keyAnchor, a)
	}
	return a
}

// setRelease moves a work item to a release, here and in Enplace.
func (c *cycleRun) setRelease(p *project, workID, rel string) {
	for i := range p.items {
		if p.items[i].ID == workID {
			p.items[i].Release = rel
		}
	}
	c.res.Ops = append(c.res.Ops, plugin.Op{Kind: "work.fields", ProjectID: p.ID, Params: map[string]any{"id": workID, "fields": map[string]string{c.field("release"): rel}}})
}

// tidyFields turns what agents may write in the Release field ("next", or
// a version like 1.5) into the release itself.
func (c *cycleRun) tidyFields(p *project) {
	today := c.today.Format(day)
	for _, w := range p.work {
		v := w.Fields[c.field("release")]
		// Work waiting to ship follows how the project ships now.
		switch {
		case v == unplanned && p.onlyContinuous():
			c.setRelease(p, w.ID, deploy)
			continue
		case v == deploy && !p.continuous:
			c.setRelease(p, w.ID, unplanned)
			continue
		case strings.EqualFold(v, "next") && p.onlyContinuous():
			c.setRelease(p, w.ID, deploy)
			continue
		}
		if v == general || v == unplanned || v == deploy {
			continue
		}
		found, byVersion, next := false, "", ""
		for _, r := range p.rels {
			if r.ID == v {
				found = true
			}
			if strings.EqualFold(r.Version, strings.TrimPrefix(strings.ToLower(v), "v")) {
				byVersion = r.ID
			}
			if next == "" && r.Open() && r.ShipDate >= today {
				next = r.ID
			}
		}
		switch {
		case found:
		case strings.EqualFold(v, "next") && next != "":
			c.setRelease(p, w.ID, next)
		case byVersion != "":
			c.setRelease(p, w.ID, byVersion)
		}
	}
}

// deploys records work that shipped as soon as it was done: each day's
// finished work is one deploy, numbered on from the newest version.
func (c *cycleRun) deploys(p *project) {
	if !p.continuous {
		return
	}
	var out []plugin.Work
	for _, w := range p.work {
		if w.Done() && w.Fields[c.field("release")] == deploy {
			out = append(out, w)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].DoneAt.Before(out[j].DoneAt) })
	for _, w := range out {
		when := c.today.Format(day)
		if !w.DoneAt.IsZero() {
			when = w.DoneAt.In(c.loc).Format(day)
		}
		var r *cycle.Release
		for i := range p.rels {
			if p.rels[i].Deploy && p.rels[i].ShipDate == when {
				r = &p.rels[i]
			}
		}
		if r == nil {
			p.rels = append(p.rels, cycle.Release{ID: c.newID(), Version: c.nextVersion(p, when), ShipDate: when, State: cycle.Live, Deploy: true, CreatedAt: c.req.Now})
			r = &p.rels[len(p.rels)-1]
		}
		c.setRelease(p, w.ID, r.ID)
		// It's in a release now, so it's counted with it.
		for i := range p.items {
			if p.items[i].ID == w.ID {
				p.items[i].Release = r.ID
			}
		}
	}
	cycle.Sort(p.rels)
}

// nextVersion is the version a new deploy gets: a patch on the newest.
func (c *cycleRun) nextVersion(p *project, when string) string {
	high := p.live
	for _, r := range p.rels {
		if high == "" || cycle.VersionLess(high, r.Version) {
			high = r.Version
		}
	}
	if s := p.set.NextVersion; s != "" && (high == "" || cycle.VersionLess(high, s)) {
		return s
	}
	return cycle.Bump(high, p.set.Scheme, "patch", when)
}

// waiting is the work that ships as soon as it's done, not done yet.
func (c *cycleRun) waiting(p *project) []string {
	ids := []string{}
	for _, w := range p.work {
		if !w.Done() && w.Fields[c.field("release")] == deploy {
			ids = append(ids, w.ID)
		}
	}
	return ids
}

// lastDeploy is the project's newest deploy.
func (p *project) lastDeploy() *cycle.Release {
	for i := len(p.rels) - 1; i >= 0; i-- {
		if p.rels[i].Deploy {
			return &p.rels[i]
		}
	}
	return nil
}

// fixes says how many pieces of work went out, like "3 fixes".
func fixes(n int) string {
	if n == 1 {
		return "1 fix"
	}
	return fmt.Sprintf("%d fixes", n)
}

// deployLine is a project's line when its work ships as it's done, like
// "Ships when done · 2 waiting · 1.4.3 out Tue".
func (c *cycleRun) deployLine(p *project) string {
	parts := []string{"Ships when done"}
	if n := len(c.waiting(p)); n > 0 {
		parts = append(parts, fmt.Sprintf("%d waiting", n))
	}
	if r := p.lastDeploy(); r != nil {
		parts = append(parts, r.Version+" out "+c.dayWord(r.ShipDate))
	}
	return strings.Join(parts, " · ")
}

// followStore moves releases along as a store plugin reports them.
func (c *cycleRun) followStore(p *project) {
	for _, id := range sortedKeys(c.req.Peers) {
		var versions []struct {
			Version    string `json:"version"`
			State      string `json:"state"`
			StoreState string `json:"store_state"`
		}
		found := false
		for _, scope := range c.riding(p.ID) {
			if c.req.Peers[id].GetProject(scope, "public.store_versions", &versions) {
				found = true
				break
			}
		}
		if !found {
			continue
		}
		for _, v := range versions {
			for i := range p.rels {
				r := &p.rels[i]
				if r.Version != v.Version || r.State == cycle.Skipped {
					continue
				}
				r.StoreState = v.StoreState
				if cycle.Further(v.State, r.State) {
					r.State = v.State
				}
			}
		}
	}
}

func (c *cycleRun) dayWord(date string) string {
	d, err := time.Parse(day, date)
	if err != nil {
		return date
	}
	switch days := int(d.Sub(c.today).Hours() / 24); {
	case days == 0:
		return "today"
	case days == 1:
		return "tomorrow"
	case days == -1:
		return "yesterday"
	case days > 1 && days < 7:
		return d.Format("Mon")
	default:
		return d.Format("2 Jan")
	}
}

func (c *cycleRun) shortDate(date string) string {
	d, err := time.Parse(day, date)
	if err != nil {
		return date
	}
	return d.Format("Mon 2 Jan")
}

// next is the project's next release still to ship.
func (p *project) next(today string) *cycle.Release {
	for i := range p.rels {
		if p.rels[i].Open() && p.rels[i].ShipDate >= today {
			return &p.rels[i]
		}
	}
	return nil
}

// status is the project's line on the rail, like "1.4 live · 1.5 ships Fri · 2 of 5".
func (c *cycleRun) status(p *project) *plugin.ProjectStatus {
	if p.onlyContinuous() {
		return &plugin.ProjectStatus{Text: c.deployLine(p)}
	}
	r := p.next(c.today.Format(day))
	if r == nil {
		return nil
	}
	if r.Total > 0 && r.Done == 0 && r.Verdict == "skip" {
		zero := 0.0
		return &plugin.ProjectStatus{Text: r.Version + " will skip: nothing done", Progress: &zero}
	}
	parts := []string{}
	if p.live != "" {
		parts = append(parts, p.live+" live")
	}
	parts = append(parts, fmt.Sprintf("%s ships %s", r.Version, c.dayWord(r.ShipDate)))
	st := &plugin.ProjectStatus{}
	if r.Total > 0 {
		parts = append(parts, fmt.Sprintf("%d of %d", r.Done, r.Total))
		v := float64(r.Done) / float64(r.Total)
		st.Progress = &v
	}
	st.Text = strings.Join(parts, " · ")
	return st
}

// calendar puts every project's ship days on the calendar.
func (c *cycleRun) calendar() {
	cal := &plugin.Calendar{Events: []plugin.CalendarEvent{}}
	for _, x := range c.req.Projects {
		if !x.On {
			continue
		}
		rels, ok := c.fresh[x.ID]
		if !ok {
			c.req.State.GetProject(x.ID, keyReleases, &rels)
		}
		for _, r := range rels {
			if !r.Open() {
				continue
			}
			d, err := time.ParseInLocation(day, r.ShipDate, c.loc)
			if err != nil {
				continue
			}
			cal.Events = append(cal.Events, plugin.CalendarEvent{ID: r.ID, Title: x.Name + " " + r.Version + " ships", Start: d, End: d.AddDate(0, 0, 1), AllDay: true, Free: true, Calendar: x.ID, Source: "Releases"})
		}
	}
	c.res.Calendar = cal
}
