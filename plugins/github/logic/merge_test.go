package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

func TestMergeButtonOnlyWhenGitHubSaysItCan(t *testing.T) {
	pass, fail := Check{"a", "pass"}, Check{"b", "fail"}
	for _, c := range []struct {
		l    Link
		want bool
	}{
		{Link{Kind: "pr", Number: 9, State: "open", Merge: "clean", Base: "main", Checks: []Check{pass}}, true},
		{Link{Kind: "pr", Number: 9, State: "open", Merge: "has_hooks"}, true},
		{Link{Kind: "pr", Number: 9, State: "open", Merge: "clean", Checks: []Check{pass, fail}}, false},
		{Link{Kind: "pr", Number: 9, State: "open", Merge: "blocked"}, false},
		{Link{Kind: "pr", Number: 9, State: "open", Merge: "dirty"}, false},
		{Link{Kind: "pr", Number: 9, State: "open"}, false},
		{Link{Kind: "pr", Number: 9, State: "draft", Merge: "clean"}, false},
		{Link{Kind: "pr", Number: 9, State: "merged", Merge: "clean"}, false},
	} {
		b := strip(c.l)
		var merge *plugin.Button
		for i := range b.Buttons {
			if b.Buttons[i].Action == actMerge {
				merge = &b.Buttons[i]
			}
		}
		if (merge != nil) != c.want {
			t.Errorf("%+v: merge button %v, want %v", c.l, merge != nil, c.want)
		}
		if merge != nil && c.l.Base == "main" && merge.Confirm != "Merge #9 into main?" {
			t.Errorf("confirm %q", merge.Confirm)
		}
		if b.Buttons[0].Label != "Open" {
			t.Errorf("Open must stay first: %+v", b.Buttons)
		}
	}
}

func TestDetailsReadMergeState(t *testing.T) {
	f := newFake("abc")
	f.bodies["/repos/Acme/app/pulls/8"] = `{"additions":1,"deletions":1,"head":{"sha":"h8"},"base":{"ref":"main"},"mergeable_state":"clean"}`
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	res := runWith(f, request(plugin.State{}, map[string]any{"repo": "acme/app"}, now))
	var ls Links
	stateOf(res).GetProject("p1", keyLinks, &ls)
	if l := ls["ACM-3"][0]; l.Merge != "clean" || l.Base != "main" {
		t.Fatalf("merge state %+v", l)
	}
}

// mergeReq is a press of Merge on #9 after a check has linked the repo.
func mergeReq(t *testing.T, f *fakeGitHub, token string) *plugin.Response {
	t.Helper()
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	res := runWith(f, request(plugin.State{}, map[string]any{"repo": "acme/app"}, now))
	req := request(stateOf(res), map[string]any{"repo": "acme/app"}, now)
	args, _ := json.Marshal(map[string]any{"number": 9, "sha": "h9"})
	req.Hook, req.Action = plugin.HookAction, &plugin.Action{Name: actMerge, Args: args}
	if token != "" {
		req.Secrets = map[string]string{"token": token}
	}
	r := &run{req: req, res: &plugin.Response{}, pid: req.ProjectID, fetch: f.fetch}
	r.merge()
	return r.res
}

func merged(res *plugin.Response) bool {
	m, _ := res.Data.(map[string]any)
	return m["merged"] == true
}

func TestMergeAction(t *testing.T) {
	// Without a token it says what the token needs, and asks GitHub nothing.
	f := newFake("abc")
	res := mergeReq(t, f, "")
	if merged(res) || !strings.Contains(res.Toast, "Pull requests: read and write") {
		t.Fatalf("no token: %+v", res)
	}
	for _, c := range f.calls {
		if strings.HasSuffix(c, "/merge") {
			t.Fatal("merged without a token")
		}
	}

	// It merges, then reads the repo again so the work moves.
	f = newFake("abc")
	f.bodies["/repos/Acme/app/pulls/9/merge"] = `{"merged":true}`
	res = mergeReq(t, f, "tok")
	if !merged(res) || res.Toast != "Merged #9" {
		t.Fatalf("merge: %+v", res)
	}
	if res.State == nil {
		t.Fatal("didn't read the repo again after merging")
	}

	// GitHub's own reason comes through, and a token that can't merge says so.
	for status, want := range map[int]string{405: "won't merge #9 yet", 409: "changed since", 403: "Pull requests: read and write"} {
		f = newFake("abc")
		f.status["/repos/Acme/app/pulls/9/merge"] = status
		if res := mergeReq(t, f, "tok"); merged(res) || !strings.Contains(res.Toast, want) {
			t.Errorf("%d: %+v", status, res)
		}
	}
}
