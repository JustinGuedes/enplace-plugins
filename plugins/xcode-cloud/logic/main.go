// Command logic is the Xcode Cloud plugin (id "xcode-cloud"): Apple's CI
// for an app product.
//
// Like TestFlight it builds on the App Store plugin: the app's bundle ID
// comes from App's "public.app", and it signs in with the same App Store
// Connect key through App's client (plugins/app/store). It follows the CI
// contract any CI plugin can (docs/writing-plugins.md): another plugin asks
// for a build with "public.ci_request", it starts one run per request, and
// it shares its runs and how each request went as "public.ci".
//
// It's also a builder (the builds contract): when the app product's
// "Builds come from" picks it, it answers the store builds App passes on
// ("public.build_request") with its own workflow, "Enplace store build",
// which it makes with Set it up and keeps right. It never knows who wanted
// the build or what happens to it after: its "Builds it makes" setting
// says what the archive is for, in Apple's words.
package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/plugins/xcode-cloud/cloud"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

func main() { plugin.Run(handle) }

// Request is a build another plugin asks for (its "public.ci_request").
type Request struct {
	ID       string `json:"id"`
	Workflow string `json:"workflow,omitempty"`
	Branch   string `json:"branch,omitempty"`
	Reason   string `json:"reason,omitempty"`
	// Store is a store build passed on by App (public.build_request),
	// made with Enplace's own workflow.
	Store bool `json:"-"`
	// At is when it was asked: one older than an hour is never started,
	// so switching the plugin on doesn't start an old build.
	At time.Time `json:"at"`
}

// Answer is how a request went.
type Answer struct {
	State string    `json:"state"` // started or failed
	Run   int       `json:"run,omitempty"`
	Error string    `json:"error,omitempty"`
	At    time.Time `json:"at"`
	By    string    `json:"by,omitempty"` // the plugin that asked
}

// Status is what the last check found, stored per product.
type Status struct {
	CheckedAt time.Time        `json:"checked_at"`
	BundleID  string           `json:"bundle_id,omitempty"`
	Keys      bool             `json:"keys"`
	AppID     string           `json:"app_id,omitempty"`
	ProductID string           `json:"product_id,omitempty"`
	NotSetUp  bool             `json:"not_set_up,omitempty"`
	Workflows []cloud.Workflow `json:"workflows,omitempty"`
	// Own is Enplace's workflow when it has one; Branch is the branch the
	// Default workflow builds, for starting Own with no branch named.
	Own    string `json:"own,omitempty"`
	Branch string `json:"branch,omitempty"`
	// SetUpError is why Set it up last failed.
	SetUpError string            `json:"set_up_error,omitempty"`
	Runs       []cloud.Run       `json:"runs,omitempty"`
	Requests   map[string]Answer `json:"requests,omitempty"`
	Error      string            `json:"error,omitempty"`
	// InStep says Enplace's workflow matched the settings at the last
	// check; Kept is when Enplace last changed it and KeptWhat how.
	InStep   bool      `json:"in_step,omitempty"`
	Kept     time.Time `json:"kept,omitempty"`
	KeptWhat string    `json:"kept_what,omitempty"`
	// Since is when it first saw the app's runs: failures before it are
	// never filed as bugs.
	Since time.Time `json:"since,omitempty"`
	// Bugs are the failing builds it filed, by workflow and branch.
	Bugs map[string]*Bug `json:"bugs,omitempty"`
}

const keyStatus = "status"

type run struct {
	req *plugin.Request
	res *plugin.Response
	pid string
}

func handle(req *plugin.Request) (*plugin.Response, error) {
	r := &run{req: req, res: &plugin.Response{}, pid: req.ProjectID}
	if r.pid == "" {
		return r.res, nil
	}
	switch req.Hook {
	case plugin.HookSchedule:
		r.refresh(nil)
	case plugin.HookAction:
		if req.Action != nil {
			r.action(req.Action)
		}
	case plugin.HookRender:
		for _, slot := range req.Slots {
			switch slot {
			case "project_card:ci":
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

func (r *run) bundleID() string {
	var a struct {
		BundleID string `json:"bundle_id"`
	}
	r.peer("app", "public.app", &a)
	return a.BundleID
}

func (r *run) status() *Status {
	var s *Status
	r.req.State.GetProject(r.pid, keyStatus, &s)
	return s
}

func (r *run) hasKeys() bool {
	s := r.req.Secrets
	return s["asc_issuer_id"] != "" && s["asc_key_id"] != "" && s["asc_private_key"] != ""
}

func call(method, u string, headers map[string]string, body []byte) (int, []byte, error) {
	res, err := plugin.Fetch(plugin.FetchRequest{Method: method, URL: u, Headers: headers, Body: body})
	if err != nil {
		return 0, nil, err
	}
	return res.Status, res.Body, nil
}

// requests are the builds linked plugins ask for, by the plugin asking:
// CI runs anyone asks for, and the store build App passes on when this
// is the builder the app product picked.
func (r *run) requests() map[string]Request {
	out := map[string]Request{}
	for id := range r.req.Peers {
		var q Request
		if r.peer(id, "public.ci_request", &q) && q.ID != "" {
			out[id] = q
		}
	}
	if r.picked() {
		var q Request
		if r.peer("app", "public.build_request", &q) && q.ID != "" {
			q.Store = true
			out["app"] = q
		}
	}
	return out
}

const selfID = "xcode-cloud"

// picked says the app product's "Builds come from" is Xcode Cloud.
func (r *run) picked() bool {
	var a struct {
		Builder struct {
			ID string `json:"id"`
		} `json:"builder"`
	}
	r.peer("app", "public.app", &a)
	return a.Builder.ID == selfID
}

// audience is what the "Builds it makes" setting asks for.
func (r *run) audience() string {
	switch b := r.setting("makes"); {
	case strings.HasPrefix(b, "TestFlight"):
		return cloud.ForInternal
	case strings.HasPrefix(b, "Don"):
		return cloud.ForNobody
	}
	return cloud.ForStore
}

// refresh reads Xcode Cloud, starts what was asked for (start, from a
// button, or a linked plugin's request) and shares the runs.
func (r *run) refresh(start *Request) {
	was := r.status()
	s := &Status{CheckedAt: r.req.Now, Keys: r.hasKeys(), BundleID: r.bundleID(), Requests: map[string]Answer{}}
	if was != nil {
		for k, v := range was.Requests {
			s.Requests[k] = v
		}
		s.Kept, s.KeptWhat, s.Since, s.Bugs = was.Kept, was.KeptWhat, was.Since, was.Bugs
	}
	defer func() {
		trim(s.Requests, 20)
		r.res.SetProject(r.pid, keyStatus, s)
		r.publish(s)
	}()
	if !s.Keys || s.BundleID == "" {
		return
	}
	c, err := cloud.New(call, r.req.Secrets["asc_issuer_id"], r.req.Secrets["asc_key_id"], r.req.Secrets["asc_private_key"], r.req.Now)
	if err != nil {
		s.Error = err.Error()
		return
	}
	if s.AppID, err = c.AppID(s.BundleID); err != nil || s.AppID == "" {
		if err != nil {
			s.Error = err.Error()
		}
		return
	}
	if s.ProductID, err = c.Product(s.AppID); err != nil {
		s.NotSetUp = err == cloud.ErrNotSetUp
		if !s.NotSetUp {
			s.Error = err.Error()
		}
		return
	}
	if s.Workflows, err = c.Workflows(s.ProductID); err != nil {
		s.Error = err.Error()
		return
	}
	if was != nil {
		s.Branch, s.SetUpError = was.Branch, was.SetUpError
	}
	if own, ok := cloud.Own(s.Workflows); ok {
		s.Own = own.ID
		r.keep(c, s, own)
	}
	for by, q := range r.requests() {
		if _, done := s.Requests[q.ID]; !done && r.req.Now.Sub(q.At) < time.Hour {
			a := r.start(c, s, q)
			a.By = by
			s.Requests[q.ID] = a
		}
	}
	if start != nil {
		a := r.start(c, s, *start)
		if a.Error != "" {
			r.res.Toast = "Couldn't start it: " + a.Error
		} else {
			r.res.Toast = fmt.Sprintf("Started Xcode Cloud build %d", a.Run)
			r.res.Log = fmt.Sprintf("Started Xcode Cloud build %d", a.Run)
		}
	}
	if s.Runs, err = c.Runs(s.ProductID, 8); err != nil {
		s.Error = err.Error()
		if was != nil {
			s.Runs = was.Runs
		}
		return
	}
	r.watch(c, s, was)
}

// start runs the asked-for workflow (or the project's), on the asked-for
// branch (or the project's).
func (r *run) start(c *cloud.Client, s *Status, q Request) Answer {
	a := Answer{At: r.req.Now}
	want := q.Workflow
	if want == "" && q.Store && s.Own != "" {
		want = s.Own
	}
	if want == "" {
		want = r.setting("workflow")
	}
	w, ok := cloud.Pick(s.Workflows, want)
	if !ok {
		a.State, a.Error = "failed", "no workflow to start: press Set it up in the Xcode Cloud settings"
		if want != "" {
			a.Error = "Xcode Cloud has no workflow called " + want
		}
		return a
	}
	branch := q.Branch
	if branch == "" {
		branch = r.setting("branch")
	}
	if branch == "" && w.ID == s.Own {
		// Enplace's workflow starts by hand, so it needs a branch.
		branch = s.Branch
		if branch == "" {
			branch = "main"
		}
	}
	n, err := c.Start(w.ID, branch)
	if err != nil {
		a.State, a.Error = "failed", err.Error()
		return a
	}
	a.State, a.Run = "started", n
	return a
}

// trim keeps the newest n answers.
func trim(m map[string]Answer, n int) {
	if len(m) <= n {
		return
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return m[keys[i]].At.After(m[keys[j]].At) })
	for _, k := range keys[n:] {
		delete(m, k)
	}
}

// publish shares the runs the way every CI plugin does.
func (r *run) publish(s *Status) {
	runs := s.Runs
	if runs == nil {
		runs = []cloud.Run{}
	}
	r.res.SetProject(r.pid, "public.ci", map[string]any{
		"provider": selfID, "name": "Xcode Cloud", "ready": s.ProductID != "",
		"runs": runs, "requests": s.Requests,
	})
	ready, note := r.builderReady(s)
	r.res.SetProject(r.pid, "public.builder", map[string]any{
		"name": "Xcode Cloud", "ready": ready, "note": note, "requests": s.Requests,
	})
}

// builderReady says whether Xcode Cloud can make a store build now, and
// if not, what's missing, in words for the person.
func (r *run) builderReady(s *Status) (bool, string) {
	switch {
	case !s.Keys || s.BundleID == "" || s.AppID == "":
		return false, "Xcode Cloud needs the app's bundle ID and App Store Connect key first."
	case s.ProductID == "":
		return false, "Connect Xcode Cloud once in Xcode: Product → Xcode Cloud → Create Workflow."
	case r.audience() == cloud.ForNobody:
		return false, "Xcode Cloud is set to make builds that aren't uploaded (Builds it makes)."
	case s.Own == "" && r.setting("workflow") == "":
		return false, "Press Set it up in the Xcode Cloud settings so Enplace can make its workflow."
	}
	return true, ""
}

func (r *run) action(a *plugin.Action) {
	switch a.Name {
	case "refresh":
		r.refresh(nil)
		if r.res.Toast == "" {
			r.res.Toast = "Checked Xcode Cloud"
		}
	case "setup":
		r.setUp()
	case "update":
		r.update()
	case "start":
		r.refresh(&Request{ID: fmt.Sprintf("button-%d", r.req.Now.Unix()), Reason: "Started from Enplace"})
	case "workflows":
		r.res.Data = r.choices()
	case "tool_ci":
		s := r.status()
		if s == nil {
			r.res.Data = map[string]any{"set_up": false}
			return
		}
		r.res.Data = map[string]any{"set_up": s.ProductID != "", "not_set_up_in_xcode": s.NotSetUp, "workflows": s.Workflows, "runs": s.Runs, "error": s.Error, "checked_at": s.CheckedAt}
	}
}

// choices lists the workflows for the Workflow setting.
func (r *run) choices() plugin.Choices {
	out := plugin.Choices{Choices: []plugin.Choice{}}
	s := r.status()
	switch {
	case s == nil || s.ProductID == "":
		out.Note = "Enplace can't see Xcode Cloud for this app yet. Finish the steps above, then press Check now."
	case len(s.Workflows) == 0:
		out.Note = "This app has no workflows yet. Make one in Xcode: Product → Xcode Cloud → Create Workflow."
	}
	if s != nil {
		for _, w := range s.Workflows {
			c := plugin.Choice{Value: w.Name}
			if !w.Enabled {
				c.Badge = "Off"
			}
			out.Choices = append(out.Choices, c)
		}
	}
	return out
}
