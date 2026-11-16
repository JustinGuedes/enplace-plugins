package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

var t0 = time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)

var stages = []plugin.Stage{{Key: "todo", Name: "To do", Kind: "todo"}, {Key: "doing", Name: "Doing", Kind: "doing"}, {Key: "review", Name: "In review", Kind: "review"}, {Key: "done", Name: "Done", Kind: "done"}}

// world is one project with the plugin on, and a vault the way the
// Obsidian plugin keeps it, carried from call to call.
type world struct {
	t        *testing.T
	state    plugin.State
	settings map[string]any
	work     []plugin.Work
	now      time.Time
	vault    map[string]string
	since    int
	v        int // the app's version, 1 when unset
}

func newWorld(t *testing.T) *world {
	w := &world{t: t, settings: map[string]any{}, now: t0, vault: map[string]string{}}
	w.state = plugin.State{Projects: map[string]map[string]json.RawMessage{}}
	w.work = []plugin.Work{
		{ID: "w1", Key: "ENP-1", ProjectID: "p1", Title: "Write the plan", Stage: "todo", StageKind: "todo", Priority: 3, Tags: []string{"plugins"}, Due: "2026-11-20", Body: "Steps first.", CreatedAt: t0.Add(-time.Hour), UpdatedAt: t0.Add(-time.Hour)},
		{ID: "w2", Key: "ENP-2", ProjectID: "p1", Title: "Ship it", Stage: "doing", StageKind: "doing", CreatedAt: t0.Add(-time.Hour), UpdatedAt: t0.Add(-time.Hour)},
		{ID: "w3", Key: "ENP-3", ProjectID: "p1", Title: "Old thing", Stage: "done", StageKind: "done", CreatedAt: t0.Add(-time.Hour), UpdatedAt: t0.Add(-time.Hour)},
	}
	return w
}

func (w *world) request(hook string) *plugin.Request {
	return &plugin.Request{Hook: hook, Now: w.now, Timezone: "UTC", ProjectID: "p1", Plugin: plugin.Info{ID: "obsidian"},
		Projects:        []plugin.Project{{ID: "p1", Key: "ENP", Name: "Enplace", On: true, Stages: stages}},
		ProjectSettings: map[string]map[string]any{"p1": w.settings}, State: w.state, Work: w.work}
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
			if string(v) == "null" {
				delete(w.state.Projects[pid], k)
				continue
			}
			w.state.Projects[pid][k] = v
		}
	}
}

// call is one exchange from Obsidian, with the vault's changes; it
// writes what comes back into the vault.
func (w *world) call(changes []change, all bool) (*plugin.Response, answer) {
	w.t.Helper()
	in := call{V: max(w.v, 1), At: w.now, Since: w.since, Changes: changes}
	if all {
		in.All = []string{}
		for p := range w.vault {
			in.All = append(in.All, p)
		}
	}
	req := w.request(plugin.HookWebhook)
	body, _ := json.Marshal(in)
	req.Webhook = &plugin.Webhook{On: true, Delivery: &plugin.Delivery{Body: body}}
	res, err := handle(req)
	if err != nil {
		w.t.Fatal(err)
	}
	w.keep(res)
	raw, _ := json.Marshal(res.Data)
	var a answer
	if err := json.Unmarshal(raw, &a); err != nil {
		w.t.Fatal(err)
	}
	if a.Error != "" {
		return res, a
	}
	for _, wr := range a.Writes {
		w.vault[wr.Path] = wr.Text
	}
	w.since = a.Rev
	return res, a
}

// edit changes a note in the vault and sends it.
func (w *world) edit(p string, f func(string) string) *plugin.Response {
	w.t.Helper()
	w.vault[p] = f(w.vault[p])
	res, _ := w.call([]change{{Path: p, Text: w.vault[p], Mod: w.now}}, false)
	return res
}

// event is a person's change in Enplace reaching the plugin.
func (w *world) event(i int) *plugin.Response {
	w.t.Helper()
	req := w.request(plugin.HookEvent)
	req.ProjectID = ""
	req.Event = &plugin.Event{Type: "work.updated", Work: w.work[i]}
	res, err := handle(req)
	if err != nil {
		w.t.Fatal(err)
	}
	w.keep(res)
	return res
}

func (w *world) at(d time.Duration) { w.now = t0.Add(d) }

func ops(res *plugin.Response, kind string) []map[string]any {
	var out []map[string]any
	for _, op := range res.Ops {
		if op.Kind == kind {
			var m map[string]any
			raw, _ := json.Marshal(op.Params)
			_ = json.Unmarshal(raw, &m)
			out = append(out, m)
		}
	}
	return out
}

// apply sets the plugin's own fields, as Enplace does without asking.
func (w *world) apply(res *plugin.Response) {
	for _, f := range ops(res, "work.fields") {
		for i := range w.work {
			if w.work[i].ID == f["id"] {
				if w.work[i].Fields == nil {
					w.work[i].Fields = map[string]string{}
				}
				for k, v := range f["fields"].(map[string]any) {
					w.work[i].Fields[k] = v.(string)
				}
			}
		}
	}
}

// approve turns filed new work into work items.
func (w *world) approve(res *plugin.Response) {
	var inputs []map[string]any
	for _, c := range ops(res, "work.create") {
		inputs = append(inputs, c)
	}
	for _, imp := range ops(res, "work.import") {
		for _, x := range imp["items"].([]any) {
			inputs = append(inputs, x.(map[string]any))
		}
	}
	for _, in := range inputs {
		n := len(w.work) + 1
		wk := plugin.Work{ID: "w" + strconv.Itoa(n), Key: "ENP-" + strconv.Itoa(n), ProjectID: "p1", Title: in["title"].(string), Stage: in["stage"].(string), CreatedAt: w.now, UpdatedAt: w.now,
			Fields: map[string]string{"obsidian.link": in["fields"].(map[string]any)["obsidian.link"].(string)}}
		wk.StageKind = wk.Stage
		if b, ok := in["body"].(string); ok {
			wk.Body = b
		}
		if p, ok := in["priority"].(float64); ok {
			wk.Priority = int(p)
		}
		if d, ok := in["due"].(string); ok {
			wk.Due = d
		}
		if tags, ok := in["tags"].([]any); ok {
			for _, x := range tags {
				wk.Tags = append(wk.Tags, x.(string))
			}
		}
		w.work = append(w.work, wk)
	}
}

func notePath(w *world, key string) string {
	for p, text := range w.vault {
		if strings.Contains(text, "enplace_id: "+key+"\n") {
			return p
		}
	}
	w.t.Fatalf("no note for %s in %v", key, w.vault)
	return ""
}

func TestFirstCallWritesOpenWork(t *testing.T) {
	w := newWorld(t)
	res, a := w.call(nil, true)
	if a.Folder != "Enplace (ENP)" || a.Project.Key != "ENP" || len(a.Stages) != 4 {
		t.Fatalf("answer %+v", a)
	}
	if len(w.vault) != 2 {
		t.Fatalf("want the two open items as notes, got %v", w.vault)
	}
	want := "---\nenplace_id: ENP-1\nstage: todo\npriority: 3\ntags: [plugins]\ndue: 2026-11-20\n---\n# Write the plan\n\nSteps first.\n"
	if got := w.vault["ENP-1 Write the plan.md"]; got != want {
		t.Fatalf("note:\n%s\nwant:\n%s", got, want)
	}
	if f := ops(res, "work.fields"); len(f) != 2 {
		t.Fatalf("links on the cards: %+v", res.Ops)
	}
	w.apply(res)
	if !strings.HasPrefix(a.Status, "Synced with Obsidian") {
		t.Fatalf("status %q", a.Status)
	}

	// Nothing changed: nothing to write, nothing to ask.
	w.at(time.Minute)
	res, a = w.call(nil, false)
	if len(a.Writes) != 0 || len(res.Ops) != 0 {
		t.Fatalf("quiet call: writes %+v ops %+v", a.Writes, res.Ops)
	}
}

func TestVaultEditsReachWork(t *testing.T) {
	w := newWorld(t)
	res, _ := w.call(nil, true)
	w.apply(res)
	p := notePath(w, "ENP-1")

	w.at(time.Minute)
	res = w.edit(p, func(s string) string { return strings.Replace(s, "stage: todo", "stage: Doing", 1) })
	if mv := ops(res, "work.move"); len(mv) != 1 || mv[0]["id"] != "w1" || mv[0]["stage"] != "doing" {
		t.Fatalf("stage edit: %+v", res.Ops)
	}
	w.work[0].Stage, w.work[0].StageKind, w.work[0].UpdatedAt = "doing", "doing", w.now

	w.at(2 * time.Minute)
	res = w.edit(p, func(s string) string { return strings.Replace(s, "# Write the plan", "# Write the whole plan", 1) })
	up := ops(res, "work.update")
	if len(up) != 1 || up[0]["title"] != "Write the whole plan" {
		t.Fatalf("title edit: %+v", res.Ops)
	}
	if len(ops(res, "work.move")) != 0 {
		t.Fatalf("stage bounced: %+v", res.Ops)
	}
}

func TestWorkEditsKeepTheirOwnProperties(t *testing.T) {
	w := newWorld(t)
	res, _ := w.call(nil, true)
	w.apply(res)
	p := notePath(w, "ENP-1")
	w.at(time.Minute)
	w.edit(p, func(s string) string {
		return strings.Replace(s, "due: 2026-11-20\n", "due: 2026-11-20\nmood: calm  # mine\naliases:\n  - plan\n", 1)
	})

	// Edited in Enplace: priority, stage and title change in place.
	w.at(2 * time.Minute)
	w.work[0].Priority, w.work[0].Stage, w.work[0].StageKind, w.work[0].Title, w.work[0].UpdatedAt = 1, "review", "review", "Write the plan, again", w.now
	w.event(0)
	_, a := w.call(nil, false)
	if len(a.Writes) != 1 {
		t.Fatalf("writes %+v", a.Writes)
	}
	want := "---\nenplace_id: ENP-1\nstage: review\npriority: 1\ntags: [plugins]\ndue: 2026-11-20\nmood: calm  # mine\naliases:\n  - plan\n---\n# Write the plan, again\n\nSteps first.\n"
	if got := w.vault[p]; got != want {
		t.Fatalf("note:\n%s\nwant:\n%s", got, want)
	}
}

func TestNewNoteBecomesWorkAndGetsItsKey(t *testing.T) {
	w := newWorld(t)
	res, _ := w.call(nil, true)
	w.apply(res)

	w.at(time.Minute)
	w.vault["Ideas/Try Bases.md"] = "---\nstage: doing\nsource: chat\n---\nSee if Bases can show the board.\n"
	res, _ = w.call([]change{{Path: "Ideas/Try Bases.md", Text: w.vault["Ideas/Try Bases.md"]}}, false)
	created := ops(res, "work.create")
	if len(created) != 1 || created[0]["title"] != "Try Bases" || created[0]["stage"] != "doing" || created[0]["body"] != "See if Bases can show the board." {
		t.Fatalf("new note: %+v", res.Ops)
	}
	w.approve(res)

	w.at(2 * time.Minute)
	_, a := w.call(nil, false)
	if len(a.Writes) != 1 || a.Writes[0].Path != "Ideas/Try Bases.md" {
		t.Fatalf("the key should be written in: %+v", a.Writes)
	}
	if got, want := w.vault["Ideas/Try Bases.md"], "---\nenplace_id: ENP-4\nstage: doing\nsource: chat\n---\nSee if Bases can show the board.\n"; got != want {
		t.Fatalf("note:\n%s\nwant:\n%s", got, want)
	}
	// And it isn't written a second note.
	if len(w.vault) != 3 {
		t.Fatalf("vault %v", w.vault)
	}
}

func TestDeletedNoteMarksWorkDone(t *testing.T) {
	w := newWorld(t)
	res, _ := w.call(nil, true)
	w.apply(res)
	p := notePath(w, "ENP-2")
	w.at(time.Minute)
	delete(w.vault, p)
	res, _ = w.call([]change{{Path: p, Deleted: true}}, false)
	mv := ops(res, "work.move")
	if len(mv) != 1 || mv[0]["id"] != "w2" || mv[0]["stage"] != "done" || !strings.Contains(mv[0]["note"].(string), "Nothing was deleted") {
		t.Fatalf("delete: %+v", res.Ops)
	}
	for _, op := range res.Ops {
		if strings.Contains(op.Kind, "delete") {
			t.Fatalf("never a delete: %+v", op)
		}
	}
}

func TestDeletedWhileClosedIsFound(t *testing.T) {
	w := newWorld(t)
	res, _ := w.call(nil, true)
	w.apply(res)
	delete(w.vault, notePath(w, "ENP-2"))
	w.at(time.Hour)
	res, _ = w.call(nil, true)
	if mv := ops(res, "work.move"); len(mv) != 1 || mv[0]["id"] != "w2" {
		t.Fatalf("delete while closed: %+v", res.Ops)
	}
}

func TestRenameKeepsTheLink(t *testing.T) {
	w := newWorld(t)
	res, _ := w.call(nil, true)
	w.apply(res)
	old := notePath(w, "ENP-1")
	w.at(time.Minute)
	text := w.vault[old]
	delete(w.vault, old)
	w.vault["Plans/The plan.md"] = text
	res, _ = w.call([]change{{Path: "Plans/The plan.md", OldPath: old, Text: text}}, false)
	if len(res.Ops) != 0 {
		t.Fatalf("a rename changes nothing: %+v", res.Ops)
	}
	// Moved without Obsidian seeing it (another app, or while closed):
	// the note names its item, so it's still the same one.
	w.at(2 * time.Minute)
	delete(w.vault, "Plans/The plan.md")
	w.vault["Elsewhere.md"] = text
	res, _ = w.call([]change{{Path: "Elsewhere.md", Text: text}, {Path: "Plans/The plan.md", Deleted: true}}, false)
	if len(res.Ops) != 0 {
		t.Fatalf("a move changes nothing: %+v", res.Ops)
	}
	w.at(3 * time.Minute)
	res = w.edit("Elsewhere.md", func(s string) string { return strings.Replace(s, "stage: todo", "stage: review", 1) })
	if mv := ops(res, "work.move"); len(mv) != 1 || mv[0]["id"] != "w1" {
		t.Fatalf("moved note lost its link: %+v", res.Ops)
	}
}

func TestExportedNoteIsLinkedNotCopied(t *testing.T) {
	w := newWorld(t)
	// A Markdown export dropped into the folder before connecting.
	w.vault["ENP-1.md"] = "---\nkey: ENP-1\ntitle: \"Write the plan\"\nstage: \"todo\"\npriority: 3\n---\n\n# Write the plan\n\nSteps first.\n"
	res, _ := w.call([]change{{Path: "ENP-1.md", Text: w.vault["ENP-1.md"], Mod: t0.Add(-2 * time.Hour)}}, true)
	if len(ops(res, "work.create"))+len(ops(res, "work.import")) != 0 {
		t.Fatalf("an exported note shouldn't be new work: %+v", res.Ops)
	}
	if len(w.vault) != 2 {
		t.Fatalf("ENP-1 should stay one note: %v", w.vault)
	}
	// Enplace had the newer tags, so they go into the note.
	if !strings.Contains(w.vault["ENP-1.md"], "tags: [plugins]") {
		t.Fatalf("note %s", w.vault["ENP-1.md"])
	}
}

func TestBothChangedLaterWins(t *testing.T) {
	w := newWorld(t)
	res, _ := w.call(nil, true)
	w.apply(res)
	p := notePath(w, "ENP-1")
	w.at(time.Minute)
	w.work[0].Title, w.work[0].UpdatedAt = "Plan, from Enplace", w.now
	w.at(2 * time.Minute)
	res = w.edit(p, func(s string) string { return strings.Replace(s, "# Write the plan", "# Plan, from Obsidian", 1) })
	if up := ops(res, "work.update"); len(up) != 1 || up[0]["title"] != "Plan, from Obsidian" {
		t.Fatalf("later change: %+v", res.Ops)
	}
	if n := ops(res, "work.note"); len(n) != 1 || !strings.Contains(n[0]["body"].(string), "Plan, from Enplace") {
		t.Fatalf("the other should be a note: %+v", res.Ops)
	}
}

func TestOldCallsAreTurnedAway(t *testing.T) {
	w := newWorld(t)
	in := call{V: 1, At: t0.Add(-10 * time.Minute)}
	req := w.request(plugin.HookWebhook)
	body, _ := json.Marshal(in)
	req.Webhook = &plugin.Webhook{On: true, Delivery: &plugin.Delivery{Body: body}}
	res, _ := handle(req)
	if a := res.Data.(answer); a.Error == "" || len(a.Writes) != 0 || res.State != nil {
		t.Fatalf("old call: %+v", res.Data)
	}
}

func TestBadPathsAreSkipped(t *testing.T) {
	w := newWorld(t)
	_, a := w.call([]change{{Path: "../escape.md", Text: "x"}, {Path: "/abs.md", Text: "x"}, {Path: "a.txt", Text: "x"}}, false)
	if len(a.Skip) != 3 {
		t.Fatalf("skipped %v", a.Skip)
	}
}

func TestWaitingLineWhenObsidianIsAway(t *testing.T) {
	w := newWorld(t)
	req := w.request(plugin.HookSchedule)
	res, err := handle(req)
	if err != nil {
		t.Fatal(err)
	}
	w.keep(res)
	if got := res.Status["p1"].Text; !strings.HasPrefix(got, "Waiting for Obsidian") {
		t.Fatalf("never connected: %q", got)
	}
	w.call(nil, true)
	w.at(time.Hour)
	res, _ = handle(w.request(plugin.HookSchedule))
	if got := res.Status["p1"].Text; got != "Waiting for Obsidian · last synced 10:00" {
		t.Fatalf("away: %q", got)
	}
}

func TestNoteRoundTrip(t *testing.T) {
	for _, text := range []string{
		"---\nenplace_id: ENP-1\nstage: todo\nmood: calm\n---\n# T\n\nBody\n",
		"Just a body\n",
		"---\ntags:\n  - a\n  - b\n---\nBody [[link]]\n",
	} {
		if got := parseNote(text).String(); got != text {
			t.Errorf("round trip:\n%q\n%q", text, got)
		}
	}
	n := parseNote("---\ntags:\n  - a\n  - \"#b\"\npriority: high\ndue: '2026-11-20'\n---\nx\n")
	if tags := n.list("tags"); len(tags) != 2 || tags[1] != "b" || n.priority() != 3 || n.due() != "2026-11-20" {
		t.Fatalf("read %v %d %q", tags, n.priority(), n.due())
	}
}
