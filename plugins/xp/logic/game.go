package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

// game is everything XP works out from finished work.
type game struct {
	now       time.Time
	loc       *time.Location
	today     string
	weekStart int
	diff      difficulty

	skills []*skill
	byID   map[string]*skill
	done   []plugin.Work // finished, oldest first, in XP projects
	open   []plugin.Work // not finished, in XP projects

	workXP, questXP, total int
	lv                     level

	days       map[string]int // finished items by local date
	streak     int
	doneToday  bool
	bestStreak int

	quests []quest // this week's
	badges []badge
	notice *notice
}

type skill struct {
	ID, Key, Project, Name, Icon, Colour string
	XP, Done                             int
	lv                                   level
}

type level struct{ N, Start, Next int }

// progress is how far through the level (0..1).
func (l level) progress(xp int) float64 {
	if l.Next <= l.Start {
		return 0
	}
	return float64(xp-l.Start) / float64(l.Next-l.Start)
}

// levelFor: level 2 at 100 XP, and each level after takes 50 more.
func levelFor(xp int) level {
	n, start, step := 1, 0, 100
	for xp >= start+step {
		start += step
		step += 50
		n++
	}
	return level{n, start, start + step}
}

// xpFor is what one finished item is worth.
func xpFor(w plugin.Work) int {
	est := w.Estimate
	if est > 240 {
		est = 240
	}
	return 10 + w.Priority*5 + est/15
}

type difficulty struct {
	Items, Days, Skills int
}

var difficulties = map[string]difficulty{
	"Gentle": {3, 2, 1},
	"Normal": {5, 3, 2},
	"Hard":   {10, 5, 3},
}

type notice struct {
	Text string `json:"text"`
	Date string `json:"date"`
}

func build(req *plugin.Request) *game {
	loc, err := time.LoadLocation(req.Timezone)
	if err != nil {
		loc = time.UTC
	}
	g := &game{now: req.Now, loc: loc, today: req.Date, weekStart: req.WeekStart, byID: map[string]*skill{}, days: map[string]int{}}
	if g.today == "" {
		g.today = g.date(g.now)
	}
	d, _ := req.Settings["difficulty"].(string)
	var ok bool
	if g.diff, ok = difficulties[d]; !ok {
		g.diff = difficulties["Normal"]
	}
	for _, p := range req.Projects {
		if !p.On {
			continue
		}
		sk := &skill{ID: p.ID, Key: strings.ToLower(p.Key), Project: p.Name, Name: p.Name, Icon: "star", Colour: p.Colour}
		if ps := req.ProjectSettings[p.ID]; ps != nil {
			if n, _ := ps["skill"].(string); strings.TrimSpace(n) != "" {
				sk.Name = strings.TrimSpace(n)
			}
			if ic, _ := ps["icon"].(string); ic != "" {
				sk.Icon = ic
			}
		}
		g.skills = append(g.skills, sk)
		g.byID[p.ID] = sk
	}
	for _, w := range req.Work {
		if g.byID[w.ProjectID] == nil {
			continue
		}
		if w.Done() && !w.DoneAt.IsZero() {
			g.done = append(g.done, w)
		} else if !w.Done() {
			g.open = append(g.open, w)
		}
	}
	sort.SliceStable(g.done, func(i, j int) bool { return g.done[i].DoneAt.Before(g.done[j].DoneAt) })
	for _, w := range g.done {
		x := xpFor(w)
		sk := g.byID[w.ProjectID]
		sk.XP += x
		sk.Done++
		g.workXP += x
		g.days[g.date(w.DoneAt)]++
	}
	for _, sk := range g.skills {
		sk.lv = levelFor(sk.XP)
	}
	sort.SliceStable(g.skills, func(i, j int) bool { return g.skills[i].XP > g.skills[j].XP })
	g.streaks()
	g.questsAndBonus()
	g.total = g.workXP + g.questXP
	g.lv = levelFor(g.total)
	g.badges = g.earnBadges()
	var n notice
	if req.State.Get("notice", &n) && n.Text != "" && n.Date >= g.addDays(g.today, -2) {
		g.notice = &n
	}
	return g
}

func (g *game) date(t time.Time) string { return t.In(g.loc).Format("2006-01-02") }

func (g *game) addDays(date string, n int) string {
	t, err := time.ParseInLocation("2006-01-02", date, g.loc)
	if err != nil {
		return date
	}
	return t.AddDate(0, 0, n).Format("2006-01-02")
}

func (g *game) weekday(date string) time.Weekday {
	t, _ := time.ParseInLocation("2006-01-02", date, g.loc)
	return t.Weekday()
}

// weekOf is the first day of the week a date is in.
func (g *game) weekOf(date string) string {
	back := (int(g.weekday(date)) - g.weekStart + 7) % 7
	return g.addDays(date, -back)
}

func (g *game) streaks() {
	g.doneToday = g.days[g.today] > 0
	d := g.today
	if !g.doneToday {
		d = g.addDays(d, -1)
	}
	for g.days[d] > 0 {
		g.streak++
		d = g.addDays(d, -1)
	}
	dates := make([]string, 0, len(g.days))
	for k := range g.days {
		dates = append(dates, k)
	}
	sort.Strings(dates)
	run := 0
	for i, k := range dates {
		if i > 0 && g.addDays(dates[i-1], 1) == k {
			run++
		} else {
			run = 1
		}
		if run > g.bestStreak {
			g.bestStreak = run
		}
	}
}

type quest struct {
	Text           string
	Reward         int
	Have, Need     int
	Done           bool
	CompletedOn    string
	completedIndex int
}

// questsFor works out a week's quests from what was finished that week.
func (g *game) questsFor(week string) []quest {
	end := g.addDays(week, 7)
	var items []plugin.Work
	for _, w := range g.done {
		if d := g.date(w.DoneAt); d >= week && d < end {
			items = append(items, w)
		}
	}
	skillsNeed := g.diff.Skills
	if len(g.skills) < skillsNeed {
		skillsNeed = max(1, len(g.skills))
	}
	qs := []quest{
		{Text: fmt.Sprintf("Finish %d things", g.diff.Items), Reward: 50, Need: g.diff.Items},
		{Text: fmt.Sprintf("Show up on %d days", g.diff.Days), Reward: 40, Need: g.diff.Days},
		{Text: "Clear something high priority", Reward: 30, Need: 1},
		{Text: fmt.Sprintf("Train %d %s", skillsNeed, plural(skillsNeed, "skill")), Reward: 30, Need: skillsNeed},
	}
	days, skills := map[string]bool{}, map[string]bool{}
	for _, w := range items {
		d := g.date(w.DoneAt)
		qs[0].Have++
		days[d] = true
		qs[1].Have = len(days)
		if w.Priority >= 3 {
			qs[2].Have++
		}
		skills[w.ProjectID] = true
		qs[3].Have = len(skills)
		for i := range qs {
			if !qs[i].Done && qs[i].Have >= qs[i].Need {
				qs[i].Done, qs[i].CompletedOn = true, d
			}
		}
	}
	for i := range qs {
		qs[i].Have = min(qs[i].Have, qs[i].Need)
	}
	return qs
}

func (g *game) questsAndBonus() {
	thisWeek := g.weekOf(g.today)
	if len(g.done) > 0 {
		for w := g.weekOf(g.date(g.done[0].DoneAt)); w < thisWeek; w = g.addDays(w, 7) {
			for _, q := range g.questsFor(w) {
				if q.Done {
					g.questXP += q.Reward
				}
			}
		}
	}
	g.quests = g.questsFor(thisWeek)
	for _, q := range g.quests {
		if q.Done {
			g.questXP += q.Reward
		}
	}
}

type badge struct {
	Key, Title, Sub, Icon string
	Earned                string // date, empty while locked
}

// earnBadges replays finished work to find when each badge was earned.
func (g *game) earnBadges() []badge {
	bs := []badge{
		{Key: "first", Title: "First step", Sub: "Finish your first thing", Icon: "check"},
		{Key: "streak3", Title: "On a roll", Sub: "A 3-day streak", Icon: "flame"},
		{Key: "streak7", Title: "Unstoppable", Sub: "A 7-day streak", Icon: "flame"},
		{Key: "streak30", Title: "Iron will", Sub: "A 30-day streak", Icon: "shield"},
		{Key: "ten", Title: "Getting going", Sub: "Finish 10 things", Icon: "bolt"},
		{Key: "hundred", Title: "Centurion", Sub: "Finish 100 things", Icon: "trophy"},
		{Key: "quests", Title: "Quest hunter", Sub: "Clear every quest in a week", Icon: "target"},
		{Key: "polymath", Title: "Polymath", Sub: "Three skills at level 3", Icon: "sparkle"},
		{Key: "level5", Title: "Seasoned", Sub: "Reach level 5", Icon: "crown"},
		{Key: "early", Title: "Early bird", Sub: "Finish something before 8am", Icon: "leaf"},
		{Key: "late", Title: "Night owl", Sub: "Finish something after 10pm", Icon: "star"},
	}
	earn := func(key, date string) {
		for i := range bs {
			if bs[i].Key == key && bs[i].Earned == "" {
				bs[i].Earned = date
			}
		}
	}
	skillXP := map[string]int{}
	total, run := 0, 0
	prev := ""
	for i, w := range g.done {
		d := g.date(w.DoneAt)
		earn("first", d)
		if i+1 == 10 {
			earn("ten", d)
		}
		if i+1 == 100 {
			earn("hundred", d)
		}
		if d != prev {
			if prev != "" && g.addDays(prev, 1) == d {
				run++
			} else {
				run = 1
			}
			prev = d
		}
		for n, k := range map[int]string{3: "streak3", 7: "streak7", 30: "streak30"} {
			if run >= n {
				earn(k, d)
			}
		}
		h := w.DoneAt.In(g.loc).Hour()
		if h < 8 {
			earn("early", d)
		}
		if h >= 22 {
			earn("late", d)
		}
		x := xpFor(w)
		skillXP[w.ProjectID] += x
		total += x
		if levelFor(total).N >= 5 {
			earn("level5", d)
		}
		n3 := 0
		for _, v := range skillXP {
			if levelFor(v).N >= 3 {
				n3++
			}
		}
		if n3 >= 3 {
			earn("polymath", d)
		}
	}
	if len(g.done) > 0 {
		for w := g.weekOf(g.date(g.done[0].DoneAt)); w <= g.today; w = g.addDays(w, 7) {
			qs := g.questsFor(w)
			all, last := true, ""
			for _, q := range qs {
				all = all && q.Done
				if q.CompletedOn > last {
					last = q.CompletedOn
				}
			}
			if all {
				earn("quests", last)
				break
			}
		}
	}
	return bs
}

func (g *game) earned() []badge {
	var out []badge
	for _, b := range g.badges {
		if b.Earned != "" {
			out = append(out, b)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Earned > out[j].Earned })
	return out
}

// levelUpNotice is set when the item just finished took you, or its
// skill, up a level.
func (g *game) levelUpNotice(w plugin.Work) *notice {
	sk := g.byID[w.ProjectID]
	if sk == nil {
		return nil
	}
	x := xpFor(w)
	if levelFor(g.total-x).N < g.lv.N {
		return &notice{Text: fmt.Sprintf("Level up! You're level %d now.", g.lv.N), Date: g.today}
	}
	if levelFor(sk.XP-x).N < sk.lv.N {
		return &notice{Text: fmt.Sprintf("%s reached level %d.", sk.Name, sk.lv.N), Date: g.today}
	}
	return nil
}

// nextUp is what to finish today to keep the streak going.
func (g *game) nextUp() *plugin.Work {
	var best *plugin.Work
	score := func(w plugin.Work) int {
		s := w.Priority
		if w.PlannedFor != "" && w.PlannedFor <= g.today {
			s += 10
		}
		if w.Due != "" && w.Due <= g.today {
			s += 20
		}
		if w.StageKind == "doing" {
			s += 5
		}
		return s
	}
	for i := range g.open {
		if best == nil || score(g.open[i]) > score(*best) {
			best = &g.open[i]
		}
	}
	return best
}

func title(n int) string {
	switch {
	case n <= 1:
		return "Novice"
	case n <= 3:
		return "Apprentice"
	case n <= 5:
		return "Journeyman"
	case n <= 8:
		return "Adept"
	case n <= 12:
		return "Expert"
	case n <= 17:
		return "Master"
	}
	return "Legend"
}

// treeNodes are the unlocks on every skill's tree.
var treeNodes = []struct {
	Level       int
	Title, Perk string
}{
	{2, "Apprentice", "The skill gets its own colour ring"},
	{3, "Practised", "Counts towards Polymath"},
	{5, "Adept", "A bronze crest on its card"},
	{7, "Expert", "A silver crest"},
	{10, "Master", "A gold crest and a title"},
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}
