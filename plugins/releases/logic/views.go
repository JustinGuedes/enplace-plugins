package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/plugins/releases/cycle"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

func (c *cycleRun) href(p plugin.Project) string {
	h := "/p/" + strings.ToLower(p.Key) + "/x/" + c.req.Plugin.ID + "/releases"
	if p.Product != "" {
		h += "?product=" + p.Product
	}
	return h
}

func (c *cycleRun) render() {
	today := c.today.Format(day)
	// Releases of one project shipping on the same day show as one.
	all := c.group(c.upcoming(1, true))
	for _, slot := range c.req.Slots {
		switch slot {
		case plugin.SlotBrief:
			for _, t := range all {
				d, _ := time.Parse(day, t.date)
				if d.Sub(c.today) > 7*24*time.Hour {
					continue
				}
				label := "Open " + t.rels[0].r.Version
				if t.shared() {
					label = "Open releases"
				}
				c.res.Brief = &plugin.Brief{Text: c.line(t), Button: &plugin.Button{Label: label, Href: c.link(t)}}
				break
			}
		case "card:soon":
			items := []plugin.Item{}
			for _, t := range all {
				done, total := t.counts()
				sub := "Ships " + c.dayWord(t.date) + " · " + cycle.VerdictLine(t.rels[0].r)
				colour := t.rels[0].p.Colour
				if t.shared() {
					sub = "Ships " + c.dayWord(t.date) + " · " + c.versions(t)
					colour = t.home.Colour
				}
				items = append(items, plugin.Item{Title: t.name(), Sub: sub, Value: fmt.Sprintf("%d of %d", done, total), Colour: colour, Progress: t.progress(), Button: &plugin.Button{Label: "Open", Href: c.link(t)}})
			}
			if len(items) > 0 {
				c.card(slot, []plugin.Block{{Type: plugin.BlockList, Items: items}})
			}
			// The nearest release with work in it sets how loud the card is:
			// lifted in its last two days, resting when it's over a week off.
			for _, t := range all {
				d, _ := time.Parse(day, t.date)
				name := t.name()
				short := t.rels[0].r.Version + " " + c.dayWord(t.date)
				if t.shared() {
					short = t.home.Name + " " + c.dayWord(t.date)
				}
				c.res.Say(slot, c.line(t), short)
				switch away := d.Sub(c.today); {
				case t.cutoffToday(today) != nil:
					c.res.Signal(slot, plugin.LevelLoud, "Cut-off for "+name+" is today")
				case away <= 2*24*time.Hour:
					c.res.Signal(slot, plugin.LevelLoud, name+" ships "+c.dayWord(t.date))
				case away > 7*24*time.Hour:
					c.res.Signal(slot, plugin.LevelQuiet, "")
				}
				break
			}
		case "project_card:release":
			if p := c.load(c.req.ProjectID); p != nil {
				c.card(slot, c.projectCard(p))
			}
		case plugin.SlotWorkFields:
			if p := c.load(c.req.ProjectID); p != nil {
				c.res.Fields = c.fields(p)
			}
		case plugin.SlotDeadlines:
			c.deadlines()
		}
	}
}

// deadlines says that open work in a release has to be done by its
// cut-off (or its ship day when there's no cut-off), so arranging the day
// puts it before work that can wait.
func (c *cycleRun) deadlines() {
	out := map[string]plugin.Deadline{}
	for _, x := range c.req.Projects {
		p := c.load(x.ID)
		if p == nil {
			continue
		}
		by := map[string]cycle.Release{}
		for _, r := range p.rels {
			if r.Open() {
				by[r.ID] = r
			}
		}
		for _, it := range p.items {
			r, ok := by[it.Release]
			if !ok || it.Done {
				continue
			}
			date, what := r.Cutoff, "cut-off"
			if date == "" {
				date, what = r.ShipDate, "ships"
			}
			if date == "" {
				continue
			}
			out[it.ID] = plugin.Deadline{Date: date, Reason: fmt.Sprintf("%s %s %s %s", p.Name, r.Version, what, c.shortDate(date)), Must: it.MustShip || it.Priority >= 4}
		}
	}
	c.res.Deadlines = out
}

func (c *cycleRun) card(slot string, blocks []plugin.Block) {
	if c.res.Cards == nil {
		c.res.Cards = map[string][]plugin.Block{}
	}
	c.res.Cards[slot] = blocks
}

// noun is what work that ships is called here.
func (c *cycleRun) noun(pid string) string {
	switch c.kind(pid) {
	case "app":
		return "App work"
	case "site":
		return "Site work"
	}
	return "Ships"
}

// fields are the Release field's choices in a project.
func (c *cycleRun) fields(p *project) map[string]plugin.FieldState {
	opts := []plugin.Option{{Value: general, Label: "General", Sub: "Project work that isn't part of a release"}}
	if p.continuous {
		label := "Ships when done"
		if c.kinds(p.ID)["site"] {
			label = "Site work, ships when done"
		}
		opts = append(opts, plugin.Option{Value: deploy, Label: label, Short: "When done", Sub: "Goes out as soon as it's done"})
	}
	if !p.onlyContinuous() {
		opts = append(opts, plugin.Option{Value: unplanned, Label: c.noun(p.ID) + ", no release yet", Sub: "Ships, but isn't planned into a release"})
	}
	today := c.today.Format(day)
	var past []plugin.Option
	for _, r := range p.rels {
		o := plugin.Option{Value: r.ID, Label: r.Version, Short: r.Version, Sub: cycle.StateNames[r.State] + " · ships " + c.shortDate(r.ShipDate), Day: r.ShipDate}
		if !r.Open() || r.ShipDate < today {
			o.Closed = !r.Open()
			if !r.Open() {
				o.Sub = cycle.StateNames[r.State] + " · " + c.shortDate(r.ShipDate)
				if r.Deploy {
					o.Sub = "Deployed " + c.shortDate(r.ShipDate)
				}
				past = append(past, o)
				continue
			}
		}
		opts = append(opts, o)
	}
	if len(past) > 8 {
		past = past[len(past)-8:]
	}
	return map[string]plugin.FieldState{"release": {Options: append(opts, past...)}, "must_ship": {}}
}

func (c *cycleRun) projectCard(p *project) []plugin.Block {
	if p.onlyContinuous() {
		b := plugin.Block{Type: plugin.BlockStat, Label: "Ships when done", Value: "Nothing out yet", Tone: "muted"}
		if r := p.lastDeploy(); r != nil {
			b.Value, b.Tone, b.Label = r.Version, "", "Ships when done · last deploy"
			b.Sub = "Out " + c.dayWord(r.ShipDate) + " with " + fixes(r.Total)
		}
		if n := len(c.waiting(p)); n > 0 {
			b.Sub = strings.TrimPrefix(b.Sub+fmt.Sprintf(" · %d waiting", n), " · ")
		}
		return []plugin.Block{b, {Type: plugin.BlockButtons, Buttons: []plugin.Button{{Label: "Deploys", Href: c.href(p.Project)}}}}
	}
	r := p.next(c.today.Format(day))
	if r == nil {
		text := "No release planned. Releases plan themselves once the cadence is on."
		return []plugin.Block{{Type: plugin.BlockText, Text: text, Tone: "muted"}, {Type: plugin.BlockButtons, Buttons: []plugin.Button{{Label: "Releases", Href: c.href(p.Project)}}}}
	}
	b := plugin.Block{Type: plugin.BlockRing, Label: "Ships " + c.dayWord(r.ShipDate), Value: r.Version, Sub: cycle.VerdictLine(*r)}
	if r.Total > 0 {
		b.Progress = float64(r.Done) / float64(r.Total)
		b.Sub = fmt.Sprintf("%d of %d done · %s", r.Done, r.Total, cycle.VerdictLine(*r))
	}
	if p.live != "" {
		b.Label = p.live + " live · " + r.Version + " ships " + c.dayWord(r.ShipDate)
	}
	blocks := []plugin.Block{b}
	if line := c.codeLine(p, r); line != nil {
		blocks = append(blocks, *line)
	}
	return append(blocks, plugin.Block{Type: plugin.BlockButtons, Buttons: []plugin.Button{{Label: "Open " + r.Version, Href: c.href(p.Project)}}})
}

func args(kv ...string) json.RawMessage {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	b, _ := json.Marshal(m)
	return b
}

// section draws one release with its work and What's new. Its version,
// dates and state are pills in its title row that edit in place, and its
// actions sit beside them with keyboard shortcuts.
func (c *cycleRun) section(p *project, r cycle.Release, folded bool) plugin.Block {
	// Sub says the dates for apps that don't draw the facts as pills yet.
	sub := "Ships " + c.shortDate(r.ShipDate)
	if r.Cutoff != "" && r.Open() {
		sub += " · cut-off " + c.shortDate(r.Cutoff)
	}
	if r.StoreState != "" {
		sub += " · " + r.StoreState
	}
	s := plugin.Block{Type: plugin.BlockSection, Text: r.Version, Sub: sub, Label: cycle.StateNames[r.State], Collapsed: folded}
	if r.StoreState != "" {
		s.Blocks = append(s.Blocks, plugin.Block{Type: plugin.BlockText, Text: "In the store: " + r.StoreState, Tone: "muted"})
	}
	if r.Total > 0 {
		s.Value = fmt.Sprintf("%d/%d", r.Done, r.Total)
		s.Progress = float64(r.Done) / float64(r.Total)
	}
	names := []string{}
	for _, st := range cycle.States {
		names = append(names, cycle.StateNames[st])
	}
	ship := plugin.Field{Key: "ship_date", Label: "Ships", Type: "date", Icon: "launch", Value: r.ShipDate, Required: true, RangeFrom: r.Cutoff, Min: c.today.Format(day)}
	if t, err := time.Parse(day, r.ShipDate); err == nil {
		today := c.today.Format(day)
		if d := t.AddDate(0, 0, -7).Format(day); d >= today {
			ship.Shortcuts = append(ship.Shortcuts, plugin.Option{Label: "A week earlier", Value: d})
		}
		ship.Shortcuts = append(ship.Shortcuts, plugin.Option{Label: "A week later", Value: t.AddDate(0, 0, 7).Format(day)})
		if w := p.set.EveryWeeks; w > 1 {
			ship.Shortcuts = append(ship.Shortcuts, plugin.Option{Label: fmt.Sprintf("%d weeks later", w), Value: t.AddDate(0, 0, 7*w).Format(day)})
		}
	}
	follow := &plugin.Follow{Key: "follow", Label: "Move the releases after it too", Sub: "Keeps the same gaps between them", On: true}
	for _, n := range p.rels {
		if n.ID == r.ID || !n.Open() || n.Deploy {
			continue
		}
		ship.Marks = append(ship.Marks, n.ShipDate)
		if n.State == cycle.Planned && n.ShipDate > r.ShipDate {
			follow.Items = append(follow.Items, plugin.Option{Label: n.Version, Value: n.ShipDate})
		}
	}
	if len(follow.Items) > 0 {
		ship.Follow = follow
	}
	s.Fields = []plugin.Field{
		{Key: "version", Label: "Version", Icon: "tag", Value: r.Version, Required: true, Help: "Like 1.4. Releases after it keep their own numbers."},
		ship,
		{Key: "cutoff", Label: "Cut-off", Type: "date", Icon: "cut", Value: r.Cutoff, Placeholder: "No cut-off", Max: r.ShipDate},
		{Key: "state", Label: "State", Type: "select", Options: names, Value: cycle.StateNames[r.State]},
	}
	s.Save = &plugin.Button{Label: "Save", Action: "save", Args: args("id", r.ID)}
	if r.Open() {
		if t := cycle.VerdictText(r); t != "" {
			s.Blocks = append(s.Blocks, plugin.Block{Type: plugin.BlockText, Text: t, Tone: "muted"})
		}
	}
	s.Menu = []plugin.Button{
		{Label: "Move dates", Icon: "move", Key: "d", Open: "ship_date"},
		{Label: "Change the cut-off", Icon: "cut", Key: "c", Open: "cutoff"},
		{Label: "Change the version", Icon: "tag", Key: "v", Open: "version"},
	}
	if r.CanSkip() {
		skip := plugin.Button{Label: "Skip " + r.Version, Icon: "skip", Key: "s", Action: "skip", Args: args("id", r.ID), Confirm: c.skipQuestion(p, r)}
		s.Buttons = []plugin.Button{skip}
		s.Menu = append(s.Menu, skip)
	}
	s.Menu = append(s.Menu, plugin.Button{Label: "Draft What's new", Icon: "doc", Key: "n", Action: "draft", Args: args("id", r.ID)})
	ids := []string{}
	for _, w := range p.work {
		if w.Fields[c.field("release")] == r.ID {
			ids = append(ids, w.ID)
		}
	}
	s.Blocks = append(s.Blocks, plugin.Block{Type: plugin.BlockWork, Work: ids, Text: "Nothing in " + r.Version + " yet. Set a work item's Release to add it."})
	// What's new folds away under the work.
	status := "Not written yet"
	switch {
	case r.WhatsNewApproved:
		status = "Approved"
	case r.WhatsNew != "":
		status = "Written, not approved yet"
	}
	btns := []plugin.Button{{Label: "Draft from what's done", Action: "draft", Args: args("id", r.ID)}}
	if r.WhatsNew != "" && !r.WhatsNewApproved {
		btns = append(btns, plugin.Button{Label: "Approve", Action: "approve", Args: args("id", r.ID)})
	}
	s.Blocks = append(s.Blocks, plugin.Block{Type: plugin.BlockSection, Text: "What's new", Sub: status, Collapsed: true, Blocks: []plugin.Block{
		{Type: plugin.BlockForm,
			Fields:  []plugin.Field{{Key: "whats_new", Label: "What's new", Type: "textarea", Value: r.WhatsNew, Placeholder: "What people will read about this version"}},
			Buttons: []plugin.Button{{Label: "Save", Action: "save", Args: args("id", r.ID)}}},
		{Type: plugin.BlockButtons, Buttons: btns},
	}})
	return s
}

// skipQuestion asks before a release is skipped, saying where its
// unfinished work will go.
func (c *cycleRun) skipQuestion(p *project, r cycle.Release) string {
	open := 0
	for _, it := range p.items {
		if it.Release == r.ID && !it.Done {
			open++
		}
	}
	if open == 0 {
		return "Skip " + r.Version + "?"
	}
	after := false
	for _, n := range p.rels {
		if n.ID == r.ID {
			after = true
			continue
		}
		if after && n.State == cycle.Planned && n.ShipDate >= r.ShipDate {
			return fmt.Sprintf("Skip %s? %s move to %s.", r.Version, plural(open, "unfinished item", "unfinished items"), n.Version)
		}
	}
	return fmt.Sprintf("Skip %s? %s will wait for a release.", r.Version, plural(open, "unfinished item", "unfinished items"))
}

func (c *cycleRun) projectPage(p *project) *plugin.Page {
	pg := &plugin.Page{Title: "Releases", Blocks: []plugin.Block{}}
	head := plugin.Block{Type: plugin.BlockText}
	switch {
	case p.onlyContinuous():
		head.Text = "Work here ships as soon as it's done. Each day's finished work is one deploy."
	case !p.set.Enabled:
		head.Text = "The cadence is off, so releases are added by hand."
	case p.set.EveryWeeks == 1:
		head.Text = "A release every " + weekdays[p.set.Weekday] + "."
	default:
		head.Text = fmt.Sprintf("A release every %d weeks, on a %s.", p.set.EveryWeeks, weekdays[p.set.Weekday])
	}
	if p.follows && p.set.Enabled && !p.onlyContinuous() {
		head.Text += " It follows the project's cadence."
	}
	if p.live != "" {
		head.Text = p.live + " is live. " + head.Text
	}
	if p.continuous && !p.onlyContinuous() {
		head.Text += " Site work ships as soon as it's done."
	}
	if figs := c.figures(p); figs != nil {
		pg.Blocks = append(pg.Blocks, *figs)
	}
	pg.Title = c.headline(p)
	pg.Blocks = append(pg.Blocks, head)
	if line := c.codeLine(p, p.next(c.today.Format(day))); line != nil {
		pg.Blocks = append(pg.Blocks, *line)
	}
	if p.continuous {
		w := c.waiting(p)
		s := plugin.Block{Type: plugin.BlockSection, Text: "Ships when done", Sub: "Goes out as soon as it's done", Blocks: []plugin.Block{
			{Type: plugin.BlockWork, Work: w, Text: "Nothing waiting. Set a work item's Release to Ships when done to add it."}}}
		if len(w) > 0 {
			s.Value = fmt.Sprintf("%d", len(w))
		}
		pg.Blocks = append(pg.Blocks, s)
	}
	var past, deployed []cycle.Release
	first := true
	// The first release is the next one; the rest come after it.
	rels := 0
	release := func(b plugin.Block) {
		switch rels {
		case 0:
			pg.Blocks = append(pg.Blocks, plugin.Block{Type: plugin.BlockHeading, Text: "Next release"})
		case 1:
			pg.Blocks = append(pg.Blocks, plugin.Block{Type: plugin.BlockHeading, Text: "After that"})
		}
		rels++
		pg.Blocks = append(pg.Blocks, b)
	}
	// Products with cycles of their own ship beside the project's: a day
	// they share shows as one section.
	theirs := c.productReleases(p)
	byDay := map[string][]coming{}
	for _, x := range theirs {
		byDay[x.r.ShipDate] = append(byDay[x.r.ShipDate], x)
	}
	// One shared day for each of their dates, in order with the project's.
	shared := func(upTo string, own *cycle.Release) {
		for len(theirs) > 0 && (theirs[0].r.ShipDate < upTo || (own != nil && theirs[0].r.ShipDate == upTo)) {
			date := theirs[0].r.ShipDate
			rels := byDay[date]
			theirs = theirs[len(rels):]
			var mine *cycle.Release
			if own != nil && own.ShipDate == date {
				mine = own
				rels = append([]coming{{p.Project, *own}}, rels...)
			}
			t := c.group(rels)[0]
			release(c.sharedSection(p, t, mine, !first))
			first = false
			if mine != nil {
				own = nil
			}
		}
		if own != nil {
			release(c.section(p, *own, !first))
			first = false
		}
	}
	for _, r := range p.rels {
		if r.Deploy {
			deployed = append(deployed, r)
			continue
		}
		if !r.Open() {
			past = append(past, r)
			continue
		}
		shared(r.ShipDate, &r)
	}
	shared("9999-12-31", nil)
	if len(deployed) > 0 {
		pg.Blocks = append(pg.Blocks, plugin.Block{Type: plugin.BlockHeading, Text: "Deploys"})
		for i := len(deployed) - 1; i >= 0 && i >= len(deployed)-8; i-- {
			pg.Blocks = append(pg.Blocks, c.deploySection(p, deployed[i], i != len(deployed)-1))
		}
	}
	if p.onlyContinuous() {
		first = false // no "Nothing is planned" or adding releases by hand
	}
	if first {
		pg.Blocks = append(pg.Blocks, plugin.Block{Type: plugin.BlockText, Text: "Nothing is planned yet.", Tone: "muted"})
	}
	next := cycle.Bump(p.live, p.set.Scheme, p.set.Bump, c.today.Format(day))
	if n := len(p.rels); n > 0 {
		next = cycle.Bump(p.rels[n-1].Version, p.set.Scheme, p.set.Bump, c.today.Format(day))
	}
	if !p.onlyContinuous() {
		pg.Blocks = append(pg.Blocks, c.addRelease(next))
	}
	if len(past) > 0 {
		items := []plugin.Item{}
		for i := len(past) - 1; i >= 0 && len(items) < 12; i-- {
			r := past[i]
			it := plugin.Item{Title: r.Version, Sub: cycle.StateNames[r.State] + " · " + c.shortDate(r.ShipDate), Done: r.State == cycle.Live}
			if r.State == cycle.Skipped && r.ShipDate >= c.today.Format(day) {
				it.Button = &plugin.Button{Label: "Bring back", Action: "unskip", Args: args("id", r.ID)}
			}
			if r.Total > 0 {
				it.Value = fmt.Sprintf("%d shipped", r.Done)
			}
			items = append(items, it)
		}
		pg.Blocks = append(pg.Blocks, plugin.Block{Type: plugin.BlockHeading, Text: "Shipped"}, plugin.Block{Type: plugin.BlockList, Items: items})
	}
	return pg
}

// addRelease is the folded form for adding a release by hand.
func (c *cycleRun) addRelease(next string) plugin.Block {
	return plugin.Block{Type: plugin.BlockSection, Text: "Add a release", Collapsed: true, Blocks: []plugin.Block{{Type: plugin.BlockForm,
		Fields: []plugin.Field{
			{Key: "version", Label: "Version", Placeholder: next, Value: next, Required: true},
			{Key: "ship_date", Label: "Ships", Type: "date", Required: true},
		},
		Buttons: []plugin.Button{{Label: "Add release", Action: "add"}}}}}
}

// deploySection is one day's deploy with the work that went out in it.
func (c *cycleRun) deploySection(p *project, r cycle.Release, folded bool) plugin.Block {
	ids := []string{}
	for _, w := range p.work {
		if w.Fields[c.field("release")] == r.ID {
			ids = append(ids, w.ID)
		}
	}
	return plugin.Block{Type: plugin.BlockSection, Text: r.Version, Sub: "Deployed " + c.shortDate(r.ShipDate) + " · " + fixes(len(ids)), Label: "Live", Collapsed: folded,
		Blocks: []plugin.Block{{Type: plugin.BlockWork, Work: ids, Text: "Nothing went out in " + r.Version + "."}}}
}

// allPage is every project's coming releases, soonest first.
func (c *cycleRun) allPage() *plugin.Page {
	pg := &plugin.Page{Title: "Releases", Blocks: []plugin.Block{{Type: plugin.BlockText, Text: "What ships next, across your projects."}}}
	items := []plugin.Item{}
	// Projects whose work ships as it's done say how that's going.
	for _, x := range c.req.Projects {
		if p := c.load(x.ID); p != nil && p.continuous {
			items = append(items, plugin.Item{Title: x.Name, Colour: x.Colour, Sub: c.deployLine(p), Button: &plugin.Button{Label: "Open", Href: c.href(x)}})
		}
	}
	// Each one's next two releases, one row a day for each project.
	for _, t := range c.group(c.upcoming(2, false)) {
		it := plugin.Item{Title: t.name(), Colour: t.rels[0].p.Colour, Sub: "Ships " + c.shortDate(t.date) + " · " + cycle.VerdictLine(t.rels[0].r), Button: &plugin.Button{Label: "Open", Href: c.link(t)}}
		if t.shared() {
			it.Colour, it.Sub = t.home.Colour, "Ships "+c.shortDate(t.date)+" · "+c.versions(t)
		}
		if done, total := t.counts(); total > 0 {
			it.Value, it.Progress = fmt.Sprintf("%d of %d", done, total), t.progress()
		}
		items = append(items, it)
	}
	if len(items) == 0 {
		pg.Blocks = append(pg.Blocks, plugin.Block{Type: plugin.BlockText, Text: "No releases yet. Switch Release cycle on for a project, or for one of its products.", Tone: "muted"})
		return pg
	}
	pg.Blocks = append(pg.Blocks, plugin.Block{Type: plugin.BlockList, Items: items})
	return pg
}

// headline answers the Releases page in a few words: when the next one ships.
func (c *cycleRun) headline(p *project) string {
	r := p.next(c.today.Format(day))
	switch {
	case p.onlyContinuous():
		return "Ships when done"
	case r == nil:
		return "Nothing planned"
	}
	when := c.dayWord(r.ShipDate)
	if t, err := time.Parse(day, r.ShipDate); err == nil {
		if days := int(t.Sub(c.today).Hours() / 24); days > 1 && days < 7 {
			when = t.Format("Monday")
		} else if days >= 7 {
			when = "on " + t.Format("2 Jan")
		}
	}
	return r.Version + " ships " + when
}

// figures sum up the next release under the headline: how much is done,
// the cut-off and, with Versions, the next build.
func (c *cycleRun) figures(p *project) *plugin.Block {
	r := p.next(c.today.Format(day))
	if r == nil || p.onlyContinuous() {
		return nil
	}
	b := plugin.Block{Type: plugin.BlockStats, Tone: "figs"}
	b.Items = append(b.Items, plugin.Item{Title: "done", Value: fmt.Sprintf("%d of %d", r.Done, r.Total), Done: r.Total > 0})
	if r.Cutoff != "" && r.Cutoff >= c.today.Format(day) {
		w := c.dayWord(r.Cutoff)
		b.Items = append(b.Items, plugin.Item{Title: "cut-off", Value: strings.ToUpper(w[:1]) + w[1:]})
	}
	if v := c.versionsPlugin(p.ID); v != nil {
		if n, err := strconv.Atoi(v.Build); err == nil {
			b.Items = append(b.Items, plugin.Item{Title: "next build", Value: strconv.Itoa(n + 1)})
		}
	}
	return &b
}
