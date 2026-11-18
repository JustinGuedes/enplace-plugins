package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

// chainBoard is Chain's Project Board, as the app sends it.
var chainBoard = []board{
	{Path: "Release Board.base", Name: "Release Board", Property: "milestone", Folder: "Tasks", Lanes: []lane{{Name: "Native MVP"}, {Name: "Later"}}},
	{Path: "Project Board.base", Name: "Project Board", Property: "note.status", Folder: "Tasks", Lanes: []lane{
		{Name: "Backlog", Colour: "purple"}, {Name: "Ready", Colour: "cyan"}, {Name: "In progress", Colour: "blue"},
		{Name: "Review", Colour: "orange"}, {Name: "Blocked", Colour: "red"}, {Name: "Done", Colour: "green"}}},
}

// callBoards is one exchange that also sends the vault's boards.
func (w *world) callBoards(bs []board) {
	w.t.Helper()
	in := call{V: 2, At: w.now, Since: w.since, Boards: bs}
	req := w.request(plugin.HookWebhook)
	body, _ := json.Marshal(in)
	req.Webhook = &plugin.Webhook{On: true, Delivery: &plugin.Delivery{Body: body}}
	res, err := handle(req)
	if err != nil {
		w.t.Fatal(err)
	}
	w.keep(res)
}

func (w *world) render(slots ...string) *plugin.Response {
	w.t.Helper()
	req := w.request(plugin.HookRender)
	req.Slots = slots
	res, err := handle(req)
	if err != nil {
		w.t.Fatal(err)
	}
	return res
}

func (w *world) view() *plugin.View {
	w.t.Helper()
	req := w.request(plugin.HookView)
	req.View = "board"
	res, err := handle(req)
	if err != nil || res.View == nil {
		w.t.Fatal(err, res)
	}
	return res.View
}

func byKey(w *world, ref string) int {
	for i := range w.work {
		if w.work[i].Fields["obsidian.link"] == ref {
			return i
		}
	}
	w.t.Fatalf("no work for %s", ref)
	return -1
}

func TestLanesComeInWithTheNotes(t *testing.T) {
	w := ownWorld(t)
	res, _ := w.call(w.sendAll(), true)
	items := ops(res, "work.import")[0]["items"].([]any)
	lanes := map[string]string{}
	for _, x := range items {
		f := x.(map[string]any)["fields"].(map[string]any)
		lanes[f["obsidian.link"].(string)], _ = f["obsidian.lane"].(string)
	}
	if lanes["CHAIN-001"] != "In progress" || lanes["CHAIN-002"] != "Blocked" || lanes["CHAIN-003"] != "Done" {
		t.Fatalf("lanes %v", lanes)
	}
}

func TestBoardLanesShapeTheFieldAndTheView(t *testing.T) {
	w := linkedOwnWorld(t)
	// Without a board, the lanes are the statuses the notes have, by stage.
	opts := w.render(plugin.SlotWorkFields).Fields["lane"].Options
	if len(opts) != 3 || opts[0].Value != "In progress" || opts[1].Value != "Blocked" || opts[2].Value != "Done" {
		t.Fatalf("lanes without a board %+v", opts)
	}
	w.at(2 * time.Minute)
	w.callBoards(chainBoard)
	fs := w.render(plugin.SlotWorkFields).Fields["lane"]
	if len(fs.Options) != 6 || fs.Options[4].Value != "Blocked" || fs.Options[4].Colour != "#ff5d6c" || fs.Options[4].Short != "Blocked" || fs.Hidden {
		t.Fatalf("lanes from the board %+v", fs)
	}
	v := w.view()
	if len(v.Groups) != 6 || !v.Columns || v.Groups[0].Title != "Backlog" || v.Groups[0].Colour != "#b38cff" {
		t.Fatalf("view %+v", v.Groups)
	}
	blocked := v.Groups[4]
	if blocked.Title != "Blocked" || len(blocked.Work) != 1 || blocked.Work[0] != w.work[byKey(w, "CHAIN-002")].ID || blocked.Set["obsidian.lane"] != "Blocked" {
		t.Fatalf("blocked lane %+v", blocked)
	}
	// The panel names the board over the folder and where each lane goes.
	res := w.render(plugin.SlotSettingsPanel)
	raw, _ := json.Marshal(res.Cards[plugin.SlotSettingsPanel])
	if !strings.Contains(string(raw), `"value":"Project Board · Tasks"`) || !strings.Contains(string(raw), `"key":"lane:Blocked","label":"Blocked","type":"select","options":["To do","Doing","In review","Done"],"value":"Doing"`) {
		t.Fatalf("panel %s", raw)
	}
}

func TestDroppingACardInALaneMovesItsNote(t *testing.T) {
	w := linkedOwnWorld(t)
	w.callBoards(chainBoard)
	p := "CHAIN-001 - Confirm the MVP.md"
	i := byKey(w, "CHAIN-001")
	// Blocked goes to the same stage: only the note changes.
	w.at(2 * time.Minute)
	w.work[i].Fields["obsidian.lane"], w.work[i].UpdatedAt = "Blocked", w.now
	res := w.event(i)
	if len(ops(res, "work.move")) != 0 {
		t.Fatalf("ops %+v", res.Ops)
	}
	w.call(nil, false)
	if got := w.vault[p]; !strings.Contains(got, `"status": "Blocked"`) || !strings.Contains(got, `"updated": "2026-11-14"`) {
		t.Fatalf("note:\n%s", got)
	}
	// Review is another stage: the work follows.
	w.at(3 * time.Minute)
	w.work[i].Fields["obsidian.lane"], w.work[i].UpdatedAt = "Review", w.now
	res = w.event(i)
	if mv := ops(res, "work.move"); len(mv) != 1 || mv[0]["stage"] != "review" {
		t.Fatalf("move %+v", res.Ops)
	}
	w.work[i].Stage, w.work[i].StageKind = "review", "review"
	w.call(nil, false)
	if !strings.Contains(w.vault[p], `"status": "Review"`) {
		t.Fatalf("note:\n%s", w.vault[p])
	}
}

func TestStageMovedInEnplaceMovesTheLane(t *testing.T) {
	w := linkedOwnWorld(t)
	w.callBoards(chainBoard)
	i := byKey(w, "CHAIN-002") // Blocked, in Doing
	w.at(2 * time.Minute)
	w.work[i].Stage, w.work[i].StageKind, w.work[i].UpdatedAt = "review", "review", w.now
	w.event(i)
	w.call(nil, false)
	p := "CHAIN-002 - Verify the baseline.md"
	if !strings.Contains(w.vault[p], "status: Review\n") {
		t.Fatalf("note:\n%s", w.vault[p])
	}
	// The lane field catches up and isn't sent back as a change.
	w.at(3 * time.Minute)
	res, _ := w.call(nil, false)
	w.apply(res)
	if w.work[i].Fields["obsidian.lane"] != "Review" {
		t.Fatalf("lane %q, ops %+v", w.work[i].Fields["obsidian.lane"], res.Ops)
	}
	w.at(4 * time.Minute)
	w.call(nil, false)
	if !strings.Contains(w.vault[p], "status: Review\n") {
		t.Fatalf("note:\n%s", w.vault[p])
	}
}

func TestLanesPickedInThePanel(t *testing.T) {
	w := linkedOwnWorld(t)
	w.callBoards(chainBoard)
	i := byKey(w, "CHAIN-002") // Blocked, in Doing
	req := w.request(plugin.HookAction)
	req.Action = &plugin.Action{Name: "obsidian.lanes", Args: json.RawMessage(`{"lane:Blocked":"In review","lane:Ready":"Doing"}`)}
	res, err := handle(req)
	if err != nil {
		t.Fatal(err)
	}
	w.keep(res)
	if mv := ops(res, "work.move"); len(mv) != 1 || mv[0]["id"] != w.work[i].ID || mv[0]["stage"] != "review" {
		t.Fatalf("moves %+v", res.Ops)
	}
	w.work[i].Stage, w.work[i].StageKind = "review", "review"
	// The panel shows the picks, and a note moved to Blocked goes to In review.
	raw, _ := json.Marshal(w.render(plugin.SlotSettingsPanel).Cards[plugin.SlotSettingsPanel])
	if !strings.Contains(string(raw), `"label":"Blocked","type":"select","options":["To do","Doing","In review","Done"],"value":"In review"`) {
		t.Fatalf("panel %s", raw)
	}
	j := byKey(w, "CHAIN-001")
	w.at(2 * time.Minute)
	p := "CHAIN-001 - Confirm the MVP.md"
	text := strings.Replace(w.vault[p], `"status": "In progress"`, `"status": "Blocked"`, 1)
	res, _ = w.call([]change{{Path: p, Text: text}}, false)
	if mv := ops(res, "work.move"); len(mv) != 1 || mv[0]["id"] != w.work[j].ID || mv[0]["stage"] != "review" {
		t.Fatalf("moves %+v", res.Ops)
	}
}

func TestBlockedNeedsNoMapping(t *testing.T) {
	w := ownWorld(t)
	delete(w.settings, "stages")
	res, _ := w.call(w.sendAll(), true)
	for _, x := range ops(res, "work.import")[0]["items"].([]any) {
		it := x.(map[string]any)
		if it["fields"].(map[string]any)["obsidian.link"] == "CHAIN-002" && it["stage"] != "doing" {
			t.Fatalf("Blocked went to %v", it["stage"])
		}
	}
}

func TestEnplaceFolderHasNoLanes(t *testing.T) {
	w := newWorld(t)
	w.call(nil, true)
	if fs := w.render(plugin.SlotWorkFields).Fields["lane"]; !fs.Hidden {
		t.Fatalf("lane field %+v", fs)
	}
	if v := w.view(); len(v.Groups) != 1 || !v.Groups[0].Muted {
		t.Fatalf("view %+v", v.Groups)
	}
}

func TestBoardsAreCleaned(t *testing.T) {
	w := ownWorld(t)
	w.callBoards([]board{{Path: "a.base", Property: "status", Folder: "../x", Lanes: []lane{{Name: "A", Colour: "#ABCDEF"}, {Name: "a"}, {Name: " "}, {Name: "B", Colour: "javascript:x"}}},
		{Path: "", Property: "status", Lanes: []lane{{Name: "X"}}}})
	got := boards(w.request(plugin.HookRender), "p1")
	if len(got) != 1 || got[0].Folder != "" || got[0].Name != "a" || len(got[0].Lanes) != 2 || got[0].Lanes[0].Colour != "#abcdef" || got[0].Lanes[1].Colour != "" {
		t.Fatalf("boards %+v", got)
	}
}
