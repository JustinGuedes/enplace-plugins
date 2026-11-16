package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

// A branch that mentions a key but never had a pull request shows on its
// item with a way to open one; branches with a pull request, old ones and
// ones that mention nothing don't (ENP-106).
func TestBranchWithoutPullRequest(t *testing.T) {
	f := newFake("abc")
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	commit := func(msg, date string) string {
		return `{"sha":"s","commit":{"message":"` + msg + `","committer":{"date":"` + date + `"}}}`
	}
	f.bodies["/repos/Acme/app/branches?per_page=100"] = `[
		{"name":"main","commit":{"sha":"abc"}},
		{"name":"claude/quiet-fox","commit":{"sha":"b1"}},
		{"name":"acm-12-login","commit":{"sha":"b2"}},
		{"name":"old","commit":{"sha":"b3"}},
		{"name":"notes","commit":{"sha":"b4"}},
		{"name":"merged-long-ago","commit":{"sha":"b5"}}]`
	f.bodies["/repos/Acme/app/commits/b1"] = commit(`ACM-20: Let agents claim work\n\nMore`, "2026-11-12T08:00:00Z")
	f.bodies["/repos/Acme/app/commits/b3"] = commit("ACM-21: stale", "2026-09-01T08:00:00Z")
	f.bodies["/repos/Acme/app/commits/b4"] = commit("Just notes", "2026-11-12T08:00:00Z")
	f.bodies["/repos/Acme/app/commits/b5"] = commit("ACM-22: done before", "2026-11-11T08:00:00Z")
	f.bodies["/repos/Acme/app/pulls?state=all&per_page=1&head=Acme%3Aclaude%2Fquiet-fox"] = `[]`
	f.bodies["/repos/Acme/app/pulls?state=all&per_page=1&head=Acme%3Amerged-long-ago"] = `[{"number":2}]`

	res := runWith(f, request(plugin.State{}, map[string]any{"repo": "acme/app"}, now))
	var ls Links
	stateOf(res).GetProject("p1", keyLinks, &ls)
	b := ls["ACM-20"]
	if len(b) != 1 || b[0].Kind != "branch" || b[0].Branch != "claude/quiet-fox" || b[0].Title != "ACM-20: Let agents claim work" ||
		b[0].URL != "https://github.com/Acme/app/compare/main...claude/quiet-fox?expand=1" {
		t.Fatalf("ACM-20: %+v", b)
	}
	for _, k := range []string{"ACM-21", "ACM-22"} {
		if _, ok := ls[k]; ok {
			t.Fatalf("%s linked: %+v", k, ls[k])
		}
	}
	for _, l := range ls["ACM-12"] {
		if l.Kind == "branch" {
			t.Fatal("acm-12-login has a pull request")
		}
	}

	// The item's panel offers to open a pull request, and tells the card why.
	req := request(stateOf(res), map[string]any{"repo": "acme/app"}, now)
	req.Hook, req.Slots, req.WorkKey = plugin.HookRender, []string{plugin.SlotWorkPanel}, "ACM-20"
	out, _ := handle(req)
	s := out.Cards[plugin.SlotWorkPanel][0]
	if s.Type != plugin.BlockStrip || s.Tone != "warn" || s.Label != "Branch claude/quiet-fox" || s.Buttons[0].Label != "Open a pull request" ||
		s.Buttons[1].Action != actStatus || !strings.Contains(string(s.Buttons[1].Args), `"nopr"`) {
		t.Fatalf("strip %+v", s)
	}

	// A branch tip is read once; the next run doesn't ask again.
	f.calls = nil
	res = runWith(f, request(stateOf(res), map[string]any{"repo": "acme/app"}, now.Add(time.Hour)))
	for _, c := range f.calls {
		if strings.HasPrefix(c, "/repos/Acme/app/commits/b") {
			t.Fatalf("read %s again", c)
		}
	}
	// Once it has a pull request, the branch link goes.
	f.bodies["/repos/Acme/app/pulls?state=all&sort=updated&direction=desc&per_page=50"] = `[{"number":10,"title":"Claim work","html_url":"u10","state":"open","updated_at":"2026-11-12T12:30:00Z","head":{"ref":"claude/quiet-fox","sha":"b1"}}]`
	res = runWith(f, request(stateOf(res), map[string]any{"repo": "acme/app"}, now.Add(2*time.Hour)))
	ls = nil
	stateOf(res).GetProject("p1", keyLinks, &ls)
	for _, l := range ls["ACM-20"] {
		if l.Kind == "branch" {
			t.Fatalf("branch link stayed after its pull request: %+v", ls["ACM-20"])
		}
	}
}

// Today's brief names work stuck on GitHub, and says nothing when none is.
func TestStuckBrief(t *testing.T) {
	ls := Links{
		"ACM-1": {{Kind: "pr", State: "open", Number: 1, Merge: "dirty"}},
		"ACM-2": {{Kind: "pr", State: "open", Number: 2, Merge: "dirty"}},
		"ACM-3": {{Kind: "branch", Branch: "x"}},
		"ACM-4": {{Kind: "pr", State: "open", Number: 4, Merge: "clean"}},
		"ACM-5": {{Kind: "pr", State: "open", Number: 5, Merge: "dirty"}},
	}
	var st plugin.State
	res := &plugin.Response{}
	res.SetProject("p1", keyLinks, ls)
	st.Projects = res.State.Projects
	req := &plugin.Request{Hook: plugin.HookRender, Slots: []string{plugin.SlotBrief}, State: st,
		Work: []plugin.Work{{Key: "ACM-1", StageKind: "review"}, {Key: "ACM-2", StageKind: "review"}, {Key: "ACM-3", StageKind: "review"},
			{Key: "ACM-4", StageKind: "review"}, {Key: "ACM-5", StageKind: "done"}}}
	out, _ := handle(req)
	if out.Brief == nil || out.Brief.Text != "Code waiting on a fix: ACM-1 and ACM-2 have conflicts and ACM-3 has code with no pull request." ||
		out.Brief.Button.Href != "/?item=ACM-1" || out.Signals[plugin.SlotBrief].Level != plugin.LevelLoud {
		t.Fatalf("brief %+v", out.Brief)
	}
	req.Work = []plugin.Work{{Key: "ACM-4", StageKind: "review"}}
	if out, _ := handle(req); out.Brief != nil {
		t.Fatalf("brief with nothing stuck: %+v", out.Brief)
	}
}

// Code already on the default branch never asks for a pull request
// (ENP-124): a branch merged straight in, a merge commit that only names
// another branch, and main itself are left out.
func TestBranchAlreadyLanded(t *testing.T) {
	f := newFake("abc")
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	commit := func(msg string) string {
		return `{"sha":"s","commit":{"message":"` + msg + `","committer":{"date":"2026-11-12T08:00:00Z"}}}`
	}
	f.bodies["/repos/Acme/app/branches?per_page=100"] = `[
		{"name":"main","commit":{"sha":"abc"}},
		{"name":"master","commit":{"sha":"m1"}},
		{"name":"release","commit":{"sha":"r1"}},
		{"name":"claude/acm-31-ios","commit":{"sha":"i1"}},
		{"name":"claude/acm-32-web","commit":{"sha":"w1"}}]`
	f.bodies["/repos/Acme/app/commits/m1"] = commit("ACM-33: on master")
	f.bodies["/repos/Acme/app/commits/r1"] = commit(`Merge remote-tracking branch 'origin/claude/acm-30-ios'`)
	f.bodies["/repos/Acme/app/commits/i1"] = commit("ACM-31: iPhone app")
	f.bodies["/repos/Acme/app/commits/w1"] = commit("ACM-32: web app")
	for _, b := range []string{"claude%2Facm-31-ios", "claude%2Facm-32-web"} {
		f.bodies["/repos/Acme/app/pulls?state=all&per_page=1&head=Acme%3A"+b] = `[]`
	}
	f.bodies["/repos/Acme/app/compare/main...claude/acm-31-ios"] = `{"status":"behind"}`
	f.bodies["/repos/Acme/app/compare/main...claude/acm-32-web"] = `{"status":"ahead"}`

	res := runWith(f, request(plugin.State{}, map[string]any{"repo": "acme/app"}, now))
	var ls Links
	stateOf(res).GetProject("p1", keyLinks, &ls)
	branches := func(k string) (out []string) {
		for _, l := range ls[k] {
			if l.Kind == "branch" {
				out = append(out, l.Branch)
			}
		}
		return out
	}
	for _, k := range []string{"ACM-30", "ACM-31", "ACM-33"} {
		if b := branches(k); len(b) > 0 {
			t.Fatalf("%s has branch links %v", k, b)
		}
	}
	if b := branches("ACM-32"); len(b) != 1 || b[0] != "claude/acm-32-web" {
		t.Fatalf("ACM-32: %v", b)
	}

	// Merged later, without its tip moving: the next run sees it landed.
	f.bodies["/repos/Acme/app/compare/main...claude/acm-32-web"] = `{"status":"identical"}`
	res = runWith(f, request(stateOf(res), map[string]any{"repo": "acme/app"}, now.Add(time.Hour)))
	ls = nil
	stateOf(res).GetProject("p1", keyLinks, &ls)
	if b := branches("ACM-32"); len(b) > 0 {
		t.Fatalf("ACM-32 still asks for a pull request: %v", b)
	}

	// Deleted: its link goes with it on the next run.
	f.bodies["/repos/Acme/app/compare/main...claude/acm-32-web"] = `{"status":"ahead"}`
	res = runWith(f, request(plugin.State{}, map[string]any{"repo": "acme/app"}, now))
	f.bodies["/repos/Acme/app/branches?per_page=100"] = `[{"name":"main","commit":{"sha":"abc"}}]`
	res = runWith(f, request(stateOf(res), map[string]any{"repo": "acme/app"}, now.Add(time.Hour)))
	ls = nil
	stateOf(res).GetProject("p1", keyLinks, &ls)
	if b := branches("ACM-32"); len(b) > 0 {
		t.Fatalf("deleted branch still linked: %v", b)
	}
}

// When the default branch it knew is gone (deleted or renamed), the check
// looks the repo up again instead of failing until the next day (ENP-124).
func TestDefaultBranchGone(t *testing.T) {
	f := newFake("abc")
	f.bodies["/repos/Acme/app"] = f.bodies["/repos/acme/app"]
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	res := runWith(f, request(plugin.State{}, map[string]any{"repo": "acme/app"}, now))
	var c *Code
	st := stateOf(res)
	st.GetProject("p1", keyCode, &c)
	c.DefaultBranch, c.Branch = "claude/first-push", "claude/first-push"
	st.Projects["p1"][keyCode], _ = json.Marshal(c)
	res = runWith(f, request(st, map[string]any{"repo": "acme/app"}, now.Add(time.Hour)))
	c = nil
	stateOf(res).GetProject("p1", keyCode, &c)
	if c.Error != "" || c.Branch != "main" || c.DefaultBranch != "main" {
		t.Fatalf("code: branch %q default %q error %q", c.Branch, c.DefaultBranch, c.Error)
	}
}
