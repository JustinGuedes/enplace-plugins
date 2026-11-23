// Command logic is the TestFlight plugin (id "testflight"): it gets an app
// product's builds onto the person's own phone.
//
// It asks for a build through the builds contract ("public.build_request")
// and never knows who makes it: the App Store plugin passes it to the
// builder of its track (Xcode Cloud, an agent on the person's Mac, or the
// person in Xcode) and shares how it's going ("public.build"). TestFlight
// takes the build from there once it's in App Store Connect.
//
// It builds on the App Store plugin: it reads which app the product is
// from App's "public.app", signs in with the product's Apple account
// (added once in Settings → Connections) and reuses
// App's code for the key (plugins/app/store through package beta). Versions
// gives the version and build number ("public.versions"), and it shares
// the builds as "public.builds" for Release cycle.
package main

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/plugins/app/store"
	"github.com/JustinGuedes/enplace-plugins/plugins/testflight/beta"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

func main() { plugin.Run(handle) }

// Status is what the last check found, stored per product.
type Status struct {
	CheckedAt time.Time `json:"checked_at"`
	BundleID  string    `json:"bundle_id,omitempty"`
	AppName   string    `json:"app_name,omitempty"`
	// Keys says the App Store Connect key is set; AppID that the app was
	// found in App Store Connect.
	Keys  bool   `json:"keys"`
	AppID string `json:"app_id,omitempty"`
	// IDReady says Apple knows the bundle ID (Enplace registers it), so
	// it can be picked when the app is added in App Store Connect.
	IDReady bool          `json:"id_ready,omitempty"`
	Group   *beta.Group   `json:"group,omitempty"`
	Testers []beta.Tester `json:"testers,omitempty"`
	Builds  []beta.Build  `json:"builds,omitempty"`
	// Error is App Store Connect saying no; Notes are smaller problems,
	// like a tester that couldn't be added.
	Error string   `json:"error,omitempty"`
	Notes []string `json:"notes,omitempty"`
}

const keyStatus = "status"

type run struct {
	req *plugin.Request
	res *plugin.Response
	pid string
}

func handle(req *plugin.Request) (*plugin.Response, error) {
	req.SignIn("apple") // the Apple account this product uses (ENP-351)
	r := &run{req: req, res: &plugin.Response{}, pid: req.ProjectID}
	if r.pid == "" {
		return r.res, nil
	}
	switch req.Hook {
	case plugin.HookSchedule:
		r.refresh()
	case plugin.HookAction:
		if req.Action != nil {
			r.action(req.Action)
		}
	case plugin.HookRender:
		for _, slot := range req.Slots {
			switch slot {
			case "project_card:testflight":
				r.card(slot, r.overview())
			case plugin.SlotSettingsPanel:
				r.card(slot, r.panel())
			}
		}
	}
	return r.res, nil
}

func (r *run) card(slot string, blocks []plugin.Block) {
	if r.res.Cards == nil {
		r.res.Cards = map[string][]plugin.Block{}
	}
	r.res.Cards[slot] = blocks
}

func (r *run) setting(k string) string {
	v, _ := r.req.ProjectSettings[r.pid][k].(string)
	return strings.TrimSpace(v)
}

func (r *run) on(k string) bool {
	v, ok := r.req.ProjectSettings[r.pid][k].(bool)
	return v || !ok
}

func (r *run) group() string {
	if g := r.setting("group"); g != "" {
		return g
	}
	return "Me"
}

func (r *run) testers() []string {
	out := []string{}
	for _, e := range strings.FieldsFunc(r.setting("testers"), func(c rune) bool { return c == ',' || c == ';' || c == ' ' || c == '\n' }) {
		if strings.Contains(e, "@") {
			out = append(out, strings.ToLower(e))
		}
	}
	return out
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

// app is which app the product is, from the App Store plugin.
func (r *run) app() (bundleID, name string) {
	var a struct {
		BundleID string `json:"bundle_id"`
		Name     string `json:"name"`
	}
	r.peer("app", "public.app", &a)
	if a.Name == "" {
		// Before it's live the store has no name for it: use the project's.
		parent, _, _ := strings.Cut(r.pid, "/")
		for _, p := range r.req.Projects {
			if p.ID == parent {
				a.Name = p.Name
			}
		}
	}
	return a.BundleID, a.Name
}

func (r *run) status() *Status {
	var s *Status
	r.req.State.GetProject(r.pid, keyStatus, &s)
	return s
}

// fetched counts this call's fetches: Enplace allows 20.
var fetched int

const maxFetched = 20

func call(method, u string, headers map[string]string, body []byte) (int, []byte, error) {
	fetched++
	res, err := plugin.Fetch(plugin.FetchRequest{Method: method, URL: u, Headers: headers, Body: body})
	if err != nil {
		return 0, nil, err
	}
	return res.Status, res.Body, nil
}

func (r *run) client() (*beta.Client, error) {
	s := r.req.Secrets
	return beta.New(call, s["asc_issuer_id"], s["asc_key_id"], s["asc_private_key"], r.req.Now)
}

func (r *run) hasKeys() bool {
	s := r.req.Secrets
	return s["asc_issuer_id"] != "" && s["asc_key_id"] != "" && s["asc_private_key"] != ""
}

// refresh checks TestFlight, sets up the group and its testers, writes
// What to test for a new build, and publishes the builds.
func (r *run) refresh() {
	was := r.status()
	s := &Status{CheckedAt: r.req.Now, Keys: r.hasKeys()}
	s.BundleID, s.AppName = r.app()
	defer func() {
		r.res.SetProject(r.pid, keyStatus, s)
		r.publish(s)
		r.trackTestBuild(s)
	}()
	if !s.Keys || s.BundleID == "" {
		return
	}
	c, err := r.client()
	if err != nil {
		s.Error = err.Error()
		return
	}
	if s.AppID, err = c.AppID(s.BundleID); err != nil || s.AppID == "" {
		if err != nil {
			s.Error = err.Error()
			s.keep(was)
			return
		}
		r.register(c, s, was)
		return
	}
	s.IDReady = true
	g, _, err := c.Group(s.AppID, r.group())
	if err != nil {
		s.Error = "Couldn't make the tester group: " + err.Error()
		s.keep(was)
		return
	}
	s.Group = &g
	if s.Testers, err = c.Testers(g.ID); err != nil {
		s.Notes = append(s.Notes, "Couldn't read the testers: "+err.Error())
	}
	r.invite(c, s)
	if s.Builds, err = c.Builds(s.AppID, 8); err != nil {
		s.Error = err.Error()
		s.keep(was)
		return
	}
	if r.on("what_to_test") {
		r.writeWhatToTest(c, s)
	}
}

// register makes sure Apple knows the bundle ID before the app is added,
// so the person only has to pick it from the list.
func (r *run) register(c *beta.Client, s *Status, was *Status) {
	if was != nil && was.IDReady && was.BundleID == s.BundleID {
		s.IDReady = true
		return
	}
	made, err := c.BundleID(s.BundleID, r.appName(s))
	if err != nil {
		if e, ok := err.(*store.Error); ok && e.Status == 403 {
			s.Notes = append(s.Notes, "The key can't register the bundle ID. Give it the Admin role in App Store Connect, or register "+s.BundleID+" yourself under Certificates, Identifiers & Profiles.")
			return
		}
		s.Notes = append(s.Notes, "Couldn't register the bundle ID: "+err.Error())
		return
	}
	s.IDReady = true
	if made {
		r.res.Log = "Registered the bundle ID " + s.BundleID + " with Apple"
	}
}

// appName is what the app is called in App Store Connect: the product's
// App details, else what the App Store plugin knows.
func (r *run) appName(s *Status) string {
	if n := r.setting("app_name"); n != "" {
		return n
	}
	return s.AppName
}

// keep holds on to what was known through a hiccup.
func (s *Status) keep(was *Status) {
	if was != nil && was.BundleID == s.BundleID {
		if s.Group == nil {
			s.Group = was.Group
		}
		if s.Testers == nil {
			s.Testers = was.Testers
		}
		s.Builds = was.Builds
	}
}

// invite adds the testers the settings name that aren't in the group yet.
func (r *run) invite(c *beta.Client, s *Status) {
	in := map[string]bool{}
	for _, t := range s.Testers {
		in[strings.ToLower(t.Email)] = true
	}
	added := false
	for _, e := range r.testers() {
		if in[e] || fetched >= maxFetched-4 {
			continue
		}
		if err := c.AddTester(s.Group.ID, e); err != nil {
			s.Notes = append(s.Notes, err.Error())
			continue
		}
		added = true
	}
	if added {
		if ts, err := c.Testers(s.Group.ID); err == nil {
			s.Testers = ts
		}
	}
}

// writeWhatToTest fills in the newest build's What to test, when it's new
// and empty, from the work finished since the build before it.
func (r *run) writeWhatToTest(c *beta.Client, s *Status) {
	if len(s.Builds) == 0 || fetched >= maxFetched-1 {
		return
	}
	b := s.Builds[0]
	if b.WhatToTest != "" || b.State == beta.Failed || b.State == beta.Expired || r.req.Now.Sub(b.Uploaded) > 7*24*time.Hour {
		return
	}
	since := b.Uploaded.Add(-14 * 24 * time.Hour)
	if len(s.Builds) > 1 {
		since = s.Builds[1].Uploaded
	}
	text := r.roundText(b.Number)
	if text == "" {
		text = whatToTest(r.req.Work, r.pid, since, b.Uploaded)
	}
	if text == "" {
		return
	}
	if err := c.SetWhatToTest(b, text); err != nil {
		s.Notes = append(s.Notes, "Couldn't write What to test: "+err.Error())
		return
	}
	s.Builds[0].WhatToTest = text
}

// whatToTest lists the work finished on the product between two times.
func whatToTest(work []plugin.Work, pid string, from, to time.Time) string {
	lines := []string{}
	for _, w := range work {
		if w.ProjectID != pid || w.StageKind != "done" || w.DoneAt.Before(from) || w.DoneAt.After(to) || hasTag(w, "testflight") || hasTag(w, "build") {
			continue
		}
		if len(lines) == 8 {
			break
		}
		lines = append(lines, "- "+w.Title)
	}
	if len(lines) == 0 {
		return ""
	}
	return "New in this build:\n" + strings.Join(lines, "\n")
}

func hasTag(w plugin.Work, tag string) bool {
	for _, t := range w.Tags {
		if strings.EqualFold(t, tag) {
			return true
		}
	}
	return false
}

// publish shares the builds, for Release cycle.
func (r *run) publish(s *Status) {
	type pub struct {
		Version  string    `json:"version"`
		Build    string    `json:"build"`
		State    string    `json:"state"`
		Uploaded time.Time `json:"uploaded"`
	}
	out := []pub{}
	for _, b := range s.Builds {
		out = append(out, pub{b.Version, b.Number, b.State, b.Uploaded})
	}
	r.res.SetProject(r.pid, "public.builds", out)
}

func (r *run) action(a *plugin.Action) {
	args := map[string]string{}
	_ = json.Unmarshal(a.Args, &args)
	switch a.Name {
	case "refresh":
		r.refresh()
		r.res.Toast = "Checked TestFlight"
	case "send":
		r.send()
	case "test_build":
		r.testBuild(a.Args)
	case "no_encryption":
		r.answerEncryption(args["build"])
	case "tool_testflight":
		r.res.Data = r.tool()
	}
}

// answerEncryption says, for the person, that a build uses no special
// encryption, so it can go to testers.
func (r *run) answerEncryption(buildID string) {
	if buildID == "" {
		return
	}
	c, err := r.client()
	if err == nil {
		err = c.NoSpecialEncryption(buildID)
	}
	if err != nil {
		r.res.Toast = "Couldn't answer: " + err.Error()
		return
	}
	r.res.Log = "Said the build uses no special encryption"
	r.refresh()
	r.res.Toast = "Answered. The build goes to your testers once Apple has it."
}
