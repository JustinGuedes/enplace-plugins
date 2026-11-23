package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/plugins/google-play/play"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

// uploadHelp is what an app's CI needs to put builds on Play. Enplace
// never changes the app's own workflows: it says what's missing.
const uploadHelp = "Your Android build needs a step that uploads the app bundle (.aab) to Play, like the r0adkll/upload-google-play action with the same JSON key as a repo secret, the package name, and track internal for main."

type step struct {
	Title, How string
	Done       bool
	// Button goes where the step is done, like the Settings tab (ENP-361).
	Button *plugin.Button
}

func (r *run) steps(s *Status) []step {
	st, _ := r.store()
	_, hasTrack := r.track()
	c, hasCI := r.ci()
	_, hasBuild := latest(s)
	connect := "It signs in with the same Google Play account as Google Play. Add it once, then invite it in the Play Console as Google Play's steps say."
	feeds := "Pick the Play track testers get on the Settings tab. It needs a track that makes builds for testers under the product's Tracks."
	builds := "Add a Builds plugin, like GitHub Actions, that builds the Android app."
	if hasCI {
		builds = c.Name + " builds the app. " + uploadHelp
	}
	if hasBuild {
		builds = "Builds reach Google Play."
	}
	steps := []step{{Title: "Connect the Play Console", How: connect, Done: st.Connected, Button: &plugin.Button{Label: "Add the account", Href: "settings#account"}}}
	// A track that already sends builds to testers needs nothing from you, so
	// it isn't a step: a fresh product doesn't start with one ticked (ENP-367).
	if !hasTrack {
		steps = append(steps, step{Title: "Pick the track testers get", How: feeds, Button: &plugin.Button{Label: "Pick it", Href: "settings#fields"}})
	}
	return append(steps, step{Title: "Builds reach Google Play", How: builds, Done: hasBuild})
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

func (r *run) setup(s *Status) []plugin.Block {
	steps := r.steps(s)
	items := []plugin.Item{}
	for i, st := range steps {
		it := plugin.Item{Title: fmt.Sprintf("%d. %s", i+1, st.Title), Sub: st.How, Done: st.Done}
		if !st.Done {
			it.Button = st.Button
		}
		items = append(items, it)
	}
	blocks := []plugin.Block{
		{Type: plugin.BlockText, Text: fmt.Sprintf("%d of %d steps left. Press Check now after each.", len(r.stepsLeft(s)), len(steps))},
		{Type: plugin.BlockList, Items: items},
	}
	if line := r.feedsLine(); line != "" {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Tone: "muted", Text: line})
	}
	return append(blocks, plugin.Block{Type: plugin.BlockButtons, Buttons: []plugin.Button{{Label: "Check now", Action: "refresh"}}})
}

// feedsLine says which Play track each of the product's tracks sends to.
func (r *run) feedsLine() string {
	lines := []string{}
	for name, p := range r.trackMap() {
		lines = append(lines, "Builds from "+name+" go to "+p)
	}
	if len(lines) == 0 {
		return ""
	}
	sort.Strings(lines)
	return strings.Join(lines, ". ") + ". Change it on the Settings tab."
}

func (r *run) panel() []plugin.Block {
	s := r.status()
	if len(r.stepsLeft(s)) > 0 {
		return r.setup(s)
	}
	lines := []string{}
	for name, p := range r.trackMap() {
		lines = append(lines, name+" → "+p)
	}
	return []plugin.Block{{Type: plugin.BlockText, Tone: "muted", Text: "Set up. " + strings.Join(lines, " · ")}}
}

// state is a build's place, in words.
func state(b Build) string {
	if b.Draft {
		return "Draft on Play"
	}
	names := []string{}
	for _, t := range b.Tracks {
		names = append(names, play.TrackName(t))
	}
	return strings.Join(names, ", ")
}

// promoteButton moves a build one track along, from the furthest it's on.
func promoteButton(b Build) *plugin.Button {
	if b.Draft || len(b.Tracks) == 0 {
		return nil
	}
	to := play.Next(b.Tracks[len(b.Tracks)-1])
	if to == "" {
		return nil
	}
	q := "Promote " + b.Version + " (" + b.Code + ") to " + play.TrackName(to) + "?"
	label := "Promote to " + strings.TrimSuffix(play.TrackName(to), " testing")
	if to == play.Production {
		q = "Release " + b.Version + " (" + b.Code + ") on Google Play, to 10% of users at first?"
		label = "Release"
	}
	return &plugin.Button{Label: label, Action: "promote", Args: raw("code", b.Code, "to", to), Confirm: q}
}

func (r *run) overview() []plugin.Block {
	s := r.status()
	if len(r.stepsLeft(s)) > 0 && s == nil {
		return []plugin.Block{{Type: plugin.BlockText, Tone: "muted", Text: "Finish setting up Play testing in its settings."}}
	}
	blocks := []plugin.Block{}
	if b, ok := latest(s); ok {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockStat, Label: "Latest build", Value: b.Version + " (" + b.Code + ")", Sub: state(b) + " · " + ago(b.Seen, r.req.Now)})
	} else {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Tone: "muted", Text: "No builds on Google Play's testing tracks yet."})
	}
	if where, failed := r.building(s); where != "" {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: where + "…"})
	} else if failed != "" {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Tone: "warn", Text: "The build didn't start: " + failed})
	}
	return append(blocks, plugin.Block{Type: plugin.BlockButtons, Buttons: []plugin.Button{{Label: "Send to testers", Action: "send"}}})
}

func (r *run) page() *plugin.Page {
	s := r.status()
	// The settings panel above the page has the setup steps while some
	// are left, so the page doesn't repeat them.
	blocks := []plugin.Block{}
	head := plugin.Block{Type: plugin.BlockSection, ID: "latest", Label: "Latest build", Text: "No builds yet", Buttons: []plugin.Button{{Label: "Send to testers", Action: "send"}}}
	if b, ok := latest(s); ok {
		head.Text, head.Sub = b.Version+" ("+b.Code+")", state(b)+" · "+ago(b.Seen, r.req.Now)
		if b.Notes != "" {
			head.Blocks = notes(b.Notes)
		}
	}
	if where, failed := r.building(s); where != "" {
		head.Blocks = append(head.Blocks, plugin.Block{Type: plugin.BlockText, Text: where + "…"})
	} else if failed != "" {
		head.Blocks = append(head.Blocks, plugin.Block{Type: plugin.BlockText, Tone: "warn", Text: "The build didn't start: " + failed})
	}
	blocks = append(blocks, head)
	if s != nil && len(s.Builds) > 0 {
		items := []plugin.Item{}
		for _, b := range s.Builds {
			items = append(items, plugin.Item{Title: b.Version + " (" + b.Code + ")", Sub: state(b) + " · " + ago(b.Seen, r.req.Now), Button: promoteButton(b)})
		}
		blocks = append(blocks, plugin.Block{Type: plugin.BlockSection, ID: "builds", Text: "Builds", Blocks: []plugin.Block{{Type: plugin.BlockList, Items: items}}})
	}
	if s != nil {
		if s.Error != "" {
			blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Tone: "warn", Text: "Last check: " + s.Error + "."})
		}
		for _, n := range s.Notes {
			blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Tone: "warn", Text: n})
		}
	}
	return &plugin.Page{Title: "Play testing", Blocks: blocks}
}

// notes draws release notes a line each.
func notes(text string) []plugin.Block {
	out := []plugin.Block{}
	for _, l := range strings.Split(text, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, plugin.Block{Type: plugin.BlockText, Text: l})
		}
	}
	return out
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
	case t.IsZero():
		return ""
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	}
	return fmt.Sprintf("%d days ago", int(d.Hours()/24))
}
