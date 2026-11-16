package main

import (
	"testing"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

// pullsPath is where the fake answers with pull requests.
const pullsPath = "/repos/Acme/app/pulls?state=all&sort=updated&direction=desc&per_page=50"

func movesOf(res *plugin.Response) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, op := range res.Ops {
		if op.Kind != "work.move" {
			continue
		}
		p := op.Params.(map[string]any)
		out[p["id"].(string)] = p
	}
	return out
}

func TestWorkMovesWithPullRequests(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	f := newFake("abc")
	f.bodies[pullsPath] = `[
		{"number":7,"title":"Fix the login screen","html_url":"https://github.com/Acme/app/pull/7","state":"closed","merged_at":"2026-11-12T09:00:00Z","updated_at":"2026-11-12T09:00:00Z","head":{"ref":"acm-12-login"}},
		{"number":8,"title":"ACM-3: dark mode","html_url":"https://github.com/Acme/app/pull/8","state":"open","draft":true,"updated_at":"2026-11-12T11:00:00Z","head":{"ref":"dark"}},
		{"number":10,"title":"ACM-5 search","html_url":"https://github.com/Acme/app/pull/10","state":"open","updated_at":"2026-11-12T11:00:00Z","head":{"ref":"search"}},
		{"number":11,"title":"ACM-6 old","html_url":"https://github.com/Acme/app/pull/11","state":"closed","merged_at":"2026-10-01T09:00:00Z","updated_at":"2026-10-01T09:00:00Z","head":{"ref":"old"}}]`
	work := []plugin.Work{
		{ID: "w12", Key: "ACM-12", ProjectID: "p1", StageKind: "review"},
		{ID: "w3", Key: "ACM-3", ProjectID: "p1", StageKind: "todo"},
		{ID: "w5", Key: "ACM-5", ProjectID: "p1", StageKind: "doing"},
		{ID: "w6", Key: "ACM-6", ProjectID: "p1", StageKind: "review"},
	}
	req := request(plugin.State{}, map[string]any{"repo": "acme/app"}, now)
	req.Work = work
	res := runWith(f, req)
	got := movesOf(res)
	if len(got) != 2 || got["w12"]["stage"] != "done" || got["w12"]["reason"] != "#7 merged" || got["w5"]["stage"] != "review" {
		// ACM-3 is a draft (stays), ACM-6 merged long before Enplace followed it.
		t.Fatalf("first check moves %+v", got)
	}
	if res.Ops[0].ProjectID != "p1" {
		t.Fatalf("op %+v", res.Ops[0])
	}

	// Nothing changed: nothing moves again, even where a person moved the
	// item back by hand.
	work[0].StageKind, work[2].StageKind = "doing", "review"
	req = request(stateOf(res), map[string]any{"repo": "acme/app"}, now.Add(15*time.Minute))
	req.Work = work
	res2 := runWith(newFakeWith(f), req)
	if len(res2.Ops) != 0 {
		t.Fatalf("moved again: %+v", res2.Ops)
	}

	// #10 closes without merging: ACM-5 goes back to doing with a note,
	// and ACM-6 merging again isn't new.
	f.bodies[pullsPath] = `[
		{"number":10,"title":"ACM-5 search","html_url":"https://github.com/Acme/app/pull/10","state":"closed","updated_at":"2026-11-12T12:10:00Z","head":{"ref":"search"}}]`
	req = request(stateOf(res2), map[string]any{"repo": "acme/app"}, now.Add(30*time.Minute))
	req.Work = work
	got = movesOf(runWith(newFakeWith(f), req))
	if len(got) != 1 || got["w5"]["stage"] != "doing" || got["w5"]["note"] == nil {
		t.Fatalf("closed moves %+v", got)
	}

	// Switched off: nothing moves.
	f.bodies[pullsPath] = `[
		{"number":12,"title":"ACM-3 dark mode","html_url":"https://github.com/Acme/app/pull/12","state":"open","updated_at":"2026-11-12T12:20:00Z","head":{"ref":"dark2"}}]`
	req = request(stateOf(res2), map[string]any{"repo": "acme/app", "move_work": false}, now.Add(45*time.Minute))
	req.Work = work
	if res := runWith(newFakeWith(f), req); len(res.Ops) != 0 {
		t.Fatalf("moved while off: %+v", res.Ops)
	}
}

func TestMergedWaitsForOtherOpenPullRequest(t *testing.T) {
	w := plugin.Work{ID: "w1", ProjectID: "p1", StageKind: "review"}
	merged := Link{Kind: "pr", Number: 1, URL: "u1", State: "merged"}
	open := Link{Kind: "pr", Number: 2, URL: "u2", State: "open"}
	if op := moveFor(w, []Link{merged, open}, []Link{merged}); op != nil {
		t.Fatalf("moved with #2 still open: %+v", op)
	}
	if op := moveFor(w, []Link{merged}, []Link{merged}); op == nil {
		t.Fatal("didn't move")
	}
	done := plugin.Work{ID: "w1", ProjectID: "p1", StageKind: "done"}
	if op := moveFor(done, []Link{open}, []Link{open}); op != nil {
		t.Fatalf("reopened done work: %+v", op)
	}
}

// newFakeWith is a fresh fake answering like f (its call count reset).
func newFakeWith(f *fakeGitHub) *fakeGitHub {
	return &fakeGitHub{bodies: f.bodies, status: f.status}
}
