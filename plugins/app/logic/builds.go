package main

// The builds contract (docs/writing-plugins.md, "Builds"). An app's store
// builds can come from any builder: a plugin like Xcode Cloud, an agent on
// the person's Mac, or the person in Xcode. The builder plugin that is the
// track's Builds step makes it, so every plugin that wants a build
// (TestFlight now, a release plugin later) asks the same way and none of
// them knows who builds:
//
//   - a plugin that wants a build publishes "public.build_request";
//   - App passes it on as its own "public.build_request" for the builder
//     it picked, and says which that is in "public.app" (builder);
//   - a builder plugin publishes "public.builder" (name, ready, note, the
//     tracks it builds and how each request went), and App shares the picked one's as
//     "public.build" for the plugin that asked.
//
// The Mac builder is App's own: it files a work item for an agent there.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

// The builders that aren't plugins.
const (
	byMac  = "An agent on my Mac"
	byHand = "I upload from Xcode"
	macID  = "mac"
	handID = "hand"
)

// BuildRequest is a build a plugin asks for.
type BuildRequest struct {
	ID     string    `json:"id"`
	At     time.Time `json:"at"`
	Reason string    `json:"reason,omitempty"`
	Branch string    `json:"branch,omitempty"`
	// Track is the product's track to build on (ENP-350), like
	// "internal-beta": the builder uses that track's own settings.
	Track string `json:"track,omitempty"`
	// Version and Build are what the build should be numbered, when the
	// asker knows; a builder that numbers its own builds can ignore them.
	Version string `json:"version,omitempty"`
	Build   string `json:"build,omitempty"`
	// By is the plugin that asked, and To the builder App passed it on
	// to: both filled in by App.
	By string `json:"by,omitempty"`
	To string `json:"to,omitempty"`
}

// Answer is how a builder took a request.
type Answer struct {
	State string    `json:"state"` // started or failed
	Run   int       `json:"run,omitempty"`
	Work  string    `json:"work,omitempty"`
	Error string    `json:"error,omitempty"`
	At    time.Time `json:"at"`
}

// Builder is one way the app's builds get made.
type Builder struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Ready    bool              `json:"ready"`
	Note     string            `json:"note,omitempty"`
	Requests map[string]Answer `json:"requests,omitempty"`
	// Tracks are the product's tracks this builder is the Builds step of.
	Tracks []string `json:"tracks,omitempty"`
	// ByTrack is who builds each of the product's tracks, in the
	// "public.build" App shares, so a plugin knows before it asks.
	ByTrack map[string]Builder `json:"by_track,omitempty"`
}

// peer reads what a linked plugin published for this product, or for its
// project when the plugin is on the project instead.
func (r *run) peer(id, key string, v any) bool {
	st, ok := r.req.Peers[id]
	if !ok {
		return false
	}
	if st.GetProject(r.pid, key, v) {
		return true
	}
	if parent, _, found := strings.Cut(r.pid, "/"); found {
		return st.GetProject(parent, key, v)
	}
	return false
}

// builders are the plugins that make builds, sorted by name.
func (r *run) builders() []Builder {
	out := []Builder{}
	for id := range r.req.Peers {
		var b Builder
		if r.peer(id, "public.builder", &b) {
			b.ID = id
			if b.Name == "" {
				b.Name = id
			}
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// builder makes the app's builds for a track (ENP-351): the builder
// plugin that is the track's Builds step, a ready one first. A request
// with no track, or builders that don't say their tracks, take any builder
// plugin on the product. Without one it's "Without a build plugin": an
// agent on the Mac or the person in Xcode.
func (r *run) builder(track string) Builder {
	all := r.builders()
	if track != "" {
		var on []Builder
		for _, b := range all {
			for _, t := range b.Tracks {
				if t == track {
					on = append(on, b)
				}
			}
		}
		if len(on) > 0 {
			all = on
		}
	}
	for _, b := range all {
		if b.Ready {
			return b
		}
	}
	if len(all) > 0 {
		return all[0]
	}
	if strings.EqualFold(r.setting("builder"), byMac) {
		return Builder{ID: macID, Name: byMac, Ready: true}
	}
	return Builder{ID: handID, Name: byHand, Ready: true}
}

// request is the newest build a linked plugin asks for, less than an hour
// old.
func (r *run) request() (BuildRequest, bool) {
	var best BuildRequest
	for id := range r.req.Peers {
		var q BuildRequest
		if r.peer(id, "public.build_request", &q) && q.ID != "" && q.At.After(best.At) {
			q.By = id
			best = q
		}
	}
	return best, best.ID != "" && r.req.Now.Sub(best.At) < time.Hour
}

// routeBuilds passes the newest build request on to the picked builder
// (or makes the Mac's work item itself) and shares how it went.
func (r *run) routeBuilds() {
	q, asked := r.request()
	b := r.builder(q.Track)
	var mine map[string]Answer
	r.req.State.GetProject(r.pid, "mac_requests", &mine)
	if mine == nil {
		mine = map[string]Answer{}
	}
	switch {
	case b.ID == macID:
		if _, done := mine[q.ID]; asked && !done {
			mine[q.ID] = r.buildOnMac(q)
			r.res.SetProject(r.pid, "mac_requests", trimAnswers(mine, 10))
		}
		b.Requests = mine
		r.res.SetProject(r.pid, "public.build_request", nil)
	case b.ID == handID || !asked:
		r.res.SetProject(r.pid, "public.build_request", nil)
	default:
		q.To = b.ID
		r.res.SetProject(r.pid, "public.build_request", q)
	}
	if ts := r.req.Tracks[r.pid]; len(ts) > 0 {
		b.ByTrack = map[string]Builder{}
		for _, t := range ts {
			tb := r.builder(t.Key)
			tb.Tracks = nil
			if tb.ID == b.ID {
				tb.Requests = b.Requests
			}
			b.ByTrack[t.Key] = tb
		}
	}
	r.res.SetProject(r.pid, "public.build", b)
}

func trimAnswers(m map[string]Answer, n int) map[string]Answer {
	if len(m) <= n {
		return m
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return m[keys[i]].At.After(m[keys[j]].At) })
	for _, k := range keys[n:] {
		delete(m, k)
	}
	return m
}

// openBuild is the Mac's build item still in flight for this product.
func (r *run) openBuild() *plugin.Work {
	for i, w := range r.req.Work {
		if w.ProjectID == r.pid && w.StageKind != "done" && w.StageKind != "cancelled" && hasTag(w, "build") {
			return &r.req.Work[i]
		}
	}
	return nil
}

func hasTag(w plugin.Work, tag string) bool {
	for _, t := range w.Tags {
		if strings.EqualFold(t, tag) {
			return true
		}
	}
	return false
}

// buildOnMac files a work item for an agent on the person's Mac to build
// the app and upload it to App Store Connect.
func (r *run) buildOnMac(q BuildRequest) Answer {
	a := Answer{At: r.req.Now, State: "started"}
	if w := r.openBuild(); w != nil {
		a.Work = w.Key
		return a
	}
	for _, op := range r.req.Waiting {
		if op.Kind == "work.create" && op.ProjectID == r.pid {
			return a
		}
	}
	in := r.info()
	if in == nil || in.BundleID == "" {
		a.State, a.Error = "failed", "the App Store plugin doesn't know the app's bundle ID yet"
		return a
	}
	name := in.Name
	if name == "" {
		name = r.project().Name
	}
	title := strings.TrimSpace(fmt.Sprintf("Build %s %s (%s) and upload it", name, q.Version, q.Build))
	r.res.Ops = append(r.res.Ops, plugin.Op{Kind: "work.create", ProjectID: r.pid, Asked: true, Params: map[string]any{
		"title": strings.ReplaceAll(title, " ()", ""),
		"body":  macBody(in.BundleID, q, r.req.Secrets),
		"tags":  []string{"build", "release"},
		"doer":  "agent",
	}})
	r.res.Log = title
	return a
}

// macBody is what the agent needs: everything but the private key, which
// stays on the Mac.
func macBody(bundleID string, q BuildRequest, sec map[string]string) string {
	keyID := strings.TrimSpace(sec["asc_key_id"])
	branch := q.Branch
	if branch == "" {
		branch = "main"
	}
	lines := []string{
		"Build the app from the repo's " + branch + " branch and upload it to App Store Connect. Follow the **upload-a-build** skill.",
		"",
		"- Bundle ID: `" + bundleID + "`",
	}
	if q.Version != "" {
		lines = append(lines, "- Version: `"+q.Version+"` (MARKETING_VERSION)")
	}
	if q.Build != "" {
		lines = append(lines, "- Build number: `"+q.Build+"` (CURRENT_PROJECT_VERSION)")
	}
	lines = append(lines,
		"- App Store Connect key ID: `"+keyID+"`, file `~/.appstoreconnect/private_keys/AuthKey_"+keyID+".p8`",
		"- Issuer ID: `"+strings.TrimSpace(sec["asc_issuer_id"])+"`",
		"",
		"Set the version and build number on the command line, don't commit them. When the upload is done, add a note with what you uploaded and hand it back. Enplace takes the build from there once Apple has processed it.",
	)
	return strings.Join(lines, "\n")
}
