//go:build !wasip1

package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin/plugintest"
)

const (
	pid  = plugintest.ProjectID
	ios  = pid + "/ios"
	mac  = pid + "/mac"
	repo = "acme/game"
)

// Everything the manifest says the plugin draws opens on a fresh workspace.
func TestSmoke(t *testing.T) { plugintest.Smoke(t, handle, "../manifest.json") }

// fake stands in for GitHub: each answer by "METHOD path" (no query). It
// returns every call it was sent, with its body.
func fake(t *testing.T, answers map[string]string) *[]string {
	sent := []string{}
	plugin.HostFunc = func(raw []byte) []byte {
		var in struct {
			Fetch plugin.FetchRequest `json:"fetch"`
		}
		_ = json.Unmarshal(raw, &in)
		m := in.Fetch.Method
		if m == "" {
			m = "GET"
		}
		path, _, _ := strings.Cut(strings.TrimPrefix(in.Fetch.URL, api), "?")
		sent = append(sent, strings.TrimSpace(m+" "+path+" "+string(in.Fetch.Body)))
		status, body := 404, `{"message":"Not Found"}`
		if a, ok := answers[m+" "+path]; ok {
			status, body = 200, a
			if m == "POST" && a == "" {
				status = 204
			}
		}
		out, _ := json.Marshal(map[string]any{"fetch": plugin.FetchResponse{Status: status, Body: []byte(body)}})
		return out
	}
	t.Cleanup(func() { plugin.HostFunc = nil })
	return &sent
}

const base = "/repos/" + repo + "/actions"

// runAt is a run made d ago: "142 completed failure 5m".
func runAt(spec string) string {
	p := strings.Fields(spec)
	d, _ := time.ParseDuration(p[3])
	at := plugintest.Now.Add(-d).Format(time.RFC3339)
	return `{"id":` + p[0] + `00,"name":"Build","run_number":` + p[0] + `,"workflow_id":7,"head_branch":"main","head_sha":"abcdef1234","display_title":"Add harbour quest","status":"` + p[1] + `","conclusion":"` + p[2] + `","event":"push","html_url":"https://github.com/acme/game/actions/runs/` + p[0] + `00","created_at":"` + at + `","updated_at":"` + at + `"}`
}

// jobsOf is a run's jobs: "iOS failure", "Android success".
func jobsOf(specs ...string) string {
	out := []string{}
	for i, s := range specs {
		name, conc, _ := strings.Cut(s, " ")
		id := string(rune('1' + i))
		step := `{"name":"Unity build","conclusion":"` + conc + `"}`
		out = append(out, `{"id":`+id+`,"name":"Build (`+name+`)","status":"completed","conclusion":"`+conc+`","html_url":"https://github.com/job`+id+`","started_at":"2026-03-02T08:00:00Z","completed_at":"2026-03-02T08:41:00Z","steps":[{"name":"Checkout","conclusion":"success"},`+step+`]}`)
	}
	return `{"jobs":[` + strings.Join(out, ",") + `]}`
}

func answers(runs ...string) map[string]string {
	list := []string{}
	for _, r := range runs {
		list = append(list, runAt(r))
	}
	return map[string]string{
		"GET " + base + "/workflows":                       `{"workflows":[{"id":7,"name":"Build","path":".github/workflows/build.yml","state":"active"},{"id":8,"name":"Story snapshot","path":".github/workflows/story-snapshot.yml","state":"active"}]}`,
		"GET " + base + "/runs":                            `{"workflow_runs":[` + strings.Join(list, ",") + `]}`,
		"GET " + base + "/workflows/7/runs":                `{"workflow_runs":[` + strings.Join(list, ",") + `]}`,
		"GET " + base + "/runs/14200/jobs":                 jobsOf("iOS failure", "Android success"),
		"GET " + base + "/runs/14100/jobs":                 jobsOf("iOS success", "Android success"),
		"GET /repos/" + repo + "/check-runs/1/annotations": `[{"path":"Assets/Scripts/Quests/Board.cs","start_line":44,"annotation_level":"failure","message":"error CS0246: The type 'HarbourQuest' could not be found"},{"path":".github","annotation_level":"warning","message":"Node 16 is old"}]`,
		"GET " + base + "/jobs/1/logs":                     "2026-03-02T08:40:58.1234567Z Building player\n2026-03-02T08:40:59.1234567Z Build failed with 1 error\n",
		"POST " + base + "/workflows/build.yml/dispatches": "",
		"POST " + base + "/runs/14200/rerun-failed-jobs":   "",
	}
}

// request is a call for scope with GitHub linked and products ios and mac.
func request(hook, scope string) *plugin.Request {
	req := plugintest.Request(hook)
	req.ProjectID = scope
	req.Plugin.ID = selfID
	req.Projects[0].Products = []plugin.ProductInfo{{Key: "ios", Name: "iPhone app", Type: "app", On: true}, {Key: "mac", Name: "Mac app", Type: "app", On: true}}
	req.Projects = append(req.Projects,
		plugin.Project{ID: ios, On: true, Product: "ios", ProductName: "iPhone app", ProductType: "app", Parent: pid},
		plugin.Project{ID: mac, On: true, Product: "mac", ProductName: "Mac app", ProductType: "app", Parent: pid})
	req.ProjectSettings = map[string]map[string]any{pid: {"workflow": "Build"}, ios: {}, mac: {}}
	req.Secrets = map[string]string{"token": "t"}
	req.Peers = map[string]plugin.State{}
	publish(req, "github", pid, "public.code", map[string]string{"repo": repo, "branch": "main"})
	return req
}

func publish(req *plugin.Request, id, scope, k string, v any) {
	st := req.Peers[id]
	st.PutProject(scope, k, v)
	req.Peers[id] = st
}

func stateOf(t *testing.T, res *plugin.Response, scope string) *Status {
	t.Helper()
	var s *Status
	if !res.State.GetProject(scope, keyStatus, &s) {
		t.Fatalf("no status for %s", scope)
	}
	return s
}

// read runs a schedule for the project and gives back its new state.
func read(t *testing.T, req *plugin.Request) (*plugin.Response, *Status) {
	t.Helper()
	res := plugintest.Call(t, handle, req)
	return res, stateOf(t, res, pid)
}

func TestItReadsRunsWithEachJob(t *testing.T) {
	fake(t, answers("142 completed failure 5m", "141 completed success 2h"))
	res, s := read(t, request(plugin.HookSchedule, pid))
	if len(s.Runs) != 2 || s.Runs[0].Status != "failed" || len(s.Runs[0].Jobs) != 2 {
		t.Fatalf("runs %+v", s.Runs)
	}
	if j := s.Runs[0].Jobs[0]; j.Name != "Build (iOS)" || j.Status != "failed" || j.Step != "Unity build" {
		t.Fatalf("job %+v", j)
	}
	var ci struct {
		Provider string  `json:"provider"`
		Ready    bool    `json:"ready"`
		Runs     []CIRun `json:"runs"`
	}
	if !res.State.GetProject(pid, "public.ci", &ci) || ci.Provider != selfID || !ci.Ready || len(ci.Runs) != 2 || ci.Runs[0].Workflow != "Build" || ci.Runs[0].Commit == "" {
		t.Fatalf("public.ci %+v", ci)
	}
}

// Each product's job is shared as a build of that product, so iOS and
// Android each get their own history. A run that builds no product is
// shared whole.
func TestEachProductsJobIsSharedAsItsOwnBuild(t *testing.T) {
	a := answers("142 completed failure 5m", "141 completed success 2h", "140 completed success 3h")
	a["GET "+base+"/runs/14000/jobs"] = jobsOf("Lint success")
	fake(t, a)
	req := request(plugin.HookSchedule, pid)
	req.Projects[0].Products = append(req.Projects[0].Products, plugin.ProductInfo{Key: "android", Name: "Android app", Type: "app", On: true})
	res := plugintest.Call(t, handle, req)
	var ci struct {
		Runs []CIRun `json:"runs"`
	}
	res.State.GetProject(pid, "public.ci", &ci)
	got := []string{}
	for _, c := range ci.Runs {
		got = append(got, fmt.Sprintf("%d %s %s %s %s", c.Number, c.Product, c.Status, c.Trigger, c.URL))
	}
	want := []string{
		"142 ios failed push https://github.com/job1",
		"142 android passed push https://github.com/job2",
		"141 ios passed push https://github.com/job1",
		"141 android passed push https://github.com/job2",
		"140  passed push https://github.com/acme/game/actions/runs/14000",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("public.ci runs:\n%s", strings.Join(got, "\n"))
	}
	if c := ci.Runs[0]; c.ID == ci.Runs[1].ID || c.Started.IsZero() || c.Finished.IsZero() || c.Commit == "" {
		t.Fatalf("run %+v", c)
	}
}

func TestAFinishedRunsJobsAreReadOnce(t *testing.T) {
	sent := fake(t, answers("142 completed failure 5m"))
	_, s := read(t, request(plugin.HookSchedule, pid))
	req := request(plugin.HookSchedule, pid)
	req.State.PutProject(pid, keyStatus, s)
	*sent = nil
	read(t, req)
	for _, c := range *sent {
		if strings.Contains(c, "/jobs") {
			t.Fatalf("asked for jobs again: %v", *sent)
		}
	}
}

// watching is a project request that has looked before, an hour ago.
func watching(t *testing.T, before *Status) *plugin.Request {
	req := request(plugin.HookSchedule, pid)
	if before == nil {
		before = &Status{}
	}
	before.Since = plugintest.Now.Add(-time.Hour)
	req.State.PutProject(pid, keyStatus, before)
	return req
}

func params(op plugin.Op) map[string]any {
	raw, _ := json.Marshal(op.Params)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return m
}

func TestTheFirstLookFilesNothing(t *testing.T) {
	fake(t, answers("142 completed failure 5m"))
	res, s := read(t, request(plugin.HookSchedule, pid))
	if len(res.Ops) != 0 || s.Since.IsZero() {
		t.Fatalf("ops %+v", res.Ops)
	}
}

func TestAFailedJobFilesABugOnItsProduct(t *testing.T) {
	fake(t, answers("142 completed failure 5m", "141 completed success 2h"))
	res, s := read(t, watching(t, nil))
	if len(res.Ops) != 1 || res.Ops[0].Kind != "work.create" {
		t.Fatalf("ops %+v", res.Ops)
	}
	p := params(res.Ops[0])
	body, _ := p["body"].(string)
	if p["product"] != "ios" || p["doer"] != "agent" || !strings.Contains(p["title"].(string), "Build (iOS) in Build on main") {
		t.Fatalf("params %+v", p)
	}
	for _, want := range []string{"Unity build", "Board.cs:44: error CS0246", "Build failed with 1 error", "`abcdef1`"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "Node 16") || strings.Contains(body, "2026-03-02T08") {
		t.Fatalf("body has warnings or timestamps:\n%s", body)
	}
	if len(s.Bugs) != 1 {
		t.Fatalf("bugs %+v", s.Bugs)
	}
}

func TestAFixedBugReRunsTheFailedJobs(t *testing.T) {
	sent := fake(t, answers("142 completed failure 5m"))
	before := &Status{Bugs: map[string]*Bug{"Build|Build (iOS)|main": {Workflow: "Build", Job: "Build (iOS)", Branch: "main", Title: "x", RunID: "14200", Run: 142, WorkID: "w9", At: plugintest.Now.Add(-time.Hour)}}}
	_ = json.Unmarshal([]byte(`[{"id":"14200","number":142,"workflow":"Build","branch":"main","status":"failed","jobs":[{"id":1,"name":"Build (iOS)","status":"failed"}]}]`), &before.Runs)
	req := watching(t, before)
	req.Work = []plugin.Work{{ID: "w9", Key: "ACM-9", ProjectID: pid, Title: "x", StageKind: "done", Tags: []string{"bug", "ci"}}}
	res, s := read(t, req)
	if !strings.Contains(strings.Join(*sent, "\n"), "POST "+base+"/runs/14200/rerun-failed-jobs") {
		t.Fatalf("no re-run: %v", *sent)
	}
	if s.Requests["fixed-ACM-9"].State != "started" || len(s.Bugs) != 0 || len(res.Ops) != 0 {
		t.Fatalf("requests %+v bugs %+v ops %+v", s.Requests, s.Bugs, res.Ops)
	}
}

func TestAProductShowsItsOwnJobWithoutCallingGitHub(t *testing.T) {
	fake(t, answers("142 completed failure 5m"))
	_, s := read(t, request(plugin.HookSchedule, pid))
	sent := fake(t, nil)
	for _, scope := range []string{ios, mac} {
		req := request(plugin.HookRender, scope)
		req.Slots = []string{"project_card:ci"}
		req.State.PutProject(pid, keyStatus, s)
		res := plugintest.Call(t, handle, req)
		card := res.Cards["project_card:ci"]
		switch {
		case scope == ios && (card[0].Label != "Build (iOS)" || card[0].Value != "Failed"):
			t.Fatalf("ios card %+v", card)
		case scope == mac && card[0].Value != "Not in the workflow yet":
			t.Fatalf("mac card %+v", card)
		}
		sched := request(plugin.HookSchedule, scope)
		sched.State.PutProject(pid, keyStatus, s)
		plugintest.Call(t, handle, sched)
	}
	if len(*sent) != 0 {
		t.Fatalf("a product called GitHub: %v", *sent)
	}
}

func TestRunStartsTheWorkflowOnItsBranch(t *testing.T) {
	sent := fake(t, answers("142 completed failure 5m"))
	req := request(plugin.HookAction, pid)
	req.Action = &plugin.Action{Name: "start"}
	res := plugintest.Call(t, handle, req)
	if !strings.Contains(strings.Join(*sent, "\n"), `POST `+base+`/workflows/build.yml/dispatches {"ref":"main"}`) || !strings.HasPrefix(res.Toast, "Started") {
		t.Fatalf("toast %q sent %v", res.Toast, *sent)
	}
}

func TestPickedAsTheAppsBuilderItStartsTheStoreBuild(t *testing.T) {
	sent := fake(t, answers("142 completed failure 5m"))
	_, s := read(t, request(plugin.HookSchedule, pid))
	req := request(plugin.HookSchedule, ios)
	req.State.PutProject(pid, keyStatus, s)
	publish(req, "app", ios, "public.app", map[string]any{"builder": map[string]string{"id": selfID}})
	publish(req, "app", ios, "public.build_request", Request{ID: "testflight-1", At: plugintest.Now.Add(-time.Minute)})
	*sent = nil
	res := plugintest.Call(t, handle, req)
	if len(*sent) != 1 || !strings.Contains((*sent)[0], "/workflows/build.yml/dispatches") {
		t.Fatalf("sent %v", *sent)
	}
	var b struct {
		Ready    bool              `json:"ready"`
		Requests map[string]Answer `json:"requests"`
	}
	if !res.State.GetProject(ios, "public.builder", &b) || !b.Ready || b.Requests["testflight-1"].State != "started" {
		t.Fatalf("builder %+v", b)
	}
}

func TestAWebhookForTheRepoChecksAtOnce(t *testing.T) {
	for _, tc := range []struct {
		repo, event string
		reads       bool
	}{{repo, "workflow_job", true}, {"other/repo", "workflow_job", false}, {repo, "push", false}} {
		sent := fake(t, answers("142 completed failure 5m"))
		req := request(plugin.HookWebhook, pid)
		req.Webhook = &plugin.Webhook{On: true, Delivery: &plugin.Delivery{Event: tc.event, Body: json.RawMessage(`{"repository":{"full_name":"` + tc.repo + `"}}`)}}
		plugintest.Call(t, handle, req)
		if (len(*sent) > 0) != tc.reads {
			t.Fatalf("%+v: sent %v", tc, *sent)
		}
	}
}

func TestJobsMatchProductsByPlatform(t *testing.T) {
	for _, tc := range []struct {
		product, name, job string
		want               bool
	}{
		{"ios", "iPhone app", "Build (iOS)", true},
		{"android", "Android app", "Build for Android", true},
		{"steam", "Steam", "Build (StandaloneWindows64)", true},
		{"mac", "Mac app", "Build (StandaloneOSX)", true},
		{"mac", "Mac app", "Build (iOS)", false},
		{"ios", "iPhone app", "Story snapshot", false},
	} {
		pl := platform(plugin.Project{Product: tc.product, ProductName: tc.name})
		if got := pl != nil && names(tc.job, pl); got != tc.want {
			t.Errorf("%s / %s: got %v", tc.name, tc.job, got)
		}
	}
}
