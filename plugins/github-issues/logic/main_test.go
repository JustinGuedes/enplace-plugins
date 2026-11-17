package main

import (
	"encoding/json"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

// fakeGitHub is a repo's issues API, started from a recorded answer
// (testdata/issues.json), that remembers changes made through it.
type fakeGitHub struct {
	t      *testing.T
	issues map[int]map[string]any
	now    time.Time
	calls  []string
	// writes answer with this status instead, like a read-only token.
	writeStatus int
	// login is who the token belongs to.
	login string
}

func newFake(t *testing.T, now time.Time) *fakeGitHub {
	raw, err := os.ReadFile("testdata/issues.json")
	if err != nil {
		t.Fatal(err)
	}
	var list []map[string]any
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	f := &fakeGitHub{t: t, issues: map[int]map[string]any{}, now: now}
	for _, is := range list {
		f.issues[int(is["number"].(float64))] = is
	}
	return f
}

func (f *fakeGitHub) fetch(r plugin.FetchRequest) (*plugin.FetchResponse, error) {
	u, _ := url.Parse(r.URL)
	method := r.Method
	if method == "" {
		method = "GET"
	}
	f.calls = append(f.calls, method+" "+strings.TrimPrefix(r.URL, api))
	ok := func(v any) (*plugin.FetchResponse, error) {
		b, _ := json.Marshal(v)
		return &plugin.FetchResponse{Status: 200, Body: b}, nil
	}
	if u.Path == "/user" {
		if f.login == "" {
			return &plugin.FetchResponse{Status: 401}, nil
		}
		return ok(map[string]any{"login": f.login})
	}
	if !strings.HasPrefix(u.Path, "/repos/acme/app/issues") {
		return &plugin.FetchResponse{Status: 404}, nil
	}
	if method != "GET" && f.writeStatus != 0 {
		return &plugin.FetchResponse{Status: f.writeStatus}, nil
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(u.Path, "/repos/acme/app/issues"), "/")
	var body map[string]any
	if len(r.Body) > 0 {
		_ = json.Unmarshal(r.Body, &body)
	}
	switch {
	case rest == "" && method == "GET":
		q := u.Query()
		var since time.Time
		if s := q.Get("since"); s != "" {
			since, _ = time.Parse(time.RFC3339, s)
		}
		out := []map[string]any{}
		for n := 1; n < 1000; n++ {
			is := f.issues[n]
			if is == nil || is["state"] != q.Get("state") {
				continue
			}
			up, _ := time.Parse(time.RFC3339, is["updated_at"].(string))
			if up.Before(since) {
				continue
			}
			if want := q.Get("labels"); want != "" && !hasLabel(is, want) {
				continue
			}
			out = append(out, is)
		}
		if q.Get("page") != "1" {
			out = nil
		}
		return ok(out)
	case rest == "" && method == "POST":
		n := 100 + len(f.issues)
		is := map[string]any{"number": float64(n), "html_url": "https://github.com/acme/app/issues/" + strconv.Itoa(n), "state": "open", "updated_at": f.now.Format(time.RFC3339)}
		for k, v := range body {
			is[k] = v
		}
		is["labels"] = labelsOf(body["labels"])
		f.issues[n] = is
		return &plugin.FetchResponse{Status: 201, Body: mustJSON(is)}, nil
	}
	n, _ := strconv.Atoi(rest)
	is := f.issues[n]
	if is == nil {
		return &plugin.FetchResponse{Status: 404}, nil
	}
	if method == "PATCH" {
		for k, v := range body {
			switch k {
			case "labels":
				v = labelsOf(v)
			case "assignees":
				// Only collaborators can be assigned; the rest are dropped.
				is["assignee"] = nil
				for _, l := range v.([]any) {
					if l == "JustinGuedes" || l == "sam" {
						is["assignee"] = map[string]any{"login": l}
					}
				}
				continue
			}
			is[k] = v
		}
		is["updated_at"] = f.now.Format(time.RFC3339)
	}
	return ok(is)
}

func hasLabel(is map[string]any, name string) bool {
	ls, _ := is["labels"].([]any)
	for _, l := range ls {
		if m, _ := l.(map[string]any); m["name"] == name {
			return true
		}
	}
	return false
}

func labelsOf(v any) []any {
	out := []any{}
	ls, _ := v.([]any)
	for _, l := range ls {
		out = append(out, map[string]any{"name": l})
	}
	return out
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func (f *fakeGitHub) patches() []string {
	var out []string
	for _, c := range f.calls {
		if !strings.HasPrefix(c, "GET ") {
			out = append(out, c)
		}
	}
	return out
}

var t0 = time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)

var stages = []plugin.Stage{{Key: "todo", Name: "To do", Kind: "todo"}, {Key: "doing", Name: "Doing", Kind: "doing"}, {Key: "review", Name: "In review", Kind: "review"}, {Key: "done", Name: "Done", Kind: "done"}}

// world is one project with the plugin on, carried from call to call the
// way Enplace carries state.
type world struct {
	t        *testing.T
	gh       *fakeGitHub
	state    plugin.State
	settings map[string]any
	secrets  map[string]string
	work     []plugin.Work
	now      time.Time
}

func newWorld(t *testing.T) *world {
	// These tests bring issues straight in; the tray has its own test.
	w := &world{t: t, gh: newFake(t, t0), settings: map[string]any{"new_issues": "Add automatically"}, now: t0}
	w.state = plugin.State{Projects: map[string]map[string]json.RawMessage{}}
	return w
}

func (w *world) request() *plugin.Request {
	// The GitHub plugin is on the project with its repo.
	peer := plugin.State{Projects: map[string]map[string]json.RawMessage{"p1": {"public.code": json.RawMessage(`{"provider":"github","repo":"acme/app"}`)}}}
	return &plugin.Request{Hook: plugin.HookSchedule, Now: w.now, ProjectID: "p1", Plugin: plugin.Info{ID: "github-issues"},
		Projects:        []plugin.Project{{ID: "p1", Key: "ACM", On: true, Stages: stages}},
		ProjectSettings: map[string]map[string]any{"p1": w.settings}, State: w.state, Work: w.work, Secrets: w.secrets,
		Peers: map[string]plugin.State{"github": peer}}
}

func (w *world) sync() *plugin.Response {
	w.t.Helper()
	w.gh.now = w.now
	res, err := run(w.request(), &source{fetch: w.gh.fetch})
	if err != nil {
		w.t.Fatal(err)
	}
	if res.State != nil {
		for pid, kv := range res.State.Projects {
			if w.state.Projects[pid] == nil {
				w.state.Projects[pid] = map[string]json.RawMessage{}
			}
			for k, v := range kv {
				w.state.Projects[pid][k] = v
			}
		}
	}
	return res
}

func ops(res *plugin.Response, kind string) []map[string]any {
	var out []map[string]any
	for _, op := range res.Ops {
		if op.Kind == kind {
			var m map[string]any
			_ = json.Unmarshal(mustJSON(op.Params), &m)
			out = append(out, m)
		}
	}
	return out
}

func status(res *plugin.Response) string { return res.Status["p1"].Text }

// approve turns the first import into work, as if the person approved it.
func (w *world) approve(res *plugin.Response) {
	w.t.Helper()
	imp := ops(res, "work.import")
	if len(imp) != 1 {
		w.t.Fatalf("want one import, got %+v", res.Ops)
	}
	for i, x := range imp[0]["items"].([]any) {
		in := x.(map[string]any)
		wk := plugin.Work{ID: "w" + strconv.Itoa(i+1), Key: "ACM-" + strconv.Itoa(i+1), ProjectID: "p1", Title: in["title"].(string), Stage: in["stage"].(string), UpdatedAt: w.now,
			Fields: map[string]string{"github-issues.link": in["fields"].(map[string]any)["github-issues.link"].(string)}}
		if b, ok := in["body"].(string); ok {
			wk.Body = b
		}
		if a, ok := in["assignee"].(string); ok {
			wk.Assignee = a
		}
		for _, t := range in["tags"].([]any) {
			wk.Tags = append(wk.Tags, t.(string))
		}
		w.work = append(w.work, wk)
	}
}

func (w *world) at(d time.Duration) { w.now = t0.Add(d) }

func TestIssuesComeInWithoutSetup(t *testing.T) {
	w := newWorld(t)
	w.settings["stages"] = "in-review = In review"
	res := w.sync()
	imp := ops(res, "work.import")
	if len(imp) != 1 {
		t.Fatalf("ops %+v, status %q", res.Ops, status(res))
	}
	items := imp[0]["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("the pull request came in, or an issue didn't: %+v", items)
	}
	first, second := items[0].(map[string]any), items[1].(map[string]any)
	if first["title"] != "Crash when opening a shared list" || first["stage"] != "todo" || first["fields"].(map[string]any)["github-issues.link"] != "#12" {
		t.Fatalf("first %+v", first)
	}
	if tags := first["tags"].([]any); len(tags) != 1 || tags[0] != "bug" {
		t.Fatalf("labels should be tags: %+v", first)
	}
	if second["stage"] != "review" || len(second["tags"].([]any)) != 1 {
		t.Fatalf("a mapped label places the issue and isn't a tag: %+v", second)
	}
	for _, c := range w.gh.calls {
		if strings.Contains(c, "/pulls") || !strings.HasPrefix(c, "GET /repos/acme/app/issues") {
			t.Fatalf("unexpected call %s", c)
		}
	}
}

func TestTwoWay(t *testing.T) {
	w := newWorld(t)
	w.approve(w.sync())
	w.at(time.Minute)
	if res := w.sync(); len(res.Ops) != 0 || len(w.gh.patches()) != 0 {
		t.Fatalf("nothing changed but ops %+v calls %v", res.Ops, w.gh.patches())
	}

	// Moved to Doing here: the issue stays open, and the work stays in
	// Doing on the next run.
	w.at(2 * time.Minute)
	w.work[0].Stage, w.work[0].UpdatedAt = "doing", w.now
	w.sync()
	w.at(3 * time.Minute)
	if res := w.sync(); len(res.Ops) != 0 || len(w.gh.patches()) != 0 {
		t.Fatalf("Doing bounced: ops %+v calls %v", res.Ops, w.gh.patches())
	}

	// Closed on GitHub: done here in one run.
	w.at(5 * time.Minute)
	w.gh.issues[12]["state"], w.gh.issues[12]["state_reason"], w.gh.issues[12]["updated_at"] = "closed", "completed", w.now.Format(time.RFC3339)
	w.at(6 * time.Minute)
	res := w.sync()
	if mv := ops(res, "work.move"); len(mv) != 1 || mv[0]["id"] != "w1" || mv[0]["stage"] != "done" {
		t.Fatalf("close didn't move the work: %+v", res.Ops)
	}
	w.work[0].Stage, w.work[0].UpdatedAt = "done", w.now

	// Reopened here: the issue opens again, even though it's no longer in
	// the open list.
	w.at(10 * time.Minute)
	w.work[0].Stage, w.work[0].UpdatedAt = "todo", w.now
	w.sync()
	if w.gh.issues[12]["state"] != "open" {
		t.Fatalf("reopen: %v", w.gh.patches())
	}

	// Marked done here: the issue closes as completed.
	w.at(12 * time.Minute)
	w.work[1].Stage, w.work[1].UpdatedAt = "done", w.now
	w.sync()
	if is := w.gh.issues[14]; is["state"] != "closed" || is["state_reason"] != "completed" {
		t.Fatalf("done here didn't close #14: %v", w.gh.patches())
	}

	// A title edited here goes out; tags go out as labels, keeping a
	// mapped stage label off.
	w.at(15 * time.Minute)
	w.work[0].Title, w.work[0].Tags, w.work[0].UpdatedAt = "Crash on shared lists", []string{"bug", "ios"}, w.now
	w.sync()
	if is := w.gh.issues[12]; is["title"] != "Crash on shared lists" || !hasLabel(is, "ios") || !hasLabel(is, "bug") {
		t.Fatalf("edit didn't go out: %+v", is)
	}
}

func TestCreateIssuesIsOffByDefault(t *testing.T) {
	w := newWorld(t)
	w.approve(w.sync())
	w.at(time.Minute)
	w.sync()
	mine := plugin.Work{ID: "w9", Key: "ACM-9", ProjectID: "p1", Title: "Private plan", Stage: "todo", CreatedAt: w.now.Add(time.Minute), UpdatedAt: w.now.Add(time.Minute)}
	w.work = append(w.work, mine)
	w.at(2 * time.Minute)
	w.sync()
	for _, c := range w.gh.patches() {
		if strings.HasPrefix(c, "POST") {
			t.Fatalf("made an issue with the toggle off: %v", c)
		}
	}

	// Switched on (with a label filter): new work becomes an issue with
	// the filter's label, and the work gets its link.
	w.settings["create_outside"] = true
	w.settings["labels"] = "enplace"
	w.at(3 * time.Minute)
	res := w.sync()
	f := ops(res, "work.fields")
	if len(f) != 1 || f[0]["id"] != "w9" {
		t.Fatalf("no link for the new issue: %+v (calls %v)", res.Ops, w.gh.patches())
	}
	var made map[string]any
	for _, is := range w.gh.issues {
		if is["title"] == "Private plan" {
			made = is
		}
	}
	if made == nil || !hasLabel(made, "enplace") {
		t.Fatalf("issue %+v", made)
	}
}

func TestRefusedWriteNamesThePermission(t *testing.T) {
	w := newWorld(t)
	w.secrets = map[string]string{"token": "read-only"}
	w.approve(w.sync())
	w.at(time.Minute)
	w.sync()
	w.gh.writeStatus = 403
	w.at(2 * time.Minute)
	w.work[0].Stage, w.work[0].UpdatedAt = "done", w.now
	res := w.sync()
	if !strings.Contains(status(res), "Issues read and write") {
		t.Fatalf("status %q", status(res))
	}
}

func TestOwnRepoAndNoRepo(t *testing.T) {
	if got := cleanRepo("https://github.com/Acme/App.git"); got != "Acme/App" {
		t.Fatalf("link: %q", got)
	}
	w := newWorld(t)
	req := w.request()
	req.Peers = nil
	res, err := run(req, &source{fetch: w.gh.fetch})
	if err != nil || len(res.Ops) != 0 || len(w.gh.calls) != 0 || strings.Contains(status(res), "stopped") {
		t.Fatalf("with no repo it should wait quietly: %v %+v %q", err, res.Ops, status(res))
	}
}

func (w *world) action(name string, args any) *plugin.Response {
	w.t.Helper()
	req := w.request()
	req.Hook = plugin.HookAction
	req.Action = &plugin.Action{Name: name, Args: mustJSON(args)}
	res, err := run(req, &source{fetch: w.gh.fetch})
	if err != nil {
		w.t.Fatal(err)
	}
	w.keep(res)
	return res
}

func (w *world) keep(res *plugin.Response) {
	if res.State == nil {
		return
	}
	for pid, kv := range res.State.Projects {
		if w.state.Projects[pid] == nil {
			w.state.Projects[pid] = map[string]json.RawMessage{}
		}
		for k, v := range kv {
			w.state.Projects[pid][k] = v
		}
	}
}

func (w *world) render(slot string, pid string) *plugin.Response {
	w.t.Helper()
	req := w.request()
	req.Hook, req.Slots, req.ProjectID = plugin.HookRender, []string{slot}, pid
	req.Projects[0].Name = "Open Wide"
	res, err := run(req, &source{fetch: w.gh.fetch})
	if err != nil {
		w.t.Fatal(err)
	}
	return res
}

func (w *world) addIssue(n int, by string, labels ...string) {
	ls := []any{}
	for _, l := range labels {
		ls = append(ls, map[string]any{"name": l})
	}
	w.gh.issues[n] = map[string]any{"number": float64(n), "title": "Issue " + strconv.Itoa(n), "state": "open", "user": map[string]any{"login": by},
		"labels": ls, "html_url": "https://github.com/acme/app/issues/" + strconv.Itoa(n), "updated_at": w.now.Format(time.RFC3339)}
}

func TestTray(t *testing.T) {
	w := newWorld(t)
	delete(w.settings, "new_issues")
	w.secrets = map[string]string{"token": "tok"}
	w.gh.login = "JustinGuedes"

	// #12 is someone else's, so it waits; #14 is the token owner's, so it
	// comes straight in.
	res := w.sync()
	imp := ops(res, "work.import")
	if len(imp) != 1 || len(imp[0]["items"].([]any)) != 1 || imp[0]["items"].([]any)[0].(map[string]any)["title"] != "Dark icon" {
		t.Fatalf("only your own issue comes in: %+v", res.Ops)
	}
	if !strings.Contains(status(res), "1 waiting") {
		t.Fatalf("status %q", status(res))
	}
	w.approve(res)

	// The tray shows it with Accept and Skip; the brief says so.
	card := w.render("project_card:tray", "p1").Cards["project_card:tray"]
	var sec *plugin.Block
	for i := range card {
		if card[i].Type == plugin.BlockSection {
			sec = &card[i]
		}
	}
	if sec == nil || sec.Label != "#12" || sec.Text != "Crash when opening a shared list" || !strings.Contains(sec.Sub, "byron-k") || len(sec.Buttons) != 2 || sec.Buttons[0].Action != "sync.bring" {
		t.Fatalf("tray %+v", card)
	}
	if b := w.render(plugin.SlotBrief, "").Brief; b == nil || b.Text != "1 issue from GitHub Issues is waiting on Open Wide." || b.Button.Href != "/p/acm/work" {
		t.Fatalf("brief %+v", b)
	}

	// Asking GitHub who the token is happens once.
	w.at(time.Minute)
	w.gh.calls = nil
	w.sync()
	for _, c := range w.gh.calls {
		if strings.Contains(c, "/user") {
			t.Fatalf("asked who the token is again: %v", w.gh.calls)
		}
	}

	// Accept: one work.create with the link.
	w.at(2 * time.Minute)
	res = w.action("sync.bring", map[string]any{"ids": []string{"12"}})
	if c := ops(res, "work.create"); len(c) != 1 || c[0]["fields"].(map[string]any)["github-issues.link"] != "#12" {
		t.Fatalf("accept: %+v", res.Ops)
	}
	if !res.Ops[0].Asked {
		t.Fatal("Accept is the person's approval: the op should be marked asked")
	}

	// Skip: off the tray, and back once it changes on GitHub.
	w.addIssue(20, "someone")
	w.at(3 * time.Minute)
	if res := w.sync(); !strings.Contains(status(res), "1 waiting") {
		t.Fatalf("status %q", status(res))
	}
	w.action("sync.pass", map[string]any{"ids": []string{"20"}})
	w.at(4 * time.Minute)
	if res := w.sync(); strings.Contains(status(res), "waiting") || len(res.Ops) != 0 {
		t.Fatalf("skipped came back unchanged: %q %+v", status(res), res.Ops)
	}
	w.gh.issues[20]["updated_at"] = t0.Add(5 * time.Minute).Format(time.RFC3339)
	w.at(6 * time.Minute)
	if res := w.sync(); !strings.Contains(status(res), "1 waiting") {
		t.Fatalf("changed after a skip, it should be back: %q", status(res))
	}

	// A "bring straight in" label skips the tray.
	w.settings["bring_labels"] = "enplace"
	w.addIssue(21, "someone", "enplace")
	w.at(7 * time.Minute)
	if c := ops(w.sync(), "work.create"); len(c) != 1 || c[0]["title"] != "Issue 21" {
		t.Fatal("a labelled issue should come straight in")
	}

	// Closed on GitHub while it waits: off the tray.
	w.gh.issues[20]["state"], w.gh.issues[20]["updated_at"] = "closed", t0.Add(8*time.Minute).Format(time.RFC3339)
	w.at(9 * time.Minute)
	if res := w.sync(); strings.Contains(status(res), "waiting") {
		t.Fatalf("closed issue still waits: %q", status(res))
	}

	// Ask me in Approvals: no tray; the new issue is filed as work, which
	// the project's permission sends to Approvals.
	w.settings["new_issues"] = "Ask me in Approvals"
	w.addIssue(22, "someone")
	w.at(10 * time.Minute)
	res = w.sync()
	if c := ops(res, "work.create"); len(c) != 1 || c[0]["title"] != "Issue 22" || strings.Contains(status(res), "waiting") || res.Ops[0].Asked {
		t.Fatalf("ask mode: %q %+v", status(res), res.Ops)
	}
}

// The assignee comes in as a login and goes out as one; a name GitHub
// can't assign leaves the issue as it was.
func TestAssignee(t *testing.T) {
	w := newWorld(t)
	w.approve(w.sync())
	if w.work[1].Assignee != "@JustinGuedes" {
		t.Fatalf("came in as %q", w.work[1].Assignee)
	}
	w.at(time.Minute)
	w.sync()

	w.at(2 * time.Minute)
	w.work[1].Assignee, w.work[1].UpdatedAt = "sam", w.now
	w.sync()
	if a := w.gh.issues[14]["assignee"].(map[string]any); a["login"] != "sam" {
		t.Fatalf("went out as %+v", a)
	}

	w.at(4 * time.Minute)
	w.work[1].Assignee, w.work[1].UpdatedAt = "Byron Smith", w.now
	w.sync()
	if a := w.gh.issues[14]["assignee"].(map[string]any); a["login"] != "sam" {
		t.Fatalf("a name changed it: %+v", a)
	}
	w.at(6 * time.Minute)
	if res := w.sync(); len(ops(res, "work.update")) != 0 {
		t.Fatalf("GitHub's assignee came back over the pick: %+v", res.Ops)
	}

	w.at(8 * time.Minute)
	w.work[1].Assignee, w.work[1].UpdatedAt = "", w.now
	w.sync()
	if a := w.gh.issues[14]["assignee"]; a != nil {
		t.Fatalf("unassigning left %+v", a)
	}
}
