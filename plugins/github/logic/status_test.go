package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

// statusOf is the pr.status button on a strip, if any, and its state.
func statusOf(b plugin.Block) (*plugin.Button, string) {
	for i := range b.Buttons {
		if b.Buttons[i].Action == actStatus {
			var a struct {
				State string `json:"state"`
			}
			_ = json.Unmarshal(b.Buttons[i].Args, &a)
			return &b.Buttons[i], a.State
		}
	}
	return nil, ""
}

func TestStatusSaysWhyNoMerge(t *testing.T) {
	pass, fail, runC := Check{"a", "pass"}, Check{"b", "fail"}, Check{"c", "run"}
	pr := func(merge string, cs ...Check) Link {
		return Link{Kind: "pr", Number: 9, State: "open", URL: "https://github.com/Acme/app/pull/9", Base: "main", Merge: merge, Checks: cs}
	}
	for _, c := range []struct {
		l     Link
		state string
		url   string
		pill  string
	}{
		{pr("clean", pass), "", "", "Checks pass"},
		{pr("dirty"), "conflicts", "/pull/9/conflicts", "Conflicts"},
		{pr("dirty", fail), "conflicts", "/pull/9/conflicts", "Conflicts"},
		{pr("clean", pass, fail), "failing", "/pull/9/checks", "Checks failing"},
		{pr("behind"), "behind", "/pull/9", "Open"},
		{pr("blocked"), "blocked", "/pull/9", "Open"},
		{pr("unstable", pass, runC), "waiting", "/pull/9", "Checks running"},
		{pr(""), "checking", "/pull/9", "Open"},
		{pr("unknown"), "checking", "/pull/9", "Open"},
		{Link{Kind: "pr", Number: 9, State: "draft", Merge: "dirty"}, "draft", "", "Draft"}, // mark it ready first (ENP-163)
		{Link{Kind: "pr", Number: 9, State: "merged"}, "", "", "Merged"},
	} {
		b := strip(c.l)
		btn, st := statusOf(b)
		if st != c.state {
			t.Errorf("%+v: state %q, want %q", c.l, st, c.state)
		}
		if btn != nil && !strings.HasSuffix(btn.URL, c.url) {
			t.Errorf("%s: url %q", c.state, btn.URL)
		}
		if b.Value != c.pill {
			t.Errorf("%+v: pill %q, want %q", c.l, b.Value, c.pill)
		}
		if b.Buttons[0].Label != "Open" {
			t.Errorf("Open must stay first: %+v", b.Buttons)
		}
	}
	if btn, _ := statusOf(strip(pr("dirty"))); btn.Label != "#9 has conflicts with main" {
		t.Errorf("label %q", btn.Label)
	}
}

// Every open pull request gets its merge state read in time, not only the
// first few by key (ENP-105: #128 was never read with 8 open).
func TestDetailsReachEveryOpenPR(t *testing.T) {
	f := newFake("abc")
	n := maxDetails + 5
	var prs []string
	for i := 1; i <= n; i++ {
		prs = append(prs, fmt.Sprintf(`{"number":%d,"title":"ACM-%d: thing","html_url":"https://github.com/Acme/app/pull/%d","state":"open","updated_at":"2026-11-12T11:00:00Z","head":{"ref":"b%d","sha":"s%d"}}`, 100+i, i, 100+i, i, i))
		f.bodies[fmt.Sprintf("/repos/Acme/app/pulls/%d", 100+i)] = `{"head":{"sha":"x"},"base":{"ref":"main"},"mergeable_state":"clean"}`
	}
	f.bodies["/repos/Acme/app/pulls?state=all&sort=updated&direction=desc&per_page=50"] = "[" + strings.Join(prs, ",") + "]"
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	res := runWith(f, request(plugin.State{}, map[string]any{"repo": "acme/app"}, now))
	res = runWith(f, request(stateOf(res), map[string]any{"repo": "acme/app"}, now.Add(15*time.Minute)))
	var ls Links
	stateOf(res).GetProject("p1", keyLinks, &ls)
	for i := 1; i <= n; i++ {
		if l := ls[fmt.Sprintf("ACM-%d", i)][0]; l.Merge != "clean" {
			t.Errorf("ACM-%d never read: %+v", i, l)
		}
	}
}

// A state GitHub hasn't worked out yet is asked again first next time.
func TestUnknownIsReadAgain(t *testing.T) {
	ls := Links{"ACM-1": {{Kind: "pr", Number: 1, State: "open", URL: "u1", Read: time.Unix(10, 0)}}, "ACM-2": {{Kind: "pr", Number: 2, State: "open", URL: "u2"}}}
	f := &fakeGitHub{bodies: map[string]string{
		"/repos/Acme/app/pulls/1": `{"mergeable_state":"clean"}`,
		"/repos/Acme/app/pulls/2": `{"mergeable_state":"unknown"}`,
	}, status: map[string]int{}}
	gh := &client{fetch: f.fetch, repo: "Acme/app"}
	now := time.Unix(1000, 0)
	details(gh, ls, false, now)
	if !ls["ACM-1"][0].Read.Equal(now) || !ls["ACM-2"][0].Read.IsZero() {
		t.Fatalf("read times %v %v", ls["ACM-1"][0].Read, ls["ACM-2"][0].Read)
	}
}
