package worksync

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

type memSource struct {
	items   map[string]Item
	updates []Change
	now     time.Time
	// people are the assignees it knows; nil knows everyone.
	people []string
}

func (m *memSource) List(*Sync) ([]Item, bool, error) {
	var out []Item
	for _, it := range m.items {
		out = append(out, it)
	}
	return out, true, nil
}

func (m *memSource) Update(_ *Sync, it Item, ch Change) (Item, error) {
	m.updates = append(m.updates, ch)
	it = m.items[it.ID]
	if ch.Title != nil {
		it.Title = *ch.Title
	}
	if ch.Priority != nil {
		it.Priority = *ch.Priority
	}
	if ch.Assignee != nil && (m.people == nil || *ch.Assignee == "" || slices.Contains(m.people, *ch.Assignee)) {
		it.Assignee = *ch.Assignee
	}
	it.UpdatedAt = m.now
	m.items[it.ID] = it
	return it, nil
}

var t0 = time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)

func request(state plugin.State, settings map[string]any, work []plugin.Work, now time.Time) *plugin.Request {
	return &plugin.Request{Hook: plugin.HookSchedule, Now: now, ProjectID: "p1", Plugin: plugin.Info{ID: "src"},
		Projects: []plugin.Project{{ID: "p1", Key: "SY", On: true, Stages: []plugin.Stage{
			{Key: "backlog", Name: "Backlog", Kind: "todo"}, {Key: "todo", Name: "To do", Kind: "todo"},
			{Key: "doing", Name: "Doing", Kind: "doing"}, {Key: "done", Name: "Done", Kind: "done"}}}},
		ProjectSettings: map[string]map[string]any{"p1": settings}, State: state, Work: work}
}

// next carries a response's state into the next request, like Enplace.
func next(prev plugin.State, res *plugin.Response) plugin.State {
	if prev.Projects == nil {
		prev.Projects = map[string]map[string]json.RawMessage{}
	}
	if res.State == nil {
		return prev
	}
	for pid, kv := range res.State.Projects {
		if prev.Projects[pid] == nil {
			prev.Projects[pid] = map[string]json.RawMessage{}
		}
		for k, v := range kv {
			prev.Projects[pid][k] = v
		}
	}
	return prev
}

func ops(res *plugin.Response, kind string) []map[string]any {
	var out []map[string]any
	for _, op := range res.Ops {
		if op.Kind == kind {
			raw, _ := json.Marshal(op.Params)
			var m map[string]any
			_ = json.Unmarshal(raw, &m)
			out = append(out, m)
		}
	}
	return out
}

func TestSyncRules(t *testing.T) {
	src := &memSource{items: map[string]Item{
		"a": {ID: "a", Ref: "A-1", Title: "One", Status: "Backlog", Priority: 2, UpdatedAt: t0},
		"b": {ID: "b", Ref: "A-2", Title: "Two", Status: "Started", UpdatedAt: t0},
		"c": {ID: "c", Ref: "A-3", Title: "Old", Status: "Closed", UpdatedAt: t0},
	}, now: t0}
	cfg := Config{Name: "Mem", Source: src}
	settings := map[string]any{"stages": "Backlog = Backlog"}

	// First run: one import of the open items, stages mapped.
	res, _ := Handle(request(plugin.State{}, settings, nil, t0), cfg)
	imp := ops(res, "work.import")
	if len(imp) != 1 || len(imp[0]["items"].([]any)) != 2 {
		t.Fatalf("import %+v", res.Ops)
	}
	first := imp[0]["items"].([]any)[0].(map[string]any)
	if first["stage"] != "backlog" || first["fields"].(map[string]any)["src.link"] != "A-1" {
		t.Fatalf("first item %+v", first)
	}
	if second := imp[0]["items"].([]any)[1].(map[string]any); second["stage"] != "doing" {
		t.Fatalf("Started should map to doing: %+v", second)
	}
	st := next(plugin.State{}, res)

	// Approved: the work exists with the link field.
	work := []plugin.Work{
		{ID: "w1", ProjectID: "p1", Title: "One", Stage: "backlog", StageKind: "todo", Priority: 2, Fields: map[string]string{"src.link": "A-1"}, UpdatedAt: t0},
		{ID: "w2", ProjectID: "p1", Title: "Two", Stage: "doing", StageKind: "doing", Fields: map[string]string{"src.link": "A-2"}, UpdatedAt: t0},
	}
	res, _ = Handle(request(st, settings, work, t0.Add(time.Minute)), cfg)
	if len(res.Ops) != 0 {
		t.Fatalf("nothing changed but %+v", res.Ops)
	}
	st = next(st, res)

	// Push only: an outside edit stays out, an Enplace edit goes out.
	settings["direction"] = PushOnly
	it := src.items["a"]
	it.Title, it.UpdatedAt = "Outside rename", t0.Add(2*time.Minute)
	src.items["a"] = it
	work[1].Priority, work[1].UpdatedAt = 4, t0.Add(2*time.Minute)
	src.now = t0.Add(3 * time.Minute)
	res, _ = Handle(request(st, settings, work, t0.Add(3*time.Minute)), cfg)
	if len(ops(res, "work.update")) != 0 || len(src.updates) != 1 || *src.updates[0].Priority != 4 {
		t.Fatalf("push only: ops %+v updates %+v", res.Ops, src.updates)
	}
	st = next(st, res)

	// Two-way, both changed the title: Enplace's is newer, so it goes out
	// and the outside one is kept in a note.
	settings["direction"] = TwoWay
	work[0].Title, work[0].UpdatedAt = "Mine", t0.Add(5*time.Minute)
	res, _ = Handle(request(st, settings, work, t0.Add(6*time.Minute)), cfg)
	notes := ops(res, "work.note")
	if src.items["a"].Title != "Mine" || len(notes) != 1 || !strings.Contains(notes[0]["body"].(string), `Mem's title: "Outside rename"`) {
		t.Fatalf("conflict: outside %q, notes %+v", src.items["a"].Title, notes)
	}

	// The status line and panel.
	st = next(st, res)
	req := request(st, settings, work, t0.Add(8*time.Minute))
	req.Hook, req.Slots, req.WorkID, req.ProjectID = plugin.HookRender, []string{plugin.SlotWorkPanel}, "w1", ""
	res, _ = Handle(req, cfg)
	if b := res.Cards[plugin.SlotWorkPanel]; len(b) != 2 || b[0].Label != "A-1" || !strings.Contains(b[0].Sub, "last synced 2 min ago") {
		t.Fatalf("panel %+v", b)
	}
}

func TestGuessKind(t *testing.T) {
	for status, want := range map[string]string{"Backlog": "todo", "Ready": "todo", "In Progress": "doing", "Blocked": "doing", "In Review": "review", "Canceled": "done", "Done": "done", "": "todo"} {
		if got := GuessKind(status); got != want {
			t.Errorf("%q: %s, want %s", status, got, want)
		}
	}
}

func TestItemStageKeepsTheStage(t *testing.T) {
	req := request(plugin.State{}, map[string]any{}, nil, t0)
	s := open(req, &plugin.Response{}, Config{}, "p1")
	if got := s.stageFor(Item{Status: "Open", StatusKind: "todo", Stage: "doing"}); got != "doing" {
		t.Fatalf("Stage should win: %s", got)
	}
	if got := s.stageFor(Item{Status: "Open", StatusKind: "todo", Stage: "nope"}); got != "todo" {
		t.Fatalf("an unknown Stage falls back to the kind: %s", got)
	}
	if s.StageKind("doing") != "doing" || s.StageKind("nope") != "" {
		t.Fatal("StageKind")
	}
}

// A source's own fields come in on import and when they change outside,
// and are never sent out.
func TestSourceFieldsComeIn(t *testing.T) {
	src := &memSource{items: map[string]Item{
		"a": {ID: "a", Ref: "A-1", Title: "One", Status: "Todo", UpdatedAt: t0, Fields: map[string]string{"points": "3"}},
	}, now: t0}
	cfg := Config{Name: "Mem", Source: src}
	res, _ := Handle(request(plugin.State{}, map[string]any{}, nil, t0), cfg)
	imp := ops(res, "work.import")
	if len(imp) != 1 {
		t.Fatalf("ops %+v", res.Ops)
	}
	if f := imp[0]["items"].([]any)[0].(map[string]any)["fields"].(map[string]any); f["src.points"] != "3" || f["src.link"] != "A-1" {
		t.Fatalf("fields %+v", f)
	}
	st := next(plugin.State{}, res)
	work := []plugin.Work{{ID: "w1", ProjectID: "p1", Title: "One", Stage: "todo", StageKind: "todo", Fields: map[string]string{"src.link": "A-1", "src.points": "3"}, UpdatedAt: t0}}
	res, _ = Handle(request(st, map[string]any{}, work, t0.Add(time.Minute)), cfg)
	if len(res.Ops) != 0 {
		t.Fatalf("nothing changed but %+v", res.Ops)
	}
	st = next(st, res)

	it := src.items["a"]
	it.Fields, it.UpdatedAt = map[string]string{"points": "5"}, t0.Add(2*time.Minute)
	src.items["a"] = it
	res, _ = Handle(request(st, map[string]any{}, work, t0.Add(3*time.Minute)), cfg)
	f := ops(res, "work.fields")
	if len(f) != 1 || f[0]["fields"].(map[string]any)["src.points"] != "5" || len(src.updates) != 0 {
		t.Fatalf("points: ops %+v updates %+v", res.Ops, src.updates)
	}
}

// The assignee comes in, goes out and comes back in like the title, and a
// person the source doesn't know doesn't bounce back as a change outside.
func TestAssigneeSyncs(t *testing.T) {
	src := &memSource{items: map[string]Item{
		"a": {ID: "a", Ref: "A-1", Title: "One", Status: "Todo", Assignee: "Byron", UpdatedAt: t0},
	}, now: t0, people: []string{"Byron", "Sam"}}
	cfg := Config{Name: "Mem", Source: src}
	set := map[string]any{}
	res, _ := Handle(request(plugin.State{}, set, nil, t0), cfg)
	imp := ops(res, "work.import")
	if len(imp) != 1 || imp[0]["items"].([]any)[0].(map[string]any)["assignee"] != "Byron" {
		t.Fatalf("import %+v", res.Ops)
	}
	st := next(plugin.State{}, res)
	work := []plugin.Work{{ID: "w1", ProjectID: "p1", Title: "One", Stage: "todo", StageKind: "todo", Assignee: "byron", Fields: map[string]string{"src.link": "A-1"}, UpdatedAt: t0}}
	res, _ = Handle(request(st, set, work, t0.Add(time.Minute)), cfg)
	if len(res.Ops) != 0 || len(src.updates) != 0 {
		t.Fatalf("same person, other case: ops %+v updates %+v", res.Ops, src.updates)
	}
	st = next(st, res)

	// Changed in Enplace: it goes out, with the member's email.
	work[0].Assignee, work[0].AssigneeEmail, work[0].UpdatedAt = "Sam", "sam@example.com", t0.Add(2*time.Minute)
	src.now = t0.Add(3 * time.Minute)
	res, _ = Handle(request(st, set, work, t0.Add(3*time.Minute)), cfg)
	if len(src.updates) != 1 || *src.updates[0].Assignee != "Sam" || src.updates[0].AssigneeEmail != "sam@example.com" || src.items["a"].Assignee != "Sam" {
		t.Fatalf("out: %+v", src.updates)
	}
	st = next(st, res)

	// Changed outside: it comes in.
	it := src.items["a"]
	it.Assignee, it.UpdatedAt = "", t0.Add(4*time.Minute)
	src.items["a"] = it
	res, _ = Handle(request(st, set, work, t0.Add(5*time.Minute)), cfg)
	up := ops(res, "work.update")
	if len(up) != 1 || up[0]["assignee"] != "" {
		t.Fatalf("in: %+v", res.Ops)
	}
	st = next(st, res)
	work[0].Assignee, work[0].AssigneeEmail = "", ""

	// Someone the source doesn't know: it keeps nobody, and the next run
	// doesn't take the person's pick away.
	work[0].Assignee, work[0].UpdatedAt = "Justin Guedes", t0.Add(6*time.Minute)
	src.now = t0.Add(7 * time.Minute)
	res, _ = Handle(request(st, set, work, t0.Add(7*time.Minute)), cfg)
	if src.items["a"].Assignee != "" || len(ops(res, "work.update")) != 0 {
		t.Fatalf("unknown person: outside %q ops %+v", src.items["a"].Assignee, res.Ops)
	}
	st = next(st, res)
	res, _ = Handle(request(st, set, work, t0.Add(8*time.Minute)), cfg)
	if len(ops(res, "work.update")) != 0 {
		t.Fatalf("the pick bounced back: %+v", res.Ops)
	}
}
