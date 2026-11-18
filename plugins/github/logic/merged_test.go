package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

// twoKeys is a pull request whose title names two items, open and then
// merged (ENP-127: #142 named ENP-120 and ENP-119).
const (
	twoKeysOpen   = `[{"number":9,"title":"ACM-120, ACM-119: setup files","html_url":"https://github.com/Acme/app/pull/9","state":"open","updated_at":"2026-11-12T11:00:00Z","head":{"ref":"thread","sha":"h9"}}]`
	twoKeysMerged = `[{"number":9,"title":"ACM-120, ACM-119: setup files","html_url":"https://github.com/Acme/app/pull/9","state":"closed","merged_at":"2026-11-12T12:10:00Z","updated_at":"2026-11-12T12:10:00Z","head":{"ref":"thread","sha":"h9"}}]`
)

func twoKeysWork() []plugin.Work {
	return []plugin.Work{
		{ID: "w120", Key: "ACM-120", ProjectID: "p1", StageKind: "review"},
		{ID: "w119", Key: "ACM-119", ProjectID: "p1", StageKind: "review"},
	}
}

// openRead is a first read that sees the pull request open, both items
// already in review.
func openRead(t *testing.T, now time.Time) (*fakeGitHub, plugin.State) {
	t.Helper()
	f := newFake("abc")
	f.bodies[pullsPath] = twoKeysOpen
	req := request(plugin.State{}, map[string]any{"repo": "acme/app"}, now)
	req.Work = twoKeysWork()
	res := runWith(f, req)
	if len(res.Ops) != 0 {
		t.Fatalf("moved while open: %+v", res.Ops)
	}
	return f, stateOf(res)
}

func TestTwoKeysInATitleBothMoveWhenItMerges(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	f, st := openRead(t, now)
	f.bodies[pullsPath] = twoKeysMerged
	req := request(st, map[string]any{"repo": "acme/app"}, now.Add(15*time.Minute))
	req.Work = twoKeysWork()
	got := movesOf(runWith(newFakeWith(f), req))
	if len(got) != 2 || got["w120"]["stage"] != "done" || got["w119"]["stage"] != "done" || got["w119"]["reason"] != "#9 merged" {
		t.Fatalf("moves %+v", got)
	}
}

// pressMerge presses Merge on #9 with the state st.
func pressMerge(f *fakeGitHub, st plugin.State, now time.Time) *plugin.Response {
	req := request(st, map[string]any{"repo": "acme/app"}, now)
	req.Work = twoKeysWork()
	args, _ := json.Marshal(map[string]any{"number": 9, "sha": "h9"})
	req.Hook, req.Action = plugin.HookAction, &plugin.Action{Name: actMerge, Args: args}
	req.Secrets = map[string]string{"token": "tok"}
	r := &run{req: req, res: &plugin.Response{}, pid: req.ProjectID, fetch: f.fetch}
	r.merge()
	return r.res
}

func TestMergeButtonMovesWorkRightAway(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)

	// GitHub's list still says open just after the merge: both move anyway.
	f, st := openRead(t, now)
	f.bodies["/repos/Acme/app/pulls/9/merge"] = `{"merged":true}`
	f.bodies["/repos/Acme/app"] = f.bodies["/repos/acme/app"]
	res := pressMerge(f, st, now.Add(time.Minute))
	if got := movesOf(res); !merged(res) || len(got) != 2 || got["w120"]["stage"] != "done" || got["w119"]["stage"] != "done" {
		t.Fatalf("moves %+v (%+v)", got, res)
	}
	var ls Links
	stateOf(res).GetProject("p1", keyLinks, &ls)
	if ls["ACM-119"][0].State != "merged" {
		t.Fatalf("links %+v", ls)
	}

	// The read after merging fails: both move from what was known.
	f, st = openRead(t, now)
	f.bodies["/repos/Acme/app/pulls/9/merge"] = `{"merged":true}`
	f.status["/repos/Acme/app/commits/main"] = 502
	f.status["/repos/Acme/app"] = 502
	res = pressMerge(f, st, now.Add(time.Minute))
	if got := movesOf(res); !merged(res) || len(got) != 2 {
		t.Fatalf("moves after a failed read %+v (%+v)", got, res)
	}

	// The next read sees it merged too, and moves nothing again.
	f.bodies[pullsPath] = twoKeysMerged
	delete(f.status, "/repos/Acme/app/commits/main")
	delete(f.status, "/repos/Acme/app")
	req := request(stateOf(res), map[string]any{"repo": "acme/app"}, now.Add(16*time.Minute))
	req.Work = twoKeysWork()
	if res := runWith(newFakeWith(f), req); len(res.Ops) != 0 {
		t.Fatalf("moved again: %+v", res.Ops)
	}
}

func TestPanelSaysAMergedItemIsOnItsWay(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	f, st := openRead(t, now)
	f.bodies[pullsPath] = twoKeysMerged
	req := request(st, map[string]any{"repo": "acme/app"}, now.Add(15*time.Minute))
	req.Work = twoKeysWork()
	res := runWith(newFakeWith(f), req)

	panel := func(stage string, webhook *plugin.Webhook, at time.Time) []plugin.Block {
		req := request(stateOf(res), map[string]any{"repo": "acme/app"}, at)
		req.Work = twoKeysWork()
		req.Work[1].StageKind = stage
		req.Hook, req.Slots, req.WorkKey, req.Webhook = plugin.HookRender, []string{plugin.SlotWorkPanel}, "ACM-119", webhook
		out, _ := handle(req)
		return out.Cards[plugin.SlotWorkPanel]
	}
	// Still in review just after merging: it says the move is on its way.
	b := panel("review", &plugin.Webhook{On: true}, now.Add(16*time.Minute))
	if b[0].Text != "Merged on GitHub, moving to Done soon" || b[0].Label != "PR #9" {
		t.Fatalf("panel %+v", b)
	}
	if last := b[len(b)-1]; strings.Contains(last.Text, "Instant updates are off") {
		t.Fatalf("says off while on: %+v", last)
	}
	// Done, or long after: nothing extra.
	for _, b := range [][]plugin.Block{panel("done", nil, now.Add(16*time.Minute)), panel("review", nil, now.Add(3*time.Hour))} {
		if strings.Contains(b[0].Text, "moving to Done") {
			t.Fatalf("panel %+v", b)
		}
	}
	// Without instant updates it says reads are every 15 minutes.
	b = panel("done", nil, now.Add(16*time.Minute))
	if last := b[len(b)-1]; !strings.Contains(last.Text, "Instant updates are off") {
		t.Fatalf("doesn't say instant updates are off: %+v", last)
	}
}
