package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/plugins/google-play/play"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

// Where the setup steps send people.
const (
	urlAPI       = "https://console.cloud.google.com/apis/library/androidpublisher.googleapis.com"
	urlReporting = "https://console.cloud.google.com/apis/library/playdeveloperreporting.googleapis.com"
	urlAccount   = "https://console.cloud.google.com/iam-admin/serviceaccounts/create"
	urlConsole   = "https://play.google.com/console/developers"
)

type step struct {
	Title, How string
	Done       bool
	Buttons    []plugin.Button
}

// steps connect the Play Console. Each ticks itself off once a check sees
// it done.
func (r *run) steps(s *Status) []step {
	if s == nil {
		s = &Status{}
	}
	key := r.req.Secrets["play_key"] != "" || s.Key
	invite := "Play Console → Users and permissions → Invite new users. Paste the service account's email"
	if s.Email != "" {
		invite = "Play Console → Users and permissions → Invite new users. Paste " + s.Email
	}
	invite += ", add this app under App permissions, and tick View app information, Release to testing tracks, Manage testing tracks, Release to production and Reply to reviews."
	return []step{
		{Title: "The app's package name", Done: r.pkg() != "",
			How: orElse(r.pkg(), "Add it to the product's App details, like com.example.game. It's the applicationId your Android build uses.")},
		{Title: "Switch on the Google Play APIs", Done: s.Connected || (key && s.Reason != "" && s.Reason != play.ReasonAPIOff && s.Reason != play.ReasonKey),
			How:     "In a Google Cloud project of yours, switch on the Google Play Android Developer API, and the Play Developer Reporting API for crash rates.",
			Buttons: []plugin.Button{{Label: "Play API", URL: urlAPI}, {Label: "Reporting API", URL: urlReporting}}},
		{Title: "Make a key for Enplace", Done: key && s.Reason != play.ReasonKey,
			How:     "Create a service account in the same project (no roles needed), then Keys → Add key → JSON. Paste the whole file into Play Console key in these settings.",
			Buttons: []plugin.Button{{Label: "Create service account", URL: urlAccount}}},
		{Title: "Invite it in the Play Console", Done: s.Connected, How: invite,
			Buttons: []plugin.Button{{Label: "Open the Play Console", URL: urlConsole}}},
	}
}

func (r *run) stepsLeft(s *Status) []string {
	out := []string{}
	for _, st := range r.steps(s) {
		if !st.Done {
			out = append(out, st.Title)
		}
	}
	return out
}

// setup draws the steps that are left, and why the last check failed.
func (r *run) setup(s *Status) []plugin.Block {
	steps := r.steps(s)
	left := len(r.stepsLeft(s))
	blocks := []plugin.Block{{Type: plugin.BlockText, Text: fmt.Sprintf("%d of %d steps left, about 3 minutes, once for all your Android apps. Each ticks itself off when Check sees it done.", left, len(steps))}}
	items := []plugin.Item{}
	for i, st := range steps {
		it := plugin.Item{Title: fmt.Sprintf("%d. %s", i+1, st.Title), Sub: st.How, Done: st.Done}
		if !st.Done && len(st.Buttons) > 0 {
			b := st.Buttons[0]
			it.Button = &b
		}
		items = append(items, it)
	}
	blocks = append(blocks, plugin.Block{Type: plugin.BlockList, Items: items})
	if s != nil && s.Error != "" && s.Key {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: "Last check: " + s.Error + ".", Tone: "warn"})
	}
	btns := []plugin.Button{{Label: "Check", Action: "refresh"}, {Label: "Reporting API", URL: urlReporting}}
	return append(blocks, plugin.Block{Type: plugin.BlockButtons, Buttons: btns})
}

// panel is the settings panel: the steps while some are left, then a line.
func (r *run) panel() []plugin.Block {
	s := r.status()
	if s != nil && s.Connected && r.pkg() != "" {
		line := "Connected to the Play Console as " + s.Email + "."
		if !s.CheckedAt.IsZero() {
			line += " Checked " + ago(s.CheckedAt, r.req.Now) + "."
		}
		return []plugin.Block{{Type: plugin.BlockText, Text: line, Tone: "muted"},
			{Type: plugin.BlockButtons, Buttons: []plugin.Button{{Label: "Check now", Action: "refresh"}}}}
	}
	return r.setup(s)
}

// level is how much the card asks for attention: a halted rollout or
// vitals over Play's line are loud.
func (r *run) level() string {
	s := r.status()
	switch {
	case s == nil || !s.Connected:
		return plugin.LevelQuiet
	case s.Live != nil && s.Live.Status == play.Halted, s.Vitals != nil && (s.Vitals.Crash > play.CrashLine || s.Vitals.ANR > play.ANRLine):
		return plugin.LevelLoud
	}
	return plugin.LevelNormal
}

func (r *run) overview() []plugin.Block {
	s := r.status()
	if r.pkg() == "" && r.setting("play_url") == "" {
		return []plugin.Block{{Type: plugin.BlockText, Text: "Add the app's package name to its App details, then connect the Play Console in settings.", Tone: "muted"}}
	}
	live := "Not live yet"
	if v := r.version(s); v != "" {
		live = v + " live"
	}
	blocks := []plugin.Block{{Type: plugin.BlockStat, Label: "Google Play", Value: live, Sub: r.liveSub(s)}}
	if s == nil || !s.Connected {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: "Connect the Play Console to follow rollouts, reviews and crashes.", Tone: "muted"})
	} else if line := vitalsLine(s); line != "" {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: line, Tone: vitalsTone(s)})
	}
	if u := r.storeURL(); u != "" {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockButtons, Buttons: []plugin.Button{{Label: "Google Play", URL: u}}})
	}
	return blocks
}

// liveSub is the line under the live version: its rollout and rating.
func (r *run) liveSub(s *Status) string {
	parts := []string{}
	if s != nil && s.Live != nil {
		switch s.Live.Status {
		case play.InProgress:
			parts = append(parts, "rolling out to "+percent(s.Live.UserFraction))
		case play.Halted:
			parts = append(parts, "rollout halted at "+percent(s.Live.UserFraction))
		}
	}
	if s != nil {
		if avg, n := rating(s.Reviews); n > 0 {
			parts = append(parts, fmt.Sprintf("★ %.1f from %d recent", avg, n))
		}
	}
	if len(parts) == 0 {
		return r.pkg()
	}
	return strings.Join(parts, " · ")
}

func vitalsLine(s *Status) string {
	if s.Vitals == nil {
		return ""
	}
	return "Crashes " + percent2(s.Vitals.Crash) + " · ANRs " + percent2(s.Vitals.ANR) + " of daily users"
}

func vitalsTone(s *Status) string {
	if s.Vitals != nil && (s.Vitals.Crash > play.CrashLine || s.Vitals.ANR > play.ANRLine) {
		return "warn"
	}
	return "muted"
}

// page is the Google Play page: setup while it's not connected, then
// production, the testing tracks, vitals and reviews.
func (r *run) page() *plugin.Page {
	s := r.status()
	blocks := []plugin.Block{}
	if s == nil || !s.Connected || r.pkg() == "" {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockSection, ID: "setup", Text: "Connect the Play Console", Blocks: r.setup(s)})
		return &plugin.Page{Title: "Google Play", Blocks: blocks}
	}
	blocks = append(blocks, r.production(s))
	if sec, ok := r.testing(s); ok {
		blocks = append(blocks, sec)
	}
	blocks = append(blocks, r.vitals(s), r.reviews(s))
	for _, n := range s.Notes {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: n, Tone: "warn"})
	}
	if s.Error != "" {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: "Last check: " + s.Error + ".", Tone: "warn"})
	}
	btns := []plugin.Button{{Label: "Check now", Action: "refresh"}}
	if u := r.storeURL(); u != "" {
		btns = append(btns, plugin.Button{Label: "Google Play", URL: u})
	}
	blocks = append(blocks, plugin.Block{Type: plugin.BlockButtons, Buttons: btns})
	return &plugin.Page{Title: "Google Play", Blocks: blocks}
}

// production shows what's live and the rollout's buttons.
func (r *run) production(s *Status) plugin.Block {
	sec := plugin.Block{Type: plugin.BlockSection, ID: "production", Label: "Production", Text: "Nothing on production yet"}
	if s.Live == nil {
		sec.Blocks = []plugin.Block{{Type: plugin.BlockText, Tone: "muted", Text: "Promote a build from a testing track to production in Play testing, or in the Play Console."}}
		return sec
	}
	l := s.Live
	sec.Text, sec.Sub = l.Name+" ("+l.Code()+")", "Everyone has it"
	switch l.Status {
	case play.InProgress, play.Halted:
		sec.Sub = "Rolling out to " + percent(l.UserFraction) + " of users"
		if l.Status == play.Halted {
			sec.Sub = "Rollout halted at " + percent(l.UserFraction)
		}
		sec.Blocks = []plugin.Block{{Type: plugin.BlockProgress, Label: "Rollout", Value: percent(l.UserFraction), Progress: l.UserFraction}}
		sec.Buttons = rolloutButtons(*l)
	}
	return sec
}

// rolloutButtons are the next steps of a staged rollout.
func rolloutButtons(l play.Release) []plugin.Button {
	if l.Status == play.Halted {
		return []plugin.Button{{Label: "Resume", Action: "rollout", Args: raw("do", "resume"), Confirm: "Resume the rollout at " + percent(l.UserFraction) + "?"}}
	}
	btns := []plugin.Button{{Label: "Halt", Action: "rollout", Args: raw("do", "halt"), Confirm: "Halt the rollout? Nobody new gets " + l.Name + "."}}
	for _, f := range []float64{0.05, 0.1, 0.2, 0.5} {
		if f > l.UserFraction {
			btns = append(btns, plugin.Button{Label: "Raise to " + percent(f), Action: "rollout", Args: raw("to", strconv.FormatFloat(f, 'f', -1, 64)), Confirm: "Raise " + l.Name + " to " + percent(f) + " of users?"})
			break
		}
	}
	return append(btns, plugin.Button{Label: "Release to everyone", Action: "rollout", Args: raw("to", "1"), Confirm: "Release " + l.Name + " to everyone?"})
}

// testing lists the testing tracks' releases. Play testing has the builds
// and their buttons; this is the glance.
func (r *run) testing(s *Status) (plugin.Block, bool) {
	items := []plugin.Item{}
	for _, sh := range s.Tracks {
		if sh.Track == play.Production {
			continue
		}
		items = append(items, plugin.Item{Title: play.TrackName(sh.Track), Value: sh.Release.Name + " (" + sh.Release.Code() + ")"})
	}
	if len(items) == 0 {
		return plugin.Block{}, false
	}
	return plugin.Block{Type: plugin.BlockSection, ID: "tracks", Text: "Testing tracks", Blocks: []plugin.Block{{Type: plugin.BlockList, Items: items}}}, true
}

func (r *run) vitals(s *Status) plugin.Block {
	sec := plugin.Block{Type: plugin.BlockSection, ID: "vitals", Text: "Crashes and freezes"}
	if s.Vitals == nil {
		sec.Blocks = []plugin.Block{{Type: plugin.BlockText, Tone: "muted", Text: "No numbers yet. Play has them once enough people use the app, a couple of days behind."}}
		return sec
	}
	sec.Sub = "Share of daily users, " + s.Vitals.Day
	sec.Blocks = []plugin.Block{{Type: plugin.BlockStats, Items: []plugin.Item{
		{Title: "Crashes", Value: percent2(s.Vitals.Crash), Sub: "Play's line is " + percent2(play.CrashLine)},
		{Title: "ANRs", Value: percent2(s.Vitals.ANR), Sub: "Play's line is " + percent2(play.ANRLine)},
	}}}
	if vitalsTone(s) == "warn" {
		sec.Blocks = append(sec.Blocks, plugin.Block{Type: plugin.BlockText, Tone: "warn", Text: "Over Play's line: Google Play shows the app to fewer people until it comes down."})
	}
	return sec
}

// reviews lists the week's reviews with a form to reply to one.
func (r *run) reviews(s *Status) plugin.Block {
	sec := plugin.Block{Type: plugin.BlockSection, ID: "reviews", Text: "Reviews this week"}
	if len(s.Reviews) == 0 {
		sec.Blocks = []plugin.Block{{Type: plugin.BlockText, Tone: "muted", Text: "No reviews in the last week. Play only shares a week of reviews."}}
		return sec
	}
	if avg, n := rating(s.Reviews); n > 0 {
		sec.Sub = fmt.Sprintf("★ %.1f from %d", avg, n)
	}
	items, open := []plugin.Item{}, []string{}
	for i, v := range s.Reviews {
		if i == 8 {
			break
		}
		sub := v.Text
		if v.Reply != "" {
			sub += "\nYou: " + v.Reply
		} else {
			open = append(open, label(v))
		}
		items = append(items, plugin.Item{Title: stars(v.Stars) + "  " + orElse(v.Author, "Someone"), Sub: sub, Value: v.At.Format("2 Jan")})
	}
	sec.Blocks = []plugin.Block{{Type: plugin.BlockList, Items: items}}
	if len(open) > 0 {
		sec.Blocks = append(sec.Blocks, plugin.Block{Type: plugin.BlockForm, Text: "Reply to a review",
			Fields: []plugin.Field{
				{Key: "review", Label: "Review", Type: "select", Options: open, Value: open[0], Required: true},
				{Key: "text", Label: "Reply", Type: "textarea", Required: true, Help: "Shows under the review on Google Play."},
			},
			Buttons: []plugin.Button{{Label: "Send reply", Action: "reply"}}})
	}
	return sec
}

// label names a review in the reply form; the reply action maps it back.
func label(v play.Review) string {
	words := strings.Fields(v.Text)
	if len(words) > 6 {
		words = append(words[:6], "…")
	}
	return stars(v.Stars) + " " + orElse(v.Author, "Someone") + ": " + strings.Join(words, " ")
}

func stars(n int) string {
	if n < 0 || n > 5 {
		n = 0
	}
	return strings.Repeat("★", n) + strings.Repeat("☆", 5-n)
}

func percent(f float64) string { return strconv.FormatFloat(f*100, 'f', -1, 64) + "%" }

func percent2(f float64) string { return strconv.FormatFloat(f*100, 'f', 2, 64) + "%" }

func orElse(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func raw(kv ...string) json.RawMessage {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	b, _ := json.Marshal(m)
	return b
}

func ago(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	}
	return fmt.Sprintf("%d days ago", int(d.Hours()/24))
}
