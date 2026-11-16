package main

import (
	"fmt"
	"strings"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

func ptr(f float64) *float64 { return &f }

func (g *game) toNext() string {
	return fmt.Sprintf("%d XP from level %d", g.lv.Next-g.total, g.lv.N+1)
}

// brief is the sentence on Today.
func (g *game) brief(req *plugin.Request) *plugin.Brief {
	if len(g.skills) == 0 {
		return &plugin.Brief{Text: "Switch XP on in a project to start levelling up.", Button: &plugin.Button{Label: "Character", Href: "/x/xp/character"}}
	}
	lead := fmt.Sprintf("You're level %d, %s.", g.lv.N, g.toNext())
	var streak string
	switch {
	case g.doneToday && g.streak > 1:
		streak = fmt.Sprintf("Your %d-day streak is safe for today.", g.streak)
	case g.doneToday:
		streak = "You've started a streak today."
	case g.streak > 0:
		if w := g.nextUp(); w != nil {
			streak = fmt.Sprintf("Finish %s to keep your %d-day streak.", w.Title, g.streak)
		} else {
			streak = fmt.Sprintf("Finish anything to keep your %d-day streak.", g.streak)
		}
	default:
		if w := g.nextUp(); w != nil {
			streak = fmt.Sprintf("Finish %s to start a streak.", w.Title)
		} else {
			streak = "Finish one thing today to start a streak."
		}
	}
	if g.notice != nil {
		return &plugin.Brief{Text: g.notice.Text + " " + lead + " " + streak, Button: &plugin.Button{Label: "Got it", Action: "dismiss"}}
	}
	return &plugin.Brief{Text: lead + " " + streak, Button: &plugin.Button{Label: "Character", Href: "/x/xp/character"}}
}

func (g *game) chip() *plugin.Chip {
	sub := g.toNext()
	if g.streak > 0 {
		sub = fmt.Sprintf("%d-day streak · %s", g.streak, sub)
	}
	return &plugin.Chip{Title: fmt.Sprintf("Level %d %s", g.lv.N, title(g.lv.N)), Subtitle: sub, Progress: ptr(g.lv.progress(g.total)), Href: "/x/xp/character"}
}

func (g *game) streakDays() plugin.Block {
	b := plugin.Block{Type: plugin.BlockDays}
	for i := 6; i >= 0; i-- {
		d := g.addDays(g.today, -i)
		b.Items = append(b.Items, plugin.Item{Title: g.weekday(d).String()[:1], Sub: d, Done: g.days[d] > 0})
	}
	return b
}

func (g *game) streakCard() []plugin.Block {
	val := fmt.Sprintf("%d %s", g.streak, plural(g.streak, "day"))
	note := "Done for today."
	if !g.doneToday {
		note = "Finish one thing today to keep it going."
		if g.streak == 0 {
			note = "Finish one thing today to start one."
		}
	}
	return []plugin.Block{
		{Type: plugin.BlockStat, Value: val, Sub: fmt.Sprintf("Best %d", g.bestStreak)},
		g.streakDays(),
		{Type: plugin.BlockText, Text: note, Tone: "muted"},
	}
}

func (g *game) questItems() []plugin.Item {
	var items []plugin.Item
	for _, q := range g.quests {
		items = append(items, plugin.Item{Title: q.Text, Sub: fmt.Sprintf("+%d XP", q.Reward), Value: fmt.Sprintf("%d/%d", q.Have, q.Need), Done: q.Done, Progress: ptr(float64(q.Have) / float64(q.Need)), Icon: "target"})
	}
	return items
}

func (g *game) questsCard() []plugin.Block {
	left := 0
	bonus := 0
	for _, q := range g.quests {
		if !q.Done {
			left++
			bonus += q.Reward
		}
	}
	end := g.weekday(g.addDays(g.weekOf(g.today), 7)).String()
	text := fmt.Sprintf("%d left, worth %d bonus XP. New quests on %s.", left, bonus, end)
	if left == 0 {
		text = fmt.Sprintf("All cleared this week. New quests on %s.", end)
	}
	return []plugin.Block{
		{Type: plugin.BlockList, Items: g.questItems()},
		{Type: plugin.BlockText, Text: text, Tone: "muted"},
	}
}

func (g *game) badgeItems(all bool) []plugin.Item {
	var items []plugin.Item
	list := g.earned()
	if all {
		list = g.badges
	}
	for _, b := range list {
		sub := b.Sub
		if b.Earned != "" {
			sub = "Earned " + b.Earned
		}
		items = append(items, plugin.Item{Title: b.Title, Sub: sub, Icon: b.Icon, Locked: b.Earned == ""})
	}
	return items
}

func (g *game) badgesCard() []plugin.Block {
	earned := g.earned()
	blocks := []plugin.Block{}
	if len(earned) == 0 {
		next := g.badges[0]
		blocks = append(blocks, plugin.Block{Type: plugin.BlockBadges, Items: []plugin.Item{{Title: next.Title, Sub: next.Sub, Icon: next.Icon, Locked: true}}})
	} else {
		items := g.badgeItems(false)
		if len(items) > 3 {
			items = items[:3]
		}
		blocks = append(blocks, plugin.Block{Type: plugin.BlockBadges, Items: items})
	}
	blocks = append(blocks,
		plugin.Block{Type: plugin.BlockText, Text: fmt.Sprintf("%d of %d earned.", len(earned), len(g.badges)), Tone: "muted"},
		plugin.Block{Type: plugin.BlockButtons, Buttons: []plugin.Button{{Label: "All badges", Href: "/x/xp/character"}}},
	)
	return blocks
}

func (g *game) skillLine(sk *skill) plugin.Item {
	return plugin.Item{
		Title: sk.Name, Icon: sk.Icon, Colour: sk.Colour,
		Sub:      fmt.Sprintf("%s · %d XP · %d to level %d", sk.Project, sk.XP, sk.lv.Next-sk.XP, sk.lv.N+1),
		Value:    fmt.Sprintf("Lv %d", sk.lv.N),
		Progress: ptr(sk.lv.progress(sk.XP)),
		Button:   &plugin.Button{Label: "Tree", Href: "/p/" + sk.Key + "/x/xp/skill"},
	}
}

// tree is a skill's unlocks as a path: each node holds the next.
func (g *game) tree(sk *skill) plugin.Item {
	var build func(i int) []plugin.Item
	build = func(i int) []plugin.Item {
		if i >= len(treeNodes) {
			return nil
		}
		n := treeNodes[i]
		got := sk.lv.N >= n.Level
		sub := fmt.Sprintf("Level %d · %s", n.Level, n.Perk)
		return []plugin.Item{{Title: n.Title, Sub: sub, Icon: map[bool]string{true: "check", false: "lock"}[got], Done: got, Locked: !got, Children: build(i + 1)}}
	}
	return plugin.Item{Title: sk.Name, Sub: fmt.Sprintf("Level %d", sk.lv.N), Icon: sk.Icon, Colour: sk.Colour, Done: true, Children: build(0)}
}

func (g *game) skillCard(projectID string) []plugin.Block {
	sk := g.byID[projectID]
	if sk == nil {
		return nil
	}
	return []plugin.Block{
		{Type: plugin.BlockRing, Label: sk.Name, Value: fmt.Sprintf("Lv %d", sk.lv.N), Progress: sk.lv.progress(sk.XP), Sub: fmt.Sprintf("%d XP · %d to level %d", sk.XP, sk.lv.Next-sk.XP, sk.lv.N+1), Colour: sk.Colour},
		{Type: plugin.BlockButtons, Buttons: []plugin.Button{{Label: "Skill tree", Href: "/p/" + sk.Key + "/x/xp/skill"}, {Label: "Character", Href: "/x/xp/character"}}},
	}
}

func (g *game) characterPage() *plugin.Page {
	p := &plugin.Page{Title: "Character"}
	if len(g.skills) == 0 {
		p.Blocks = []plugin.Block{
			{Type: plugin.BlockHeading, Text: "You haven't picked any skills yet"},
			{Type: plugin.BlockText, Text: "Switch XP on in a project (its settings, under Plugins) and that project becomes a skill. Finished work there earns XP."},
		}
		return p
	}
	names := []string{}
	for _, sk := range g.skills {
		names = append(names, sk.Name)
	}
	p.Blocks = []plugin.Block{
		{Type: plugin.BlockRing, Label: fmt.Sprintf("Level %d %s", g.lv.N, title(g.lv.N)), Value: fmt.Sprintf("Lv %d", g.lv.N), Progress: g.lv.progress(g.total), Sub: fmt.Sprintf("%d XP · %s", g.total, g.toNext()), Tone: "big"},
		{Type: plugin.BlockStats, Items: []plugin.Item{
			{Title: "Things finished", Value: fmt.Sprint(len(g.done)), Icon: "check"},
			{Title: "Streak", Value: fmt.Sprintf("%d", g.streak), Sub: fmt.Sprintf("best %d", g.bestStreak), Icon: "flame"},
			{Title: "Quest XP", Value: fmt.Sprint(g.questXP), Icon: "target"},
			{Title: "Badges", Value: fmt.Sprintf("%d/%d", len(g.earned()), len(g.badges)), Icon: "trophy"},
		}},
		{Type: plugin.BlockHeading, Text: "Skills", Sub: strings.Join(names, ", ")},
	}
	var skills []plugin.Item
	for _, sk := range g.skills {
		skills = append(skills, g.skillLine(sk))
	}
	p.Blocks = append(p.Blocks, plugin.Block{Type: plugin.BlockList, Items: skills})
	p.Blocks = append(p.Blocks, plugin.Block{Type: plugin.BlockHeading, Text: "This week's quests"}, plugin.Block{Type: plugin.BlockList, Items: g.questItems()})
	p.Blocks = append(p.Blocks, plugin.Block{Type: plugin.BlockHeading, Text: "Skill trees"})
	var trees []plugin.Item
	for _, sk := range g.skills {
		trees = append(trees, g.tree(sk))
	}
	p.Blocks = append(p.Blocks, plugin.Block{Type: plugin.BlockTree, Items: trees})
	p.Blocks = append(p.Blocks, plugin.Block{Type: plugin.BlockHeading, Text: "Badges"}, plugin.Block{Type: plugin.BlockBadges, Items: g.badgeItems(true)})
	return p
}

func (g *game) skillPage(projectID string) *plugin.Page {
	sk := g.byID[projectID]
	if sk == nil {
		return &plugin.Page{Blocks: []plugin.Block{{Type: plugin.BlockText, Text: "XP isn't on for this project."}}}
	}
	p := &plugin.Page{Title: sk.Name + " skill tree", Blocks: []plugin.Block{
		{Type: plugin.BlockRing, Label: sk.Name, Value: fmt.Sprintf("Lv %d", sk.lv.N), Progress: sk.lv.progress(sk.XP), Sub: fmt.Sprintf("%d XP · %d things finished · %d to level %d", sk.XP, sk.Done, sk.lv.Next-sk.XP, sk.lv.N+1), Colour: sk.Colour, Tone: "big"},
		{Type: plugin.BlockTree, Items: []plugin.Item{g.tree(sk)}},
	}}
	var recent []plugin.Item
	for i := len(g.done) - 1; i >= 0 && len(recent) < 8; i-- {
		w := g.done[i]
		if w.ProjectID == projectID {
			recent = append(recent, plugin.Item{Title: w.Title, Sub: w.Key + " · " + g.date(w.DoneAt), Value: fmt.Sprintf("+%d XP", xpFor(w)), Done: true})
		}
	}
	if len(recent) > 0 {
		p.Blocks = append(p.Blocks, plugin.Block{Type: plugin.BlockHeading, Text: "Recent XP"}, plugin.Block{Type: plugin.BlockList, Items: recent})
	}
	p.Blocks = append(p.Blocks, plugin.Block{Type: plugin.BlockButtons, Buttons: []plugin.Button{{Label: "Rename this skill", Href: "/p/" + sk.Key + "/settings/xp"}, {Label: "Character", Href: "/x/xp/character"}}})
	return p
}

// Signals: how much each Today card matters right now. Today rests the quiet
// ones and lifts the loud ones, so loud is kept for what won't wait.

// streakSignal: safe once something's done today, loud on an evening when
// the streak would otherwise end tonight.
func (g *game) streakSignal(res *plugin.Response, slot string, req *plugin.Request) {
	val := fmt.Sprintf("%d %s", g.streak, plural(g.streak, "day"))
	switch {
	case g.doneToday:
		res.Signal(slot, plugin.LevelQuiet, "")
		res.Say(slot, fmt.Sprintf("Your %s streak is safe for today.", val), val)
	case g.streak == 0:
		res.Signal(slot, plugin.LevelQuiet, "")
		res.Say(slot, "Finish one thing today to start a streak.", "No streak yet")
	default:
		res.Say(slot, fmt.Sprintf("Finish one thing today to keep your %s streak.", val), val)
		if req.Now.In(g.loc).Hour() >= 17 {
			res.Signal(slot, plugin.LevelLoud, fmt.Sprintf("Your %s streak ends tonight", val))
		}
	}
}

func (g *game) questsSignal(res *plugin.Response, slot string) {
	left := 0
	for _, q := range g.quests {
		if !q.Done {
			left++
		}
	}
	if left == 0 {
		res.Signal(slot, plugin.LevelQuiet, "")
		res.Say(slot, "", "All cleared")
		return
	}
	res.Say(slot, fmt.Sprintf("%d of this week's quests still to clear.", left), fmt.Sprintf("%d left", left))
}

// badgesSignal: loud on the day a badge is earned, quiet the rest of the time.
func (g *game) badgesSignal(res *plugin.Response, slot string) {
	earned := g.earned()
	res.Signal(slot, plugin.LevelQuiet, "")
	res.Say(slot, "", fmt.Sprintf("%d of %d", len(earned), len(g.badges)))
	if len(earned) > 0 && earned[0].Earned == g.today {
		res.Signal(slot, plugin.LevelLoud, "New badge: "+earned[0].Title)
	}
}
