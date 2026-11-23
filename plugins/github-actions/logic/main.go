// Command logic is the GitHub Actions plugin (id "github-actions"): a
// repo's CI builds.
//
// It builds on the GitHub plugin: the repo comes from GitHub's
// "public.code" and the token is GitHub's, shared with it. It follows the
// CI contract any CI plugin can (docs/writing-plugins.md): it shares its
// runs as "public.ci" and starts one run for each "public.ci_request".
//
// One instance reads GitHub: the project's, or a product's when the
// project doesn't have the plugin. A product with the plugin under a
// project that has it only reads the project's runs and shows its own
// job, so switching it on for a product costs no calls to GitHub.
//
// On an app product it's also a builder (the builds contract): when the
// product's "Builds come from" picks it, it starts the workflow for the
// store build App passes on ("public.build_request").
package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

func main() { plugin.Run(handle) }

const selfID = "github-actions"

// Request is a build another plugin asks for (its "public.ci_request", or
// the store build App passes on).
type Request struct {
	ID       string    `json:"id"`
	Workflow string    `json:"workflow,omitempty"`
	Branch   string    `json:"branch,omitempty"`
	Reason   string    `json:"reason,omitempty"`
	At       time.Time `json:"at"`
}

// Answer is how a request went. GitHub doesn't say which run a start
// made, so Run stays 0.
type Answer struct {
	State string    `json:"state"` // started or failed
	Run   int       `json:"run,omitempty"`
	Error string    `json:"error,omitempty"`
	At    time.Time `json:"at"`
	By    string    `json:"by,omitempty"`
}

// Status is what the last read of GitHub found, stored per scope.
type Status struct {
	CheckedAt time.Time         `json:"checked_at"`
	Repo      string            `json:"repo,omitempty"`
	Workflows []Workflow        `json:"workflows,omitempty"`
	Runs      []Run             `json:"runs,omitempty"`
	Requests  map[string]Answer `json:"requests,omitempty"`
	Error     string            `json:"error,omitempty"`
	// Since is when it first read the repo's runs: failures before it
	// are never filed as bugs.
	Since time.Time       `json:"since,omitzero"`
	Bugs  map[string]*Bug `json:"bugs,omitempty"`
}

const keyStatus = "status"

type run struct {
	req *plugin.Request
	res *plugin.Response
	pid string
	// cur is the status a refresh is making, newer than the stored one.
	cur *Status
}

func handle(req *plugin.Request) (*plugin.Response, error) {
	req.SignIn("github") // the GitHub account this product uses (ENP-351)
	r := &run{req: req, res: &plugin.Response{}, pid: req.ProjectID}
	if r.pid == "" {
		return r.res, nil
	}
	switch req.Hook {
	case plugin.HookSchedule:
		r.refresh(nil)
	case plugin.HookWebhook:
		r.webhook()
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

// Setup is what one CI set-up builds: the workflow, the branch Run starts
// it on and, on a product, the job that builds the product. It's kept
// together so a product can later have several (main builds, beta,
// release).
type Setup struct {
	Workflow string
	Branch   string
	Job      string
}

func (r *run) setup(scope string) Setup {
	get := func(k string) string {
		v, _ := r.req.ProjectSettings[scope][k].(string)
		return strings.TrimSpace(v)
	}
	return Setup{Workflow: get("workflow"), Branch: get("branch"), Job: get("job")}
}

func (r *run) setting(k string) string {
	v, _ := r.req.ProjectSettings[r.pid][k].(string)
	return strings.TrimSpace(v)
}

// scope is the project or product this call is for.
func (r *run) scope() plugin.Project {
	for _, p := range r.req.Projects {
		if p.ID == r.pid {
			return p
		}
	}
	return plugin.Project{ID: r.pid}
}

// parent is the project when this is a product under a project that has
// the plugin too, and it then reads GitHub for both.
func (r *run) parent() string {
	sc := r.scope()
	if sc.Parent == "" {
		return ""
	}
	for _, p := range r.req.Projects {
		if p.ID == sc.Parent && p.On {
			return p.ID
		}
	}
	return ""
}

// reads says this instance reads GitHub itself.
func (r *run) reads() bool { return r.parent() == "" }

// status is the runs this scope shows: its own read, or its project's.
func (r *run) status() *Status {
	from := r.pid
	if p := r.parent(); p != "" {
		from = p
	} else if r.cur != nil {
		return r.cur
	}
	var s *Status
	r.req.State.GetProject(from, keyStatus, &s)
	return s
}

func (r *run) own() *Status {
	var s *Status
	r.req.State.GetProject(r.pid, keyStatus, &s)
	return s
}

// peer reads what a linked plugin published for this scope, or for its
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

// code is what the GitHub plugin found: the repo and its branch.
func (r *run) code() (repo, branch string) {
	var c struct {
		Repo   string `json:"repo"`
		Branch string `json:"branch"`
	}
	r.peer("github", "public.code", &c)
	return c.Repo, c.Branch
}

func (r *run) client() *client {
	repo, _ := r.code()
	return &client{token: r.req.Secrets["token"], repo: repo}
}

// refresh reads GitHub (when this instance does), starts what was asked
// for (start, from a button, or a linked plugin's request) and shares
// the runs.
func (r *run) refresh(start *Request) {
	was := r.own()
	s := &Status{CheckedAt: r.req.Now, Requests: map[string]Answer{}}
	r.cur = s
	if was != nil {
		for k, v := range was.Requests {
			s.Requests[k] = v
		}
		s.Since, s.Bugs, s.Workflows, s.Runs = was.Since, was.Bugs, was.Workflows, was.Runs
	}
	defer func() {
		trim(s.Requests, 20)
		r.res.SetProject(r.pid, keyStatus, s)
		r.publish(s)
	}()
	c := r.client()
	s.Repo = c.repo
	if c.repo == "" {
		return
	}
	if r.reads() {
		r.read(c, s, was)
	}
	for by, q := range r.requests() {
		if _, done := s.Requests[q.ID]; !done && r.req.Now.Sub(q.At) < time.Hour {
			a := r.start(c, q)
			a.By = by
			s.Requests[q.ID] = a
		}
	}
	if start != nil {
		a := r.start(c, *start)
		if a.Error != "" {
			r.res.Toast = "Couldn't start it: " + a.Error
		} else {
			r.res.Toast = "Started it on GitHub. It shows here once GitHub picks it up."
			r.res.Log = "Started a GitHub Actions run"
		}
	}
}

// runCount is how many runs it keeps.
const runCount = 8

// read fetches workflows, runs and the jobs of runs that changed.
func (r *run) read(c *client, s *Status, was *Status) {
	var err error
	if s.Workflows, err = c.workflows(); err != nil {
		s.Error = err.Error()
		return
	}
	var id int64
	if w, ok := pick(s.Workflows, r.setup(r.pid).Workflow); ok && r.setup(r.pid).Workflow != "" {
		id = w.ID
	}
	runs, err := c.runs(id, runCount)
	if err != nil {
		s.Error = err.Error()
		return
	}
	known := map[string]Run{}
	if was != nil {
		for _, x := range was.Runs {
			known[x.ID] = x
		}
	}
	for i, x := range runs {
		// A finished run's jobs don't change: keep them rather than ask again.
		if k, ok := known[x.ID]; ok && k.Status == x.Status && k.Finished.Equal(x.Finished) && len(k.Jobs) > 0 && x.Status != "running" && x.Status != "queued" {
			runs[i].Jobs = k.Jobs
			continue
		}
		if runs[i].Jobs, err = c.jobs(x.ID); err != nil {
			s.Error = err.Error()
		}
	}
	s.Runs = runs
	r.watch(c, s, was)
}

// pick finds a workflow by name or file (build.yml or its path); a blank
// name picks nothing.
func pick(ws []Workflow, name string) (Workflow, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return Workflow{}, false
	}
	for _, w := range ws {
		file := w.Path[strings.LastIndex(w.Path, "/")+1:]
		if strings.ToLower(w.Name) == name || strings.ToLower(w.Path) == name || strings.ToLower(file) == name {
			return w, true
		}
	}
	return Workflow{}, false
}

// target is the workflow Run starts and the branch it starts it on.
func (r *run) target(q Request) (workflow, branch string) {
	st := r.setup(r.pid)
	s := r.status()
	workflow = q.Workflow
	if workflow == "" {
		workflow = st.Workflow
	}
	if workflow == "" && s != nil && len(s.Runs) > 0 {
		workflow = s.Runs[0].Workflow
	}
	if s != nil {
		if w, ok := pick(s.Workflows, workflow); ok {
			workflow = w.Path[strings.LastIndex(w.Path, "/")+1:]
		}
	}
	branch = q.Branch
	if branch == "" {
		branch = st.Branch
	}
	if branch == "" {
		_, branch = r.code()
	}
	if branch == "" {
		branch = "main"
	}
	return workflow, branch
}

func (r *run) start(c *client, q Request) Answer {
	a := Answer{At: r.req.Now}
	workflow, branch := r.target(q)
	if workflow == "" {
		a.State, a.Error = "failed", "no workflow to start yet: pick one in the GitHub Actions settings"
		return a
	}
	if err := c.dispatch(workflow, branch); err != nil {
		a.State, a.Error = "failed", err.Error()
		return a
	}
	a.State = "started"
	return a
}

// requests are the builds linked plugins ask for, by the plugin asking:
// CI runs anyone asks for, and the store build App passes on when this is
// the builder the app product picked.
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
			out["app"] = q
		}
	}
	return out
}

// picked says the app product's "Builds come from" is GitHub Actions.
func (r *run) picked() bool {
	var a struct {
		Builder struct {
			ID string `json:"id"`
		} `json:"builder"`
	}
	r.peer("app", "public.app", &a)
	return a.Builder.ID == selfID
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

// publish shares the runs the way every CI plugin does, and, on an app
// product, how it's doing as a builder.
func (r *run) publish(s *Status) {
	shown := r.status()
	runs := []Run{}
	if shown != nil && shown.Runs != nil {
		runs = shown.Runs
	}
	ready := shown != nil && shown.Repo != "" && len(shown.Workflows) > 0 && shown.Error == ""
	r.res.SetProject(r.pid, "public.ci", map[string]any{
		"provider": selfID, "name": "GitHub Actions", "ready": ready,
		"runs": runs, "requests": s.Requests,
	})
	if _, ok := r.req.Peers["app"]; !ok {
		return
	}
	note := ""
	workflow, _ := r.target(Request{})
	switch {
	case s.Repo == "":
		note = "GitHub Actions needs the GitHub plugin linked to the repo first."
	case !ready:
		note = "GitHub Actions can't read the repo's workflows yet: check the GitHub token has Actions: read and write."
	case workflow == "":
		note = "Pick the workflow that makes the app in the GitHub Actions settings."
	}
	r.res.SetProject(r.pid, "public.builder", map[string]any{
		"name": "GitHub Actions", "ready": note == "", "note": note, "requests": s.Requests,
	})
}

func (r *run) action(a *plugin.Action) {
	switch a.Name {
	case "refresh":
		r.refresh(nil)
		if r.res.Toast == "" {
			r.res.Toast = "Checked GitHub Actions"
			if s := r.own(); s != nil && s.Error != "" {
				r.res.Toast = "GitHub Actions: " + s.Error
			}
		}
	case "start":
		r.refresh(&Request{ID: fmt.Sprintf("button-%d", r.req.Now.Unix()), Reason: "Started from Enplace"})
	case "hook_connect":
		r.connect()
	case "hook_disconnect":
		r.disconnect()
	case "workflows":
		r.res.Data = r.workflowChoices()
	case "jobs":
		r.res.Data = r.jobChoices()
	case "tool_ci":
		s := r.status()
		if s == nil {
			r.res.Data = map[string]any{"set_up": false}
			return
		}
		r.res.Data = map[string]any{"set_up": s.Repo != "" && len(s.Workflows) > 0, "repo": s.Repo, "workflows": s.Workflows,
			"runs": s.Runs, "job": r.jobFor(r.scope()), "error": s.Error, "checked_at": s.CheckedAt}
	}
}

func (r *run) workflowChoices() plugin.Choices {
	out := plugin.Choices{Choices: []plugin.Choice{}}
	s := r.status()
	if s == nil || len(s.Workflows) == 0 {
		out.Note = "Enplace hasn't read this repo's workflows yet. Link the repo in the GitHub plugin, then press Check now."
		return out
	}
	for _, w := range s.Workflows {
		c := plugin.Choice{Value: w.Name, Detail: w.Path}
		if w.State != "active" {
			c.Badge = "Off"
		}
		out.Choices = append(out.Choices, c)
	}
	return out
}

func (r *run) jobChoices() plugin.Choices {
	out := plugin.Choices{Choices: []plugin.Choice{}}
	seen := map[string]bool{}
	if s := r.status(); s != nil {
		for _, x := range s.Runs {
			for _, j := range x.Jobs {
				if !seen[j.Name] {
					seen[j.Name] = true
					out.Choices = append(out.Choices, plugin.Choice{Value: j.Name, Detail: x.Workflow})
				}
			}
		}
	}
	if len(out.Choices) == 0 {
		out.Note = "No jobs yet: they show here once the workflow has run."
	}
	return out
}
