package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

func (r *run) render() {
	for _, s := range r.req.Slots {
		switch s {
		case plugin.SlotBrief:
			r.brief()
		case plugin.SlotWorkFields:
			r.fields()
		case plugin.SlotDeadlines:
			r.deadlines()
		case plugin.SlotWorkPanel:
			r.card(s, r.workPanel())
		case plugin.SlotSettingsPanel:
			r.card(s, r.settingsPanel())
		case "project_card:week":
			r.card(s, r.weekCard(s))
		case "project_card:coming":
			r.card(s, r.comingCard(s))
		case "project_card:apps":
			r.card(s, r.appsCard(s))
		}
	}
}

func (r *run) card(slot string, blocks []plugin.Block) {
	if len(blocks) == 0 {
		return
	}
	if r.res.Cards == nil {
		r.res.Cards = map[string][]plugin.Block{}
	}
	r.res.Cards[slot] = blocks
}

func raw(kv ...string) json.RawMessage {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	b, _ := json.Marshal(m)
	return b
}

func (r *run) workHref(w plugin.Work) string { return "/work/" + w.Key }

func (r *run) productsHref() string {
	return "/p/" + strings.ToLower(r.p.Key) + "/settings/products"
}

// brief is Today's line: the next post that isn't ready, else a slot about
// to go empty. It says nothing when all is well.
func (r *run) brief() {
	for _, p := range r.req.Projects {
		if !p.On {
			continue
		}
		x := &run{req: r.req, res: &plugin.Response{}, loc: r.loc, today: r.today}
		x.load(p.ID)
		if w := x.late(); w != nil {
			d, _ := x.due(*w)
			r.res.Brief = &plugin.Brief{Text: fmt.Sprintf("%s posts %s to %s and is still in %s.", w.Title, strings.ToLower(x.dayWord(d)), label(x.postsTo(*w)), x.stageName(*w)),
				Button: &plugin.Button{Label: "Open " + w.Key, Href: x.workHref(*w)}}
			return
		}
		if s := x.emptySoon(); s != nil {
			r.res.Brief = &plugin.Brief{Text: fmt.Sprintf("%s's %s slot %s has nothing planned yet.", p.Name, s.Channel.Name, strings.ToLower(x.dayWord(s.Date))),
				Button: &plugin.Button{Label: "Open " + p.Name, Href: "/p/" + strings.ToLower(p.Key)}}
			return
		}
	}
}

// fields are the choices of Format and Promotes.
func (r *run) fields() {
	if r.p == nil {
		return
	}
	promotes := []plugin.Option{{Value: "", Label: "Nothing in particular"}}
	for _, p := range r.req.Projects {
		if p.ID == r.p.ID || p.Parent != "" {
			continue
		}
		promotes = append(promotes, plugin.Option{Value: p.ID, Label: p.Name, Short: p.Name})
	}
	r.res.Fields = map[string]plugin.FieldState{
		"format": {Options: []plugin.Option{
			{Value: "", Label: "Not set"},
			{Value: "short", Label: "Short video", Sub: "Under a minute, for TikTok, Reels and Shorts", Short: "Short"},
			{Value: "long", Label: "Long video", Sub: "A few minutes or more, for YouTube", Short: "Long"},
			{Value: "post", Label: "Post", Sub: "Words or pictures, no video", Short: "Post"},
		}},
		"promotes": {Options: promotes},
		"post_to":  {Hidden: len(r.chans) == 0},
	}
}

// deadlines say each open piece has to be ready by the day it posts.
func (r *run) deadlines() {
	for _, p := range r.req.Projects {
		if !p.On {
			continue
		}
		x := &run{req: r.req, res: r.res, loc: r.loc, today: r.today}
		x.load(p.ID)
		for _, w := range x.upcoming() {
			if ready(w) {
				continue
			}
			if r.res.Deadlines == nil {
				r.res.Deadlines = map[string]plugin.Deadline{}
			}
			reason := "Posts " + x.dayWord(mustDay(w.Due))
			if l := label(x.postsTo(w)); l != "" {
				reason = "Posts to " + l + " " + x.dayWord(mustDay(w.Due))
			}
			r.res.Deadlines[w.ID] = plugin.Deadline{Date: w.Due, Reason: reason}
		}
	}
}

func mustDay(s string) time.Time {
	d, _ := time.Parse(day, s)
	return d
}

// workPanel is a piece's channels: where it posts, and each post's link
// once it's out.
func (r *run) workPanel() []plugin.Block {
	if r.p == nil {
		return nil
	}
	w := r.find(r.req.WorkID)
	if w == nil {
		return nil
	}
	if len(r.chans) == 0 {
		return []plugin.Block{
			{Type: plugin.BlockText, Tone: "muted", Text: "Add your channels as products of type Channel (a TikTok, Instagram or YouTube account) to choose where this posts."},
			{Type: plugin.BlockButtons, Buttons: []plugin.Button{{Label: "Add a channel", Href: r.productsHref()}}},
		}
	}
	pc := r.pieces[w.ID]
	links := map[string]string{}
	if pc != nil && pc.Links != nil {
		links = pc.Links
	}
	to := r.postsTo(*w)
	on := map[string]bool{}
	for _, c := range to {
		on[c.Key] = true
	}
	when := "No post day yet: set its due date to the day it goes out."
	if d, ok := r.due(*w); ok {
		when = "Posts " + d.Format("Mon 2 Jan")
		if n := d.Sub(r.today); n >= 0 && n < 7*24*time.Hour {
			when = "Posts " + strings.ToLower(r.dayWord(d))
		}
	}
	items := []plugin.Item{}
	for _, c := range r.chans {
		it := plugin.Item{Title: c.Name, Sub: "Not posting here", Icon: "sparkle"}
		switch {
		case links[c.Key] != "" && on[c.Key]:
			it.Sub, it.Done = "Posted", true
			it.Button = &plugin.Button{Label: "Open", URL: links[c.Key]}
		case on[c.Key]:
			it.Sub = "Posts here"
			it.Button = &plugin.Button{Label: "Don't post here", Action: "toggle", Args: raw("work", w.ID, "channel", c.Key)}
		default:
			it.Button = &plugin.Button{Label: "Post here", Action: "toggle", Args: raw("work", w.ID, "channel", c.Key)}
		}
		items = append(items, it)
	}
	blocks := []plugin.Block{{Type: plugin.BlockText, Text: when}, {Type: plugin.BlockList, Items: items}}
	if len(to) > 0 && w.StageKind != "cancelled" {
		fields := []plugin.Field{}
		for _, c := range to {
			fields = append(fields, plugin.Field{Key: "link:" + c.Key, Label: c.Name + " link", Type: "url", Placeholder: "https://…", Value: links[c.Key]})
		}
		text := "Once it's out, paste each post's link. With every link in, it moves to " + r.doneName() + "."
		blocks = append(blocks, plugin.Block{Type: plugin.BlockForm, Text: text, Fields: fields,
			Buttons: []plugin.Button{{Label: "Save links", Action: "links", Args: raw("work", w.ID)}}})
	}
	return blocks
}

// settingsPanel sets the days each channel posts on.
func (r *run) settingsPanel() []plugin.Block {
	if r.p == nil {
		return nil
	}
	if len(r.chans) == 0 {
		return []plugin.Block{
			{Type: plugin.BlockText, Text: "Each channel is a product of type Channel: a TikTok, Instagram or YouTube account. Add one, then set the days it posts on here."},
			{Type: plugin.BlockButtons, Buttons: []plugin.Button{{Label: "Add a channel", Href: r.productsHref()}}},
		}
	}
	fields := []plugin.Field{}
	for _, c := range r.chans {
		fields = append(fields, plugin.Field{Key: "days:" + c.Key, Label: c.Name, Placeholder: "Mon Wed Fri", Value: daysText(c.Days),
			Help: "The days it posts on. Leave empty for no set days."})
	}
	return []plugin.Block{{Type: plugin.BlockForm, Text: "Posting rhythm: the Overview and Today tell you when one of these days has nothing planned.",
		Fields: fields, Buttons: []plugin.Button{{Label: "Save rhythm", Action: "rhythm"}}}}
}

// weekCard is this week per channel: how many of its days have something
// planned.
func (r *run) weekCard(slot string) []plugin.Block {
	if r.p == nil {
		return nil
	}
	if len(r.chans) == 0 {
		return []plugin.Block{
			{Type: plugin.BlockText, Text: "Add your channels as products of type Channel to see each one's week here."},
			{Type: plugin.BlockButtons, Buttons: []plugin.Button{{Label: "Add a channel", Href: r.productsHref()}}},
		}
	}
	items := []plugin.Item{}
	planned, total := 0, 0
	for _, c := range r.chans {
		slots := r.week(c)
		it := plugin.Item{Title: c.Name, Icon: "sparkle"}
		if len(slots) == 0 {
			n := 0
			start := r.weekStart()
			for _, w := range r.work {
				d, ok := r.due(w)
				if !ok || d.Before(start) || !d.Before(start.AddDate(0, 0, 7)) || w.StageKind == "cancelled" {
					continue
				}
				for _, x := range r.postsTo(w) {
					if x.Key == c.Key {
						n++
					}
				}
			}
			it.Sub, it.Value = "No set days", plural(n, "post")+" this week"
			items = append(items, it)
			continue
		}
		done, gap := 0, ""
		for _, s := range slots {
			if len(s.Pieces) > 0 {
				done++
			} else if gap == "" && !s.Date.Before(r.today) {
				gap = s.Date.Weekday().String() + " has nothing planned"
			}
		}
		planned, total = planned+done, total+len(slots)
		p := float64(done) / float64(len(slots))
		it.Progress = &p
		it.Value = fmt.Sprintf("%d of %d", done, len(slots))
		it.Sub = daysText(c.Days)
		if gap != "" {
			it.Sub += " · " + gap
		}
		items = append(items, it)
	}
	if total > 0 {
		r.res.Say(slot, fmt.Sprintf("%d of %d posts this week are planned.", planned, total), fmt.Sprintf("%d of %d", planned, total))
	}
	if s := r.emptySoon(); s != nil {
		r.res.Signal(slot, plugin.LevelLoud, s.Channel.Name+" "+strings.ToLower(r.dayWord(s.Date))+" has nothing planned")
	}
	return []plugin.Block{{Type: plugin.BlockList, Items: items}}
}

// comingCard is what posts next, with where each piece is.
func (r *run) comingCard(slot string) []plugin.Block {
	if r.p == nil {
		return nil
	}
	up := r.upcoming()
	if len(up) == 0 {
		return []plugin.Block{{Type: plugin.BlockText, Tone: "muted", Text: "Nothing has a post day yet. Give a piece a due date for the day it goes out."}}
	}
	items := []plugin.Item{}
	for i, w := range up {
		if i == 6 {
			break
		}
		d, _ := r.due(w)
		sub := r.stageName(w)
		if l := label(r.postsTo(w)); l != "" {
			sub += " · " + l
		}
		items = append(items, plugin.Item{Title: w.Title, Sub: sub, Value: r.dayWord(d), Done: ready(w), Button: &plugin.Button{Label: "Open", Href: r.workHref(w)}})
	}
	first := up[0]
	d, _ := r.due(first)
	r.res.Say(slot, fmt.Sprintf("Next up: %s, %s.", first.Title, strings.ToLower(r.dayWord(d))), first.Title+" "+r.dayWord(d))
	if w := r.late(); w != nil {
		wd, _ := r.due(*w)
		r.res.Signal(slot, plugin.LevelLoud, w.Title+" posts "+strings.ToLower(r.dayWord(wd))+", still in "+r.stageName(*w))
	}
	return []plugin.Block{{Type: plugin.BlockList, Items: items}}
}

// appsCard is each project the content promotes: what's coming and what
// went out in the last 30 days, warning when one has had nothing for three
// weeks.
func (r *run) appsCard(slot string) []plugin.Block {
	if r.p == nil {
		return nil
	}
	type app struct {
		id, name, colour string
		coming, posted   int
		last             time.Time
	}
	apps := map[string]*app{}
	for _, w := range r.work {
		id := w.Fields[r.field("promotes")]
		if id == "" || w.StageKind == "cancelled" {
			continue
		}
		a := apps[id]
		if a == nil {
			a = &app{id: id, name: id}
			for _, p := range r.req.Projects {
				if p.ID == id {
					a.name, a.colour = p.Name, p.Colour
				}
			}
			apps[id] = a
		}
		switch {
		case w.StageKind == "done":
			at := w.DoneAt
			if d, ok := r.due(w); ok {
				at = d
			}
			if at.After(a.last) {
				a.last = at
			}
			if r.today.Sub(at) <= 30*24*time.Hour {
				a.posted++
			}
		case open(w):
			a.coming++
		}
	}
	if len(apps) == 0 {
		return []plugin.Block{{Type: plugin.BlockText, Tone: "muted", Text: "Set Promotes on a piece to see each app here."}}
	}
	list := make([]*app, 0, len(apps))
	for _, a := range apps {
		list = append(list, a)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].name < list[j].name })
	items := []plugin.Item{}
	quiet := ""
	for _, a := range list {
		it := plugin.Item{Title: a.name, Colour: a.colour, Value: fmt.Sprintf("%d coming · %d posted", a.coming, a.posted)}
		if a.coming == 0 && (a.last.IsZero() || r.today.Sub(a.last) > 21*24*time.Hour) {
			it.Sub = "Nothing in 3 weeks"
			if quiet == "" {
				quiet = a.name
			}
		}
		items = append(items, it)
	}
	if quiet != "" {
		r.res.Say(slot, quiet+" has had no content in 3 weeks.", quiet+" quiet")
	}
	return []plugin.Block{{Type: plugin.BlockList, Items: items}}
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
