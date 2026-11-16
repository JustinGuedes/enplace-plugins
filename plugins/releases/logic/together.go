package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/JustinGuedes/enplace-plugins/plugins/releases/cycle"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

// A project's products can each have a Release cycle of their own, and
// when they follow the project's cadence their releases land on the same
// days. Releases shipping on the same day in one project are drawn as one
// shared release: the date, then each product's version and progress.

// coming is one release still to ship, with the project or product it's in.
type coming struct {
	p plugin.Project
	r cycle.Release
}

// together is the releases of one project shipping on one day.
type together struct {
	home plugin.Project // the project, even when only its products ship
	date string
	rels []coming
}

// upcoming is each project's and product's next releases still to ship
// (at most per of them, 0 for all), soonest first. withWork leaves out
// releases with nothing in them.
func (c *cycleRun) upcoming(per int, withWork bool) []coming {
	today := c.today.Format(day)
	var out []coming
	for _, x := range c.req.Projects {
		if !x.On {
			continue
		}
		var rels []cycle.Release
		c.req.State.GetProject(x.ID, keyReleases, &rels)
		cycle.Sort(rels)
		n := 0
		for _, r := range rels {
			if !r.Open() || r.Deploy || r.ShipDate < today || (withWork && r.Total == 0) {
				continue
			}
			out = append(out, coming{x, r})
			if n++; per > 0 && n >= per {
				break
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].r.ShipDate < out[j].r.ShipDate })
	return out
}

// group puts releases of one project shipping on the same day together,
// keeping them soonest first.
func (c *cycleRun) group(rels []coming) []together {
	var out []together
	at := map[string]int{}
	for _, x := range rels {
		home := c.home(x.p)
		k := home.ID + "|" + x.r.ShipDate
		i, ok := at[k]
		if !ok {
			i = len(out)
			at[k] = i
			out = append(out, together{home: home, date: x.r.ShipDate})
		}
		out[i].rels = append(out[i].rels, x)
	}
	for i := range out {
		sort.SliceStable(out[i].rels, func(a, b int) bool { return c.short(out[i].rels[a].p) < c.short(out[i].rels[b].p) })
	}
	return out
}

// home is the project a project or product belongs to.
func (c *cycleRun) home(p plugin.Project) plugin.Project {
	if p.Parent == "" {
		return p
	}
	for _, x := range c.req.Projects {
		if x.ID == p.Parent {
			return x
		}
	}
	return plugin.Project{ID: p.Parent, Key: p.Key, Name: p.Name, Colour: p.Colour}
}

// short names a project's or product's releases inside its project: the
// product's own name, or for the project's cycle, the products riding it.
func (c *cycleRun) short(p plugin.Project) string {
	if p.Product != "" {
		if n := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(p.ProductName), strings.TrimSpace(c.home(p).Name))); n != "" {
			return n
		}
		return p.ProductName
	}
	var names []string
	for _, pr := range p.Products {
		if !pr.On {
			names = append(names, pr.Name)
		}
	}
	if len(names) > 0 && len(names) < len(p.Products) {
		return strings.Join(names, " & ")
	}
	return p.Name
}

// shared reports whether more than one project or product ships that day.
func (t together) shared() bool { return len(t.rels) > 1 }

// name is what a day's releases are called: "Enplace Web 1.6" alone, or
// the project when several ship.
func (t together) name() string {
	if !t.shared() {
		return t.rels[0].p.Name + " " + t.rels[0].r.Version
	}
	return t.home.Name
}

// versions lists each product's version: "Web 1.6 · Apple 1.4".
func (c *cycleRun) versions(t together) string {
	var parts []string
	for _, x := range t.rels {
		parts = append(parts, c.short(x.p)+" "+x.r.Version)
	}
	return strings.Join(parts, " · ")
}

// counts adds up the work in a day's releases.
func (t together) counts() (done, total int) {
	for _, x := range t.rels {
		done += x.r.Done
		total += x.r.Total
	}
	return
}

// progress is how far a day's releases are, nil with nothing in them.
func (t together) progress() *float64 {
	done, total := t.counts()
	if total == 0 {
		return nil
	}
	v := float64(done) / float64(total)
	return &v
}

// cutoffToday is the first release in a day's that has its cut-off today.
func (t together) cutoffToday(today string) *coming {
	for i := range t.rels {
		if t.rels[i].r.Cutoff == today {
			return &t.rels[i]
		}
	}
	return nil
}

// link opens a day's releases: the project's Releases page when the
// project has a cycle (it shows them together), else the release's own.
func (c *cycleRun) link(t together) string {
	if t.shared() && t.home.On {
		return c.href(t.home)
	}
	return c.href(t.rels[0].p)
}

// each says how each release in a day's is going: "Web 1.6 has 3 of 5
// done and Apple 1.4 has 2 of 4".
func (c *cycleRun) each(t together) string {
	var parts []string
	for _, x := range t.rels {
		parts = append(parts, fmt.Sprintf("%s %s has %d of %d done", c.short(x.p), x.r.Version, x.r.Done, x.r.Total))
	}
	return andList(parts)
}

func andList(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

// line is a day's releases in one sentence for the brief and the card.
func (c *cycleRun) line(t together) string {
	today := c.today.Format(day)
	if !t.shared() {
		x := t.rels[0]
		if x.r.Cutoff == today {
			return fmt.Sprintf("Cut-off for %s %s is today: %d of %d done.", x.p.Name, x.r.Version, x.r.Done, x.r.Total)
		}
		return fmt.Sprintf("%s %s ships %s with %d of %d done.", x.p.Name, x.r.Version, c.dayWord(x.r.ShipDate), x.r.Done, x.r.Total)
	}
	if t.cutoffToday(today) != nil {
		return fmt.Sprintf("Cut-off for %s's release %s is today: %s.", t.home.Name, c.dayWord(t.date), c.each(t))
	}
	return fmt.Sprintf("%s ships %s: %s.", t.home.Name, c.dayWord(t.date), c.each(t))
}

// productReleases are a project's products with cycles of their own, and
// their releases still to ship, for drawing on the project's page.
func (c *cycleRun) productReleases(p *project) []coming {
	if p.Parent != "" {
		return nil
	}
	var out []coming
	for _, x := range c.upcoming(0, false) {
		if x.p.Parent == p.ID {
			out = append(out, x)
		}
	}
	return out
}

// sharedSection is one day on a project's Releases page where more than
// the project's own cycle ships: each product's release as a row, and the
// project's own release, if it has one that day, in full under them.
func (c *cycleRun) sharedSection(p *project, t together, own *cycle.Release, folded bool) plugin.Block {
	s := plugin.Block{Type: plugin.BlockSection, Text: c.shortDate(t.date), Sub: c.versions(t), Label: "Ships " + c.dayWord(t.date), Collapsed: folded}
	if done, total := t.counts(); total > 0 {
		s.Value = fmt.Sprintf("%d/%d", done, total)
		s.Progress = float64(done) / float64(total)
	}
	items := []plugin.Item{}
	for _, x := range t.rels {
		if x.p.ID == p.ID {
			continue
		}
		it := plugin.Item{Title: c.short(x.p) + " " + x.r.Version, Colour: x.p.Colour, Sub: cycle.StateNames[x.r.State] + " · " + cycle.VerdictLine(x.r), Button: &plugin.Button{Label: "Open", Href: c.href(x.p)}}
		if x.r.Total > 0 {
			v := float64(x.r.Done) / float64(x.r.Total)
			it.Value, it.Progress = fmt.Sprintf("%d of %d", x.r.Done, x.r.Total), &v
		}
		items = append(items, it)
	}
	if len(items) > 0 {
		s.Blocks = append(s.Blocks, plugin.Block{Type: plugin.BlockList, Items: items})
	}
	if own != nil {
		s.Blocks = append(s.Blocks, c.section(p, *own, folded))
	}
	return s
}
