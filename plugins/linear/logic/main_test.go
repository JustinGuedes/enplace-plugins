package main

import (
	"encoding/json"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

// fakeLinear is Linear's GraphQL API, started from recorded answers
// (testdata/), that remembers changes made through it.
type fakeLinear struct {
	t      *testing.T
	key    string
	team   map[string]any
	issues []map[string]any
	now    time.Time
	calls  []string // operation names
	vars   []map[string]any
}

func readJSON(t *testing.T, name string, v any) {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatal(err)
	}
}

func newFake(t *testing.T) *fakeLinear {
	f := &fakeLinear{t: t, key: "lin_api_good", now: t0}
	var team map[string]any
	readJSON(t, "team.json", &team)
	f.team = team
	var list struct {
		Data struct {
			Issues struct {
				Nodes []map[string]any `json:"nodes"`
			} `json:"issues"`
		} `json:"data"`
	}
	readJSON(t, "issues.json", &list)
	f.issues = list.Data.Issues.Nodes
	return f
}

func (f *fakeLinear) issue(id string) map[string]any {
	for _, is := range f.issues {
		if is["id"] == id {
			return is
		}
	}
	return nil
}

func (f *fakeLinear) byRef(ref string) map[string]any {
	for _, is := range f.issues {
		if is["identifier"] == ref {
			return is
		}
	}
	return nil
}

func (f *fakeLinear) touch(is map[string]any) { is["updatedAt"] = f.now.Format(time.RFC3339) }

func (f *fakeLinear) stateByID(id string) map[string]any {
	for _, st := range f.team["data"].(map[string]any)["team"].(map[string]any)["states"].(map[string]any)["nodes"].([]any) {
		if m := st.(map[string]any); m["id"] == id {
			return m
		}
	}
	return nil
}

func (f *fakeLinear) labelsByID(ids []any) map[string]any {
	all := map[string]string{"l-bug": "Bug", "l-ios": "iOS", "l-feature": "Feature"}
	nodes := []any{}
	for _, id := range ids {
		name := all[id.(string)]
		if name == "" {
			name = strings.TrimPrefix(id.(string), "l-new-")
		}
		nodes = append(nodes, map[string]any{"id": id, "name": name})
	}
	return map[string]any{"nodes": nodes}
}

func (f *fakeLinear) fetch(r plugin.FetchRequest) (*plugin.FetchResponse, error) {
	if r.URL != api || r.Method != "POST" {
		f.t.Fatalf("unexpected fetch %s %s", r.Method, r.URL)
	}
	var body struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	_ = json.Unmarshal(r.Body, &body)
	op := strings.FieldsFunc(body.Query, func(r rune) bool { return r == ' ' || r == '(' || r == '{' || r == '\n' })[1]
	f.calls = append(f.calls, op)
	f.vars = append(f.vars, body.Variables)
	file := func(name string) (*plugin.FetchResponse, error) {
		raw, _ := os.ReadFile("testdata/" + name)
		return &plugin.FetchResponse{Status: 200, Body: raw}, nil
	}
	ok := func(v any) (*plugin.FetchResponse, error) {
		b, _ := json.Marshal(map[string]any{"data": v})
		return &plugin.FetchResponse{Status: 200, Body: b}, nil
	}
	if r.Headers["Authorization"] != f.key {
		// What Linear answers for a bad key: 400 with an authentication error.
		raw, _ := os.ReadFile("testdata/auth_error.json")
		return &plugin.FetchResponse{Status: 400, Body: raw}, nil
	}
	switch op {
	case "Teams":
		return file("teams.json")
	case "Team":
		return ok(f.team["data"])
	case "Issues":
		var out []any
		for _, is := range f.issues {
			if f.matches(is, body.Variables["filter"].(map[string]any)) {
				out = append(out, is)
			}
		}
		return ok(map[string]any{"issues": map[string]any{"nodes": out, "pageInfo": map[string]any{"hasNextPage": false}}})
	case "Update":
		is := f.issue(body.Variables["id"].(string))
		if is == nil {
			return ok(map[string]any{"issueUpdate": map[string]any{"success": false}})
		}
		f.apply(is, body.Variables["input"].(map[string]any))
		return ok(map[string]any{"issueUpdate": map[string]any{"success": true, "issue": is}})
	case "Create":
		in := body.Variables["input"].(map[string]any)
		n := strconv.Itoa(len(f.issues) + 1)
		is := map[string]any{"id": "i-" + n, "identifier": "ENG-" + n, "url": "https://linear.app/acme/issue/ENG-" + n, "title": "", "description": "", "priority": 0,
			"state": f.stateByID("s-todo"), "labels": map[string]any{"nodes": []any{}}, "comments": map[string]any{"nodes": []any{}}}
		f.apply(is, in)
		f.issues = append(f.issues, is)
		return ok(map[string]any{"issueCreate": map[string]any{"success": true, "issue": is}})
	case "Users":
		var out []any
		raw, _ := json.Marshal(body.Variables["filter"])
		for _, u := range linearUsers {
			for _, v := range []string{u["email"], u["name"], u["displayName"]} {
				if strings.Contains(strings.ToLower(string(raw)), `"eqignorecase":"`+strings.ToLower(v)+`"`) {
					out = append(out, map[string]any{"id": u["id"]})
					break
				}
			}
		}
		return ok(map[string]any{"users": map[string]any{"nodes": out}})
	case "Label":
		in := body.Variables["input"].(map[string]any)
		return ok(map[string]any{"issueLabelCreate": map[string]any{"success": true, "issueLabel": map[string]any{"id": "l-new-" + in["name"].(string), "name": in["name"]}}})
	}
	f.t.Fatalf("unexpected operation %s", op)
	return nil, nil
}

var linearUsers = []map[string]string{
	{"id": "u-sam", "name": "Sam Rivera", "displayName": "sam", "email": "sam@acme.dev"},
	{"id": "u-justin", "name": "Justin", "displayName": "justin", "email": "justin@acme.dev"},
}

func (f *fakeLinear) apply(is, in map[string]any) {
	for k, v := range in {
		switch k {
		case "stateId":
			is["state"] = f.stateByID(v.(string))
		case "labelIds":
			is["labels"] = f.labelsByID(v.([]any))
		case "assigneeId":
			is["assignee"] = nil
			for _, u := range linearUsers {
				if u["id"] == v {
					is["assignee"] = map[string]any{"name": u["name"], "displayName": u["displayName"], "email": u["email"]}
				}
			}
		case "teamId", "projectId":
		default:
			is[k] = v
		}
	}
	f.touch(is)
}

// matches is the slice of Linear's IssueFilter the plugin uses.
func (f *fakeLinear) matches(is map[string]any, filter map[string]any) bool {
	if team, ok := filter["team"]; ok && team.(map[string]any)["id"].(map[string]any)["eq"] != "t-eng" {
		return false
	}
	if p, ok := filter["project"]; ok {
		want := p.(map[string]any)["id"].(map[string]any)["eq"]
		if want == "p-sync" && is["project"] == nil {
			return false
		}
	}
	if st, ok := filter["state"]; ok {
		nin := st.(map[string]any)["type"].(map[string]any)["nin"].([]any)
		if slices.Contains(nin, is["state"].(map[string]any)["type"]) {
			return false
		}
	}
	if up, ok := filter["updatedAt"]; ok {
		gt, _ := time.Parse(time.RFC3339, up.(map[string]any)["gt"].(string))
		at, _ := time.Parse(time.RFC3339, is["updatedAt"].(string))
		if !at.After(gt) {
			return false
		}
	}
	if id, ok := filter["id"]; ok && !slices.Contains(id.(map[string]any)["in"].([]any), is["id"]) {
		return false
	}
	return true
}

var t0 = time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)

var stages = []plugin.Stage{{Key: "todo", Name: "To do", Kind: "todo"}, {Key: "doing", Name: "Doing", Kind: "doing"}, {Key: "review", Name: "In review", Kind: "review"}, {Key: "done", Name: "Done", Kind: "done"}}

// world is one project with the plugin on, carried from call to call the
// way Enplace carries state.
type world struct {
	t        *testing.T
	lin      *fakeLinear
	state    plugin.State
	settings map[string]any
	secrets  map[string]string
	work     []plugin.Work
	now      time.Time
}

func newWorld(t *testing.T) *world {
	return &world{t: t, lin: newFake(t), settings: map[string]any{}, secrets: map[string]string{"api_key": "lin_api_good"}, now: t0,
		state: plugin.State{Workspace: map[string]json.RawMessage{}, Projects: map[string]map[string]json.RawMessage{}}}
}

func (w *world) request(hook string) *plugin.Request {
	return &plugin.Request{Hook: hook, Now: w.now, ProjectID: "p1", Plugin: plugin.Info{ID: "linear"},
		Projects:        []plugin.Project{{ID: "p1", Key: "ACM", Name: "Acme", On: true, Stages: stages}},
		ProjectSettings: map[string]map[string]any{"p1": w.settings}, State: w.state, Work: w.work, Secrets: w.secrets}
}

func (w *world) call(req *plugin.Request) *plugin.Response {
	w.t.Helper()
	w.lin.now = w.now
	res, err := run(req, &source{fetch: w.lin.fetch})
	if err != nil {
		w.t.Fatal(err)
	}
	if res.State != nil && req.Hook != plugin.HookRender {
		for k, v := range res.State.Workspace {
			w.state.Workspace[k] = v
		}
		for pid, kv := range res.State.Projects {
			if w.state.Projects[pid] == nil {
				w.state.Projects[pid] = map[string]json.RawMessage{}
			}
			for k, v := range kv {
				if string(v) == "null" {
					delete(w.state.Projects[pid], k)
				} else {
					w.state.Projects[pid][k] = v
				}
			}
		}
	}
	return res
}

func (w *world) sync() *plugin.Response { return w.call(w.request(plugin.HookSchedule)) }

func (w *world) act(name string, args any) *plugin.Response {
	req := w.request(plugin.HookAction)
	raw, _ := json.Marshal(args)
	req.Action = &plugin.Action{Name: name, Args: raw}
	return w.call(req)
}

func (w *world) panel(project string) []plugin.Block {
	req := w.request(plugin.HookRender)
	req.ProjectID, req.Slots, req.Secrets = project, []string{slotPanel}, nil
	return w.call(req).Cards[slotPanel]
}

func (w *world) at(d time.Duration) { w.now = t0.Add(d) }

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

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

func text(blocks []plugin.Block) string {
	var b strings.Builder
	for _, x := range blocks {
		b.WriteString(x.Text + " " + x.Sub + "\n")
		for _, it := range x.Items {
			b.WriteString(it.Title + " → " + it.Value + "\n")
		}
		for _, f := range x.Fields {
			b.WriteString(f.Label + ": " + strings.Join(f.Options, "|") + " = " + f.Value + "\n")
		}
	}
	return b.String()
}

// link picks the team, as a person does on the settings panel, and turns
// the first import into work, as if they approved it.
func (w *world) link() *plugin.Response {
	w.t.Helper()
	w.act("linear.check", nil)
	res := w.act("linear.team", map[string]string{"team": "Engineering (ENG)"})
	imp := ops(res, "work.import")
	if len(imp) != 1 {
		w.t.Fatalf("want one import, got %+v (toast %q)", res.Ops, res.Toast)
	}
	for i, x := range imp[0]["items"].([]any) {
		in := x.(map[string]any)
		wk := plugin.Work{ID: "w" + strconv.Itoa(i+1), Key: "ACM-" + strconv.Itoa(i+1), ProjectID: "p1", Title: in["title"].(string), Stage: in["stage"].(string), UpdatedAt: w.now, CreatedAt: w.now, Fields: map[string]string{}}
		for k, v := range in["fields"].(map[string]any) {
			wk.Fields[k] = v.(string)
		}
		if b, ok := in["body"].(string); ok {
			wk.Body = b
		}
		if p, ok := in["priority"].(float64); ok {
			wk.Priority = int(p)
		}
		if d, ok := in["due"].(string); ok {
			wk.Due = d
		}
		if a, ok := in["assignee"].(string); ok {
			wk.Assignee = a
		}
		if tags, ok := in["tags"].([]any); ok {
			for _, t := range tags {
				wk.Tags = append(wk.Tags, t.(string))
			}
		}
		for _, st := range stages {
			if st.Key == wk.Stage {
				wk.StageKind = st.Kind
			}
		}
		w.work = append(w.work, wk)
	}
	return res
}

func TestPanelAsksForAKey(t *testing.T) {
	w := newWorld(t)
	w.secrets = nil
	if got := text(w.panel("p1")); !strings.Contains(got, "Add a Linear personal API key") {
		t.Fatalf("panel %q", got)
	}
	// A schedule run with no key and no team waits quietly.
	res := w.sync()
	if len(res.Ops) != 0 || strings.Contains(res.Status["p1"].Text, "stopped") {
		t.Fatalf("no key: ops %+v status %q", res.Ops, res.Status["p1"].Text)
	}
}

func TestBadKeyIsClear(t *testing.T) {
	w := newWorld(t)
	w.secrets = map[string]string{"api_key": "lin_api_wrong"}
	res := w.act("linear.check", nil)
	if !strings.Contains(res.Toast, "turned the API key down") {
		t.Fatalf("toast %q", res.Toast)
	}
	for _, p := range []string{"", "p1"} {
		blocks := w.panel(p)
		if len(blocks) == 0 || blocks[0].Tone != "warn" || !strings.Contains(blocks[0].Text, "turned the API key down") {
			t.Fatalf("panel %q: %+v", p, blocks)
		}
	}
}

func TestTeamsListedAndFirstImport(t *testing.T) {
	w := newWorld(t)
	// The schedule reads the teams once a key is saved, before any team is picked.
	w.sync()
	if got := text(w.panel("")); !strings.Contains(got, "sam's key sees 2 teams: Engineering (ENG), Design (DES)") {
		t.Fatalf("workspace panel %q", got)
	}
	if got := text(w.panel("p1")); !strings.Contains(got, "Team: |Engineering (ENG)|Design (DES) = ") {
		t.Fatalf("project panel %q", got)
	}

	res := w.link()
	items := ops(res, "work.import")[0]["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("the done issue came in, or an open one didn't: %+v", items)
	}
	one, two, three := items[0].(map[string]any), items[1].(map[string]any), items[2].(map[string]any)
	f1 := one["fields"].(map[string]any)
	if one["title"] != "Offline mode drops edits" || one["stage"] != "doing" || one["priority"] != 4.0 || one["due"] != "2026-11-20" ||
		f1["linear.link"] != "ENG-1" || f1["linear.points"] != "3" || one["tags"].([]any)[0] != "Bug" || !strings.Contains(one["body"].(string), "**reconnect**") {
		t.Fatalf("ENG-1 %+v", one)
	}
	if two["stage"] != "review" || two["priority"] != 2.0 {
		t.Fatalf("In Review should land in review, Linear medium is medium: %+v", two)
	}
	if three["stage"] != "todo" || three["fields"].(map[string]any)["linear.points"] != "1.5" {
		t.Fatalf("Backlog should land in To do: %+v", three)
	}

	// The panel shows how each status lands, and the Linear project picker.
	got := text(w.panel("p1"))
	for _, want := range []string{"Team: Engineering (ENG)|Design (DES) = Engineering (ENG)", "Linear project: All of the team's issues|Sync engine = All of the team's issues",
		"Triage → To do", "In Progress → Doing", "In Review → In review", "Canceled → Done"} {
		if !strings.Contains(got, want) {
			t.Fatalf("panel lacks %q:\n%s", want, got)
		}
	}

	// Approved: the next run sees nothing new, and asks only for what changed.
	w.at(time.Minute)
	res = w.sync()
	if len(res.Ops) != 0 {
		t.Fatalf("nothing changed but %+v", res.Ops)
	}
	last := w.lin.vars[len(w.lin.vars)-1]["filter"].(map[string]any)
	if _, ok := last["updatedAt"]; !ok {
		t.Fatalf("later runs should only read changed issues: %+v", last)
	}

	// The sub-issue's panel names its parent, with an Open link.
	req := w.request(plugin.HookRender)
	req.Slots, req.WorkID, req.ProjectID = []string{plugin.SlotWorkPanel}, "w2", ""
	b := w.call(req).Cards[plugin.SlotWorkPanel]
	if len(b) == 0 || b[0].Label != "ENG-2" || b[0].Value != "In Review" || !strings.Contains(b[0].Sub, "sub-issue of ENG-1") || len(b[0].Buttons) != 1 || !strings.HasPrefix(b[0].Buttons[0].URL, "https://linear.app/") {
		t.Fatalf("panel %+v", b)
	}
}

func TestTwoWay(t *testing.T) {
	w := newWorld(t)
	w.link()

	// Edited in Linear: title and status come in within one run.
	w.at(2 * time.Minute)
	w.lin.now = w.now
	is := w.lin.byRef("ENG-3")
	is["title"], is["state"], is["estimate"] = "Sketch the import flow", w.lin.stateByID("s-progress"), 2.0
	w.lin.touch(is)
	w.at(3 * time.Minute)
	res := w.sync()
	up, mv, fl := ops(res, "work.update"), ops(res, "work.move"), ops(res, "work.fields")
	if len(up) != 1 || up[0]["id"] != "w3" || up[0]["title"] != "Sketch the import flow" {
		t.Fatalf("title didn't come in: %+v", res.Ops)
	}
	if len(mv) != 1 || mv[0]["stage"] != "doing" {
		t.Fatalf("status didn't come in: %+v", res.Ops)
	}
	if len(fl) != 1 || fl[0]["fields"].(map[string]any)["linear.points"] != "2" {
		t.Fatalf("points didn't come in: %+v", res.Ops)
	}
	w.work[2].Title, w.work[2].Stage, w.work[2].StageKind, w.work[2].Fields["linear.points"], w.work[2].UpdatedAt = "Sketch the import flow", "doing", "doing", "2", w.now

	// Edited here: title, stage, priority and tags go out the same way.
	w.at(5 * time.Minute)
	w.work[0].Title, w.work[0].Stage, w.work[0].StageKind, w.work[0].Priority, w.work[0].Tags, w.work[0].UpdatedAt = "Offline edits vanish", "done", "done", 2, []string{"Bug", "sync"}, w.now
	res = w.sync()
	is = w.lin.byRef("ENG-1")
	if is["title"] != "Offline edits vanish" || is["state"].(map[string]any)["name"] != "Done" || is["priority"] != 3.0 {
		t.Fatalf("change didn't go out: %+v (ops %+v)", is, res.Ops)
	}
	if !slices.Contains(w.lin.calls, "Label") || len(is["labels"].(map[string]any)["nodes"].([]any)) != 2 {
		t.Fatalf("a new tag should add the label: calls %v labels %+v", w.lin.calls, is["labels"])
	}
	if res.Status["p1"].Text != "Synced with Linear at 10:05" {
		t.Fatalf("status %q", res.Status["p1"].Text)
	}

	// Moved to In review here: the issue goes to Linear's In Review.
	w.at(7 * time.Minute)
	w.work[2].Stage, w.work[2].StageKind, w.work[2].UpdatedAt = "review", "review", w.now
	w.sync()
	if st := w.lin.byRef("ENG-3")["state"].(map[string]any)["name"]; st != "In Review" {
		t.Fatalf("review went to %v", st)
	}

	// Archived in Linear: done here with a note, nothing deleted.
	w.at(9 * time.Minute)
	w.lin.now = w.now
	is = w.lin.byRef("ENG-2")
	is["archivedAt"] = w.now.Format(time.RFC3339)
	w.lin.touch(is)
	w.at(10 * time.Minute)
	res = w.sync()
	if mv := ops(res, "work.move"); len(mv) != 1 || mv[0]["id"] != "w2" || mv[0]["stage"] != "done" || !strings.Contains(mv[0]["note"].(string), "Nothing was deleted") {
		t.Fatalf("archive: %+v", res.Ops)
	}
}

func TestNewWorkBecomesAnIssue(t *testing.T) {
	w := newWorld(t)
	w.settings["create_outside"] = true
	w.link()
	w.at(4 * time.Minute)
	w.work = append(w.work, plugin.Work{ID: "w9", Key: "ACM-9", ProjectID: "p1", Title: "Write the release notes", Body: "For 1.1", Stage: "todo", StageKind: "todo", Priority: 1, Tags: []string{"iOS"}, CreatedAt: w.now, UpdatedAt: w.now})
	res := w.sync()
	f := ops(res, "work.fields")
	if len(f) != 1 || f[0]["id"] != "w9" || f[0]["fields"].(map[string]any)["linear.link"] != "ENG-5" {
		t.Fatalf("ops %+v", res.Ops)
	}
	is := w.lin.byRef("ENG-5")
	if is["title"] != "Write the release notes" || is["priority"] != 4.0 || is["state"].(map[string]any)["name"] != "Todo" || is["labels"].(map[string]any)["nodes"].([]any)[0].(map[string]any)["id"] != "l-ios" {
		t.Fatalf("issue %+v", is)
	}
	i := slices.Index(w.lin.calls, "Create")
	if in := w.lin.vars[i]["input"].(map[string]any); in["teamId"] != "t-eng" {
		t.Fatalf("create input %+v", in)
	}
}

func TestProjectFilter(t *testing.T) {
	w := newWorld(t)
	w.act("linear.check", nil)
	w.act("linear.team", map[string]string{"team": "ENG"})
	w.state.Projects["p1"] = map[string]json.RawMessage{keyPick: w.state.Projects["p1"][keyPick], keyTeam: w.state.Projects["p1"][keyTeam]}
	res := w.act("linear.project", map[string]string{"project": "Sync engine"})
	if res.Toast != "Syncing only Sync engine" {
		t.Fatalf("toast %q", res.Toast)
	}
	res = w.sync()
	imp := ops(res, "work.import")
	if len(imp) != 1 || len(imp[0]["items"].([]any)) != 1 {
		t.Fatalf("only ENG-1 is in Sync engine: %+v", res.Ops)
	}
}

func TestAgentToolReadsComments(t *testing.T) {
	w := newWorld(t)
	w.link()
	req := w.request(plugin.HookAction)
	req.ReadOnly, req.Secrets = true, nil
	req.Action = &plugin.Action{Name: "tool_issue", Args: json.RawMessage(`{"work":"ACM-1"}`)}
	res, err := run(req, &source{fetch: func(plugin.FetchRequest) (*plugin.FetchResponse, error) {
		t.Fatal("the tool never calls Linear")
		return nil, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	got := string(mustJSON(res.Data))
	for _, want := range []string{`"issue":"ENG-1"`, `"status":"In Progress"`, `"author":"ada"`, `"body":"Seen on iOS 26 too."`, `"points":"3"`, `"linear_project":"Sync engine"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("tool %s lacks %s", got, want)
		}
	}
	req.Action.Args = json.RawMessage(`{"work":"ACM-77"}`)
	res, _ = run(req, &source{})
	if !strings.Contains(string(mustJSON(res.Data)), "no work item") {
		t.Fatalf("unknown work: %+v", res.Data)
	}
}

func TestPriorityAndKinds(t *testing.T) {
	for l, e := range map[int]int{0: 0, 1: 4, 2: 3, 3: 2, 4: 1} {
		if priority(l) != e || priority(e) != l {
			t.Errorf("priority %d <-> %d", l, e)
		}
	}
	for _, c := range []struct {
		st   state
		want string
	}{{state{Name: "Triage", Type: "triage"}, "todo"}, {state{Name: "In Progress", Type: "started"}, "doing"}, {state{Name: "Code review", Type: "started"}, "review"},
		{state{Name: "Canceled", Type: "canceled"}, "done"}, {state{Name: "Shipped", Type: "completed"}, "done"}} {
		if got := kindOf(c.st); got != c.want {
			t.Errorf("%s: %s, want %s", c.st.Name, got, c.want)
		}
	}
}

// The assignee comes in as Linear has them and goes out by the member's
// email; someone Linear doesn't know leaves the issue as it was.
func TestAssigneeBothWays(t *testing.T) {
	w := newWorld(t)
	w.lin.byRef("ENG-1")["assignee"] = map[string]any{"name": "Sam Rivera", "displayName": "sam", "email": "sam@acme.dev"}
	w.lin.touch(w.lin.byRef("ENG-1"))
	w.link()
	if w.work[0].Assignee != "Sam Rivera <sam@acme.dev>" {
		t.Fatalf("came in as %q", w.work[0].Assignee)
	}
	w.at(time.Minute)
	if res := w.sync(); len(res.Ops) != 0 {
		t.Fatalf("approved work should be in step: %+v", res.Ops)
	}

	w.at(5 * time.Minute)
	w.lin.now = w.now
	w.work[0].Assignee, w.work[0].AssigneeEmail, w.work[0].UpdatedAt = "Justin Guedes", "justin@acme.dev", w.now
	w.sync()
	if a := w.lin.byRef("ENG-1")["assignee"].(map[string]any); a["name"] != "Justin" {
		t.Fatalf("went out as %+v", a)
	}

	w.at(7 * time.Minute)
	w.lin.now = w.now
	w.work[0].Assignee, w.work[0].AssigneeEmail, w.work[0].UpdatedAt = "Byron", "", w.now
	w.sync()
	if a := w.lin.byRef("ENG-1")["assignee"].(map[string]any); a["name"] != "Justin" {
		t.Fatalf("an unknown person changed it: %+v", a)
	}
	w.at(9 * time.Minute)
	if res := w.sync(); len(ops(res, "work.update")) != 0 {
		t.Fatalf("Linear's assignee came back over the pick: %+v", res.Ops)
	}

	w.at(11 * time.Minute)
	w.lin.now = w.now
	w.work[0].Assignee, w.work[0].UpdatedAt = "", w.now
	w.sync()
	if a := w.lin.byRef("ENG-1")["assignee"]; a != nil {
		t.Fatalf("unassigning left %+v", a)
	}
}
