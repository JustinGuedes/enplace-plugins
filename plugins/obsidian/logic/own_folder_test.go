package main

import (
	"strings"
	"testing"
	"time"
)

// Notes the way a vault someone else keeps has them: its own folder,
// quoted keys, "status" and "id" of its own, P1-style priorities.
const (
	chainOne = `---
"id": "CHAIN-001"
"type": "task"
"status": "In progress"
"segment": "Product"
"priority": "P1"
"assignee": "Byron"
"due": null
"blocked_by": []
"created": "2026-10-30"
"updated": "2026-11-15"
"tags": ["chain", "task"]
---

# Confirm the MVP

Agree one end-to-end outcome.

## Work log

- 2026-10-30 — Seeded.
`
	chainTwo = `---
id: CHAIN-002
status: Blocked
priority: P2
due:
updated: 2026-11-13
tags:
  - chain
---

# Verify the baseline

Waiting on the repo.
`
	chainDone = `---
"id": "CHAIN-003"
"status": "Done"
---

# Old work
`
	chainStages = "Backlog = To do; Ready = To do; In progress = Doing; Review = In review; Blocked = Doing; Done = Done"
)

func ownWorld(t *testing.T) *world {
	w := newWorld(t)
	w.v = 2
	w.settings["folder"] = "Tasks/"
	w.settings["stages"] = chainStages
	w.vault = map[string]string{
		"CHAIN-001 - Confirm the MVP.md":     chainOne,
		"CHAIN-002 - Verify the baseline.md": chainTwo,
		"CHAIN-003 - Old work.md":            chainDone,
	}
	return w
}

// sendAll sends every note, as the app does the first time.
func (w *world) sendAll() []change {
	var out []change
	for p, text := range w.vault {
		out = append(out, change{Path: p, Text: text, Mod: w.now})
	}
	return out
}

func TestOwnFolderBringsNotesInAsTheyAre(t *testing.T) {
	w := ownWorld(t)
	res, a := w.call(w.sendAll(), true)
	if a.Error != "" || a.Path != "Tasks" {
		t.Fatalf("answer %+v", a)
	}
	// Enplace's own work is never written into their folder.
	if len(a.Writes) != 0 {
		t.Fatalf("writes %+v", a.Writes)
	}
	imp := ops(res, "work.import")
	if len(imp) != 1 {
		t.Fatalf("ops %+v", res.Ops)
	}
	items := imp[0]["items"].([]any)
	if len(items) != 3 { // finished notes come in too
		t.Fatalf("items %+v", items)
	}
	if done := items[2].(map[string]any); done["stage"] != "done" || done["title"] != "Old work" {
		t.Fatalf("CHAIN-003 %+v", done)
	}
	if st := res.Status["p1"]; st == nil || !strings.HasSuffix(st.Text, " · Tasks folder") || !strings.HasSuffix(a.Status, " · Tasks folder") {
		t.Fatalf("status %+v %q", res.Status["p1"], a.Status)
	}
	one := items[0].(map[string]any)
	if one["title"] != "Confirm the MVP" || one["stage"] != "doing" || one["priority"] != float64(3) ||
		one["fields"].(map[string]any)["obsidian.link"] != "CHAIN-001" || !strings.Contains(one["body"].(string), "## Work log") {
		t.Fatalf("CHAIN-001 %+v", one)
	}
	two := items[1].(map[string]any)
	if two["stage"] != "doing" || two["priority"] != float64(2) || two["fields"].(map[string]any)["obsidian.link"] != "CHAIN-002" {
		t.Fatalf("CHAIN-002 %+v", two)
	}
	w.approve(res)

	// Linked: no key is written into their notes.
	w.at(time.Minute)
	_, a = w.call(nil, false)
	if len(a.Writes) != 0 || w.vault["CHAIN-001 - Confirm the MVP.md"] != chainOne {
		t.Fatalf("writes %+v", a.Writes)
	}
}

func linkedOwnWorld(t *testing.T) *world {
	w := ownWorld(t)
	res, _ := w.call(w.sendAll(), true)
	w.approve(res)
	w.at(time.Minute)
	w.call(nil, false)
	return w
}

func TestOwnFolderTakesEnplaceEditsInTheirStyle(t *testing.T) {
	w := linkedOwnWorld(t)
	i := len(w.work) - 3 // CHAIN-001
	w.at(2 * time.Minute)
	wk := &w.work[i]
	wk.Title, wk.Stage, wk.StageKind, wk.Priority, wk.Due, wk.UpdatedAt = "Confirm the MVP with Byron", "review", "review", 2, "2026-11-30", w.now
	wk.Body = strings.Replace(wk.Body, "- 2026-10-30 — Seeded.", "- 2026-10-30 — Seeded.\n- 2026-11-16 — Agreed.", 1)
	wk.Tags = []string{"chain", "task"}
	w.event(i)
	_, a := w.call(nil, false)
	if len(a.Writes) != 1 || a.Writes[0].Path != "CHAIN-001 - Confirm the MVP.md" {
		t.Fatalf("writes %+v", a.Writes)
	}
	want := `---
"id": "CHAIN-001"
"type": "task"
"status": "Review"
"segment": "Product"
"priority": "P2"
"assignee": "Byron"
"due": "2026-11-30"
"blocked_by": []
"created": "2026-10-30"
"updated": "2026-11-14"
"tags": ["chain", "task"]
---

# Confirm the MVP with Byron

Agree one end-to-end outcome.

## Work log

- 2026-10-30 — Seeded.
- 2026-11-16 — Agreed.
`
	if got := w.vault["CHAIN-001 - Confirm the MVP.md"]; got != want {
		t.Fatalf("note:\n%s\nwant:\n%s", got, want)
	}
	if len(w.vault) != 3 {
		t.Fatalf("no notes are added: %v", w.vault)
	}
}

func TestOwnFolderKeepsAStatusTheStageStillFits(t *testing.T) {
	w := linkedOwnWorld(t)
	i := len(w.work) - 2 // CHAIN-002, Blocked, in Doing
	w.at(2 * time.Minute)
	w.work[i].Title, w.work[i].UpdatedAt = "Verify the repo baseline", w.now
	w.event(i)
	w.call(nil, false)
	got := w.vault["CHAIN-002 - Verify the baseline.md"]
	if !strings.Contains(got, "status: Blocked\n") || !strings.Contains(got, "# Verify the repo baseline\n") || !strings.Contains(got, "updated: 2026-11-14\n") {
		t.Fatalf("note:\n%s", got)
	}
	// Clearing the due date keeps the key, as their notes have it.
	w.at(3 * time.Minute)
	w.work[i].Priority, w.work[i].UpdatedAt = 0, w.now
	w.event(i)
	w.call(nil, false)
	if got := w.vault["CHAIN-002 - Verify the baseline.md"]; !strings.Contains(got, "\npriority:\ndue:\n") {
		t.Fatalf("note:\n%s", got)
	}
}

func TestOwnFolderEditsReachWork(t *testing.T) {
	w := linkedOwnWorld(t)
	p := "CHAIN-001 - Confirm the MVP.md"
	w.at(2 * time.Minute)
	res := w.edit(p, func(s string) string {
		s = strings.Replace(s, `"status": "In progress"`, `"status": "Done"`, 1)
		return strings.Replace(s, "- 2026-10-30 — Seeded.", "- 2026-10-30 — Seeded.\n- 2026-11-16 — Shipped.", 1)
	})
	if mv := ops(res, "work.move"); len(mv) != 1 || mv[0]["stage"] != "done" {
		t.Fatalf("move %+v", res.Ops)
	}
	if up := ops(res, "work.update"); len(up) != 1 || !strings.Contains(up[0]["body"].(string), "Shipped.") {
		t.Fatalf("update %+v", res.Ops)
	}
}

func TestOwnFolderNoteMovedWhileClosedKeepsItsLink(t *testing.T) {
	w := linkedOwnWorld(t)
	w.at(2 * time.Minute)
	text := w.vault["CHAIN-002 - Verify the baseline.md"]
	delete(w.vault, "CHAIN-002 - Verify the baseline.md")
	w.vault["CHAIN-002 - Verify.md"] = text
	res, _ := w.call([]change{{Path: "CHAIN-002 - Verify.md", Text: text, Mod: w.now}}, true)
	if len(ops(res, "work.create"))+len(ops(res, "work.import"))+len(ops(res, "work.move")) != 0 {
		t.Fatalf("ops %+v", res.Ops)
	}
}

func TestOwnFolderNeedsANewApp(t *testing.T) {
	w := ownWorld(t)
	w.v = 1
	res, a := w.call(w.sendAll(), true)
	if a.Error == "" || res.State != nil {
		t.Fatalf("old app: %+v", a)
	}
}

func TestOwnFolderIsAPlainFolder(t *testing.T) {
	for raw, want := range map[string]string{"Tasks": "Tasks", "/Work/Tasks/": "Work/Tasks", "../x": "", ".obsidian": "", "a/../../b": "", " ": ""} {
		w := newWorld(t)
		w.settings["folder"] = raw
		if got := ownFolder(w.request("webhook"), "p1"); got != want {
			t.Errorf("%q: %q, want %q", raw, got, want)
		}
	}
}

func TestQuotedNoteRoundTrip(t *testing.T) {
	for _, text := range []string{chainOne, chainTwo, chainDone, "---\na: 1\n---\n\n\n# T\nno gap", "# Only a title\n"} {
		if got := parseNote(text).String(); got != text {
			t.Errorf("round trip:\n%q\n%q", text, got)
		}
	}
	n := parseNote(chainOne)
	if n.value("status") != "In progress" || n.value("id") != "CHAIN-001" || n.priority() != 3 || n.due() != "" || len(n.list("tags")) != 2 {
		t.Fatalf("read %q %q %d %v", n.value("status"), n.value("id"), n.priority(), n.list("tags"))
	}
}

// A note's assignee comes in, and a change in Enplace is written back in
// the note's own style.
func TestOwnFolderAssignee(t *testing.T) {
	w := linkedOwnWorld(t)
	i := len(w.work) - 3 // CHAIN-001
	if w.work[i].Assignee != "Byron" {
		t.Fatalf("came in as %q", w.work[i].Assignee)
	}
	w.at(2 * time.Minute)
	w.work[i].Assignee, w.work[i].UpdatedAt = "Justin Guedes", w.now
	w.event(i)
	w.call(nil, false)
	if got := w.vault["CHAIN-001 - Confirm the MVP.md"]; !strings.Contains(got, "\n\"assignee\": \"Justin Guedes\"\n") {
		t.Fatalf("note:\n%s", got)
	}
}
