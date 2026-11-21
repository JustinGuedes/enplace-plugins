package main

// The builds contract (docs/writing-plugins.md, "Builds"). An app's store
// builds can come from any builder: a plugin like Xcode Cloud, an agent on
// the person's Mac, or the person in Xcode. The product's "Builds come
// from" setting picks one, here, so every plugin that wants a build
// (TestFlight now, a release plugin later) asks the same way and none of
// them knows who builds:
//
//   - a plugin that wants a build publishes "public.build_request";
//   - App passes it on as its own "public.build_request" for the builder
//     it picked, and says which that is in "public.app" (builder);
//   - a builder plugin publishes "public.builder" (name, ready, note and
//     how each request went), and App shares the picked one's as
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
	// Version and Build are what the build should be numbered, when the
	// asker knows; a builder that numbers its own builds can ignore them.
	Version string `json:"version,omitempty"`
	Build   string `json:"build,omitempty"`
	// By is the plugin that asked, filled in by App.
	By string `json:"by,omitempty"`
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

// builder is the one "Builds come from" picks. Left blank, it's the
// builder plugin on the product (a ready one first), else the person.
func (r *run) builder() Builder {
	want := r.setting("builder")
	all := r.builders()
	switch {
	case strings.EqualFold(want, byMac):
		return Builder{ID: macID, Name: byMac, Ready: true}
	case strings.EqualFold(want, byHand):
		return Builder{ID: handID, Name: byHand, Ready: true}
	case want != "":
		for _, b := range all {
			if strings.EqualFold(b.Name, want) || b.ID == want {
				return b
			}
		}
		return Builder{ID: want, Name: want, Note: want + " isn't on this app. Switch its plugin on, or pick another in Builds come from."}
	}
	for _, b := range all {
		if b.Ready {
			return b
		}
	}
	if len(all) > 0 {
		return all[0]
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
	b := r.builder()
	q, asked := r.request()
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
		r.res.SetProject(r.pid, "public.build_request", q)
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

// builderChoices lists the builders for "Builds come from".
func (r *run) builderChoices() plugin.Choices {
	out := plugin.Choices{Choices: []plugin.Choice{}}
	for _, b := range r.builders() {
		c := plugin.Choice{Value: b.Name, Detail: "Plugin"}
		if !b.Ready {
			c.Badge = "Not ready"
			c.Detail = b.Note
		}
		out.Choices = append(out.Choices, c)
	}
	if len(out.Choices) == 0 {
		out.Note = "Add a plugin that makes builds, like Xcode Cloud, to build in the cloud."
	}
	out.Choices = append(out.Choices,
		plugin.Choice{Value: byMac, Detail: "Files a work item for an agent on your Mac"},
		plugin.Choice{Value: byHand, Detail: "Product → Archive in Xcode, then Distribute App"})
	return out
}
