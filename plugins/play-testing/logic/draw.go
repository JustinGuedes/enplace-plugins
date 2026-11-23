package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/plugins/google-play/play"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

// secretName is the repo secret the upload step reads the key from.
const secretName = "PLAY_SERVICE_ACCOUNT_JSON"

type step struct {
	Title, How string
	Done       bool
	// Button goes where the step is done, like the Settings tab (ENP-361).
	Button *plugin.Button
	// More are buttons under the steps while this one is open (ENP-379).
	More []plugin.Button
}

func (r *run) steps(s *Status) []step {
	st, _ := r.store()
	track, hasTrack := r.track()
	_, hasBuild := latest(s)
	connect := "Uses the key you add for Google Play. Once it's added and invited in the Play Console, this ticks by itself."
	if st.Connected {
		connect = "Connected as " + st.Email + "."
	}
	feeds := "Pick the Play track testers get on the Settings tab. It needs a track that makes builds for testers under the product's Tracks."
	steps := []step{{Title: "Connect the Play Console", How: connect, Done: st.Connected, Button: &plugin.Button{Label: "Add the account", Href: "settings#account"}}}
	// A track that already sends builds to testers needs nothing from you, so
	// it isn't a step: a fresh product doesn't start with one ticked (ENP-367).
	if !hasTrack {
		steps = append(steps, step{Title: "Pick the track testers get", How: feeds, Button: &plugin.Button{Label: "Pick it", Href: "settings#fields"}})
	}
	return append(steps, r.uploadStep(st.Package, playTrack(track), hasBuild))
}

// uploadStep says whether the app's builds already go to Google Play,
// from its Builds plugin's workflow files, and hands over the step that
// sends them when they don't. Enplace never changes the app's own
// workflows (ENP-379).
func (r *run) uploadStep(pkg, track string, hasBuild bool) step {
	s := step{Title: "Send each build to Google Play", Done: hasBuild}
	c, hasCI := r.ci()
	sends := hasCI && c.Scanned && slices.Contains(c.Uploads, "google-play")
	firstByHand := " Google only takes an app's very first build by hand: upload one .aab in the Play Console under Testing › Internal testing."
	switch {
	case hasBuild:
		s.How = "Builds reach Google Play."
		return s
	case !hasCI:
		s.How = "Add a Builds plugin, like GitHub Actions, that builds the Android app."
		return s
	case sends:
		s.How = "Your " + c.Name + " workflow already sends builds to Google Play. A build shows here once one gets there." + firstByHand
		return s
	case c.Scanned:
		s.How = "Your " + c.Name + " workflow builds the app but doesn't send it to Google Play yet. Copy the step into your Android job, after the build, and save the Google Play key as a repo secret named " + secretName + "."
	default:
		s.How = "Your app's build has to send each build to Google Play, and Enplace can't read your workflow files to see if it does. If it doesn't, copy the step into your Android job, after the build, and save the Google Play key as a repo secret named " + secretName + "."
	}
	s.How += firstByHand
	s.More = []plugin.Button{{Label: "Copy the step", Copy: uploadYAML(pkg, track)}}
	if c.Repo != "" {
		s.More = append(s.More, plugin.Button{Label: "Add the repo secret", URL: "https://github.com/" + c.Repo + "/settings/secrets/actions/new"})
	}
	return s
}

// uploadYAML is a GitHub Actions step that uploads the build to Play.
func uploadYAML(pkg, track string) string {
	if pkg == "" {
		pkg = "com.example.app # your package name"
	}
	if track == "" {
		track = play.Internal
	}
	return fmt.Sprintf(`      - name: Send the build to Google Play
        uses: r0adkll/upload-google-play@v1
        with:
          serviceAccountJsonPlainText: ${{ secrets.%s }}
          packageName: %s
          releaseFiles: "**/*.aab"
          track: %s
`, secretName, pkg, track)
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
		{Type: plugin.BlockText, Text: fmt.Sprintf("%d of %d steps left. Each ticks by itself; Check now looks straight away.", len(r.stepsLeft(s)), len(steps))},
		{Type: plugin.BlockList, Items: items},
	}
	for _, st := range steps {
		if !st.Done && len(st.More) > 0 {
			blocks = append(blocks, plugin.Block{Type: plugin.BlockButtons, Buttons: st.More})
		}
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
