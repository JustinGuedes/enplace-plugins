package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

// fakeGitHub answers API paths with canned bodies and counts the calls.
type fakeGitHub struct {
	bodies map[string]string
	status map[string]int
	calls  []string
}

func (f *fakeGitHub) fetch(r plugin.FetchRequest) (*plugin.FetchResponse, error) {
	p := strings.TrimPrefix(r.URL, api)
	f.calls = append(f.calls, p)
	if s, ok := f.status[p]; ok {
		return &plugin.FetchResponse{Status: s}, nil
	}
	b, ok := f.bodies[p]
	if !ok {
		return &plugin.FetchResponse{Status: 404}, nil
	}
	return &plugin.FetchResponse{Status: 200, Body: []byte(b)}, nil
}

func newFake(sha string) *fakeGitHub {
	return &fakeGitHub{bodies: map[string]string{
		"/repos/acme/app":                                      `{"full_name":"Acme/app","html_url":"https://github.com/Acme/app","default_branch":"main"}`,
		"/repos/Acme/app/commits/main":                         `{"sha":"` + sha + `","html_url":"https://github.com/Acme/app/commit/` + sha + `","commit":{"message":"Fix login\n\nMore","committer":{"date":"2026-11-12T10:00:00Z"}}}`,
		"/repos/Acme/app/git/trees/" + sha + "?recursive=1":    `{"tree":[{"path":"README.md","type":"blob"},{"path":"ios","type":"tree"},{"path":"ios/Info.plist","type":"blob"},{"path":"ios/project.yml","type":"blob"},{"path":"web/package.json","type":"blob"}]}`,
		"/repos/Acme/app/contents/ios/project.yml?ref=" + sha:  "settings:\n  MARKETING_VERSION: 1.5\n",
		"/repos/Acme/app/contents/web/package.json?ref=" + sha: `{"version":"0.0.1"}`,
		"/repos/Acme/app/tags?per_page=100":                    `[{"name":"latest","commit":{"sha":"z"}},{"name":"v1.4","commit":{"sha":"t14"}},{"name":"v1.10.0","commit":{"sha":"t110"}},{"name":"v1.9","commit":{"sha":"t19"}}]`,
		"/repos/Acme/app/compare/t110..." + sha:                `{"ahead_by":12}`,
		"/repos/Acme/app/pulls?state=all&sort=updated&direction=desc&per_page=50": `[
			{"number":7,"title":"Fix the login screen","html_url":"https://github.com/Acme/app/pull/7","state":"closed","merged_at":"2026-11-12T09:00:00Z","updated_at":"2026-11-12T09:00:00Z","head":{"ref":"acm-12-login"}},
			{"number":8,"title":"ACM-3: dark mode","html_url":"https://github.com/Acme/app/pull/8","state":"open","draft":true,"updated_at":"2026-11-12T11:00:00Z","head":{"ref":"dark"}},
			{"number":9,"title":"Unrelated","html_url":"https://github.com/Acme/app/pull/9","state":"open","updated_at":"2026-11-12T11:00:00Z","head":{"ref":"other-acme-4"}}]`,
		"/repos/Acme/app/commits?per_page=50&sha=main": `[{"sha":"` + sha + `","html_url":"https://github.com/Acme/app/commit/` + sha + `","commit":{"message":"Fix login (ACM-12, acm-013)","committer":{"date":"2026-11-12T10:00:00Z"}}}]`,
	}, status: map[string]int{}}
}

func request(state plugin.State, settings map[string]any, now time.Time) *plugin.Request {
	return &plugin.Request{Hook: plugin.HookSchedule, Now: now, ProjectID: "p1",
		Projects:        []plugin.Project{{ID: "p1", Key: "ACM", On: true}},
		ProjectSettings: map[string]map[string]any{"p1": settings}, State: state}
}

func runWith(f *fakeGitHub, req *plugin.Request) *plugin.Response {
	r := &run{req: req, res: &plugin.Response{}, pid: req.ProjectID, fetch: f.fetch}
	r.save(r.check(false))
	return r.res
}

func stateOf(res *plugin.Response) plugin.State {
	return plugin.State{Projects: res.State.Projects}
}

func TestCheckFindsVersionAndTag(t *testing.T) {
	f := newFake("abc")
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	res := runWith(f, request(plugin.State{}, map[string]any{"repo": "https://github.com/acme/app.git"}, now))
	var c Code
	if !stateOf(res).GetProject("p1", keyCode, &c) {
		t.Fatal("nothing saved")
	}
	if c.Error != "" || c.Version != "1.5" || c.File != "ios/project.yml" || !c.Auto || c.Tag != "v1.10.0" || c.Ahead != 12 || c.Message != "Fix login" {
		t.Fatalf("got %+v", c)
	}
	var pub Public
	if !stateOf(res).GetProject("p1", keyPublic, &pub) || pub.Version != "1.5" || pub.TagVersion != "1.10.0" || pub.Provider != "github" {
		t.Fatalf("public %+v", pub)
	}

	// Nothing moved: only the head, pull requests (with the open one that
	// mentions work) and the tags are asked for.
	f.calls = nil
	res = runWith(f, request(stateOf(res), map[string]any{"repo": "https://github.com/acme/app.git"}, now.Add(15*time.Minute)))
	if len(f.calls) != 4 {
		t.Fatalf("calls %v", f.calls)
	}
	stateOf(res).GetProject("p1", keyCode, &c)
	if c.Version != "1.5" || c.Ahead != 12 {
		t.Fatalf("lost what it knew: %+v", c)
	}
}

func TestNamedFileAndErrors(t *testing.T) {
	f := newFake("abc")
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	res := runWith(f, request(plugin.State{}, map[string]any{"repo": "acme/app", "version_file": "web/package.json"}, now))
	var c Code
	stateOf(res).GetProject("p1", keyCode, &c)
	if c.Version != "0.0.1" || c.Auto || c.File != "web/package.json" {
		t.Fatalf("got %+v", c)
	}

	// The token stops working: the error shows, the version stays.
	f.status["/repos/Acme/app/commits/main"] = 401
	res = runWith(f, request(stateOf(res), map[string]any{"repo": "acme/app", "version_file": "web/package.json"}, now.Add(time.Hour)))
	stateOf(res).GetProject("p1", keyCode, &c)
	if !strings.Contains(c.Error, "token") || c.Version != "0.0.1" {
		t.Fatalf("got %+v", c)
	}

	// A repo that isn't there.
	res = runWith(newFake("abc"), request(plugin.State{}, map[string]any{"repo": "acme/nope"}, now))
	stateOf(res).GetProject("p1", keyCode, &c)
	if !strings.Contains(c.Error, "Can't find acme/nope") {
		t.Fatalf("got %+v", c)
	}
	if raw := res.State.Projects["p1"][keyPublic]; string(raw) != "null" {
		t.Fatalf("published %s", raw)
	}
}

func TestParseRepo(t *testing.T) {
	for in, want := range map[string]string{
		"acme/app":                           "acme/app",
		"https://github.com/acme/app":        "acme/app",
		"github.com/acme/app.git":            "acme/app",
		"git@github.com:acme/my.app.git":     "acme/my.app",
		"https://github.com/acme/app/tree/x": "acme/app",
		"not a repo":                         "",
		"https://gitlab.com/acme/app":        "",
	} {
		if got := parseRepo(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

func TestRenderCard(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	c := Code{Repo: "Acme/app", Typed: "acme/app", URL: "https://github.com/Acme/app", Branch: "main", File: "ios/project.yml", Version: "1.5", Tag: "v1.4", Ahead: 3, Message: "Fix", CommitAt: now.Add(-2 * time.Hour), CheckedAt: now}
	raw, _ := json.Marshal(c)
	req := request(plugin.State{Projects: map[string]map[string]json.RawMessage{"p1": {keyCode: raw}}}, map[string]any{"repo": "acme/app"}, now)
	req.Hook, req.Slots = plugin.HookRender, []string{"project_card:code", plugin.SlotSettingsPanel}
	res, _ := handle(req)
	card := res.Cards["project_card:code"]
	if len(card) == 0 || card[0].Value != "1.5" || card[0].Sub != "ios/project.yml · main · 3 commits since v1.4" {
		t.Fatalf("card %+v", card)
	}
	if res.Signals["project_card:code"].Level != plugin.LevelQuiet {
		t.Fatal("new code shouldn't be loud")
	}
	if len(res.Cards[plugin.SlotSettingsPanel]) == 0 {
		t.Fatal("no panel")
	}
}

func TestLinksToWork(t *testing.T) {
	f := newFake("abc")
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	res := runWith(f, request(plugin.State{}, map[string]any{"repo": "acme/app"}, now))
	var ls Links
	stateOf(res).GetProject("p1", keyLinks, &ls)
	if len(ls["ACM-12"]) != 2 || ls["ACM-12"][0].Kind != "commit" || ls["ACM-12"][1].State != "merged" || ls["ACM-12"][1].Branch != "acm-12-login" {
		t.Fatalf("ACM-12: %+v", ls["ACM-12"])
	}
	if len(ls["ACM-3"]) != 1 || ls["ACM-3"][0].State != "draft" || len(ls["ACM-13"]) != 1 {
		t.Fatalf("links %+v", ls)
	}
	if _, ok := ls["ACM-4"]; ok {
		t.Fatal("acme-4 isn't ACM-4")
	}

	// The panel on ACM-12 shows the pull request as a strip, the commit,
	// and what happened as events; ACM-99 says how it links itself.
	req := request(stateOf(res), map[string]any{"repo": "acme/app"}, now)
	req.Hook, req.Slots, req.WorkKey = plugin.HookRender, []string{plugin.SlotWorkPanel}, "ACM-12"
	out, _ := handle(req)
	blocks := out.Cards[plugin.SlotWorkPanel]
	if len(blocks) != 4 || blocks[0].Type != plugin.BlockStrip || blocks[0].Label != "PR #7" || blocks[0].Value != "Merged" || blocks[0].Tone != "done" ||
		blocks[1].Type != plugin.BlockList || len(blocks[1].Items) != 1 || blocks[2].Type != plugin.BlockEvents || len(blocks[2].Items) != 2 ||
		blocks[2].Items[1].Title != "Merged the pull request" || blocks[2].Items[1].Value != "2026-11-12T09:00:00Z" || !strings.Contains(blocks[3].Text, "moves with its pull request") {
		t.Fatalf("panel %+v", blocks)
	}
	req.WorkKey = "ACM-99"
	out, _ = handle(req)
	if b := out.Cards[plugin.SlotWorkPanel]; len(b) != 1 || b[0].Type != plugin.BlockStrip || b[0].Tone != "waiting" || !strings.Contains(b[0].Sub, "mentions ACM-99") {
		t.Fatalf("empty panel %+v", b)
	}

	// Another repo starts afresh.
	f2 := newFake("abc")
	f2.bodies["/repos/acme/other"] = `{"full_name":"acme/other","default_branch":"main"}`
	f2.bodies["/repos/acme/other/commits/main"] = `{"sha":"x1","commit":{"message":"init","committer":{"date":"2026-11-12T10:00:00Z"}}}`
	f2.bodies["/repos/acme/other/git/trees/x1?recursive=1"] = `{"tree":[]}`
	f2.bodies["/repos/acme/other/pulls?state=all&sort=updated&direction=desc&per_page=50"] = `[]`
	f2.bodies["/repos/acme/other/commits?per_page=50&sha=main"] = `[]`
	f2.bodies["/repos/acme/other/tags?per_page=100"] = `[]`
	res = runWith(f2, request(stateOf(res), map[string]any{"repo": "acme/other"}, now))
	ls = nil
	stateOf(res).GetProject("p1", keyLinks, &ls)
	if len(ls) != 0 {
		t.Fatalf("kept links from the old repo: %+v", ls)
	}
}

func TestMentions(t *testing.T) {
	re := keyRe("ENP")
	got := mentions(re, "feature/enp-12-login", "ENP 2 and ENP-007 and #ENP-3", "OPENP-5 xENP-6", "ENP_9", "ENP-20 ENP-21, ENP-0")
	want := []string{"ENP-12", "ENP-7", "ENP-3", "ENP-9", "ENP-20", "ENP-21"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestOpenPullRequestDetails(t *testing.T) {
	f := newFake("abc")
	f.bodies["/repos/Acme/app/pulls/8"] = `{"additions":212,"deletions":40,"head":{"sha":"h8"}}`
	f.bodies["/repos/Acme/app/commits/h8/check-runs?per_page=50"] = `{"check_runs":[
		{"name":"test","status":"completed","conclusion":"success"},
		{"name":"vet","status":"completed","conclusion":"success"},
		{"name":"web","status":"completed","conclusion":"skipped"},
		{"name":"build","status":"in_progress"}]}`
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	res := runWith(f, request(plugin.State{}, map[string]any{"repo": "acme/app"}, now))
	var ls Links
	stateOf(res).GetProject("p1", keyLinks, &ls)
	l := ls["ACM-3"][0]
	if l.Adds != 212 || l.Dels != 40 || len(l.Checks) != 4 || l.facts() != "Draft · 3 of 4 checks · +212 −40" {
		t.Fatalf("details %+v (%s)", l, l.facts())
	}
	if b := strip(l); b.Value != "Draft" || b.Tone != "open" || len(b.Items) != 4 || b.Items[0].Title != "build" || b.Items[0].Done || b.Items[0].Locked {
		t.Fatalf("strip %+v", b)
	}

	// GitHub can't be reached for details next time: what was read stays.
	delete(f.bodies, "/repos/Acme/app/pulls/8")
	res = runWith(f, request(stateOf(res), map[string]any{"repo": "acme/app"}, now.Add(15*time.Minute)))
	ls = nil
	stateOf(res).GetProject("p1", keyLinks, &ls)
	if l := ls["ACM-3"][0]; l.Adds != 212 || len(l.Checks) != 4 {
		t.Fatalf("lost details %+v", l)
	}
}

func TestStripStates(t *testing.T) {
	pass, fail, run := Check{"a", "pass"}, Check{"b", "fail"}, Check{"c", "run"}
	for _, c := range []struct {
		l           Link
		value, tone string
	}{
		{Link{State: "open"}, "Open", "open"},
		{Link{State: "open", Checks: []Check{pass, run}}, "Checks running", "open"},
		{Link{State: "open", Checks: []Check{pass, pass}}, "Checks pass", "good"},
		{Link{State: "draft", Checks: []Check{pass, fail}}, "Checks failing", "warn"},
		{Link{State: "draft"}, "Draft", "open"},
		{Link{State: "merged", Checks: []Check{fail}}, "Merged", "done"},
		{Link{State: "closed"}, "Closed", "muted"},
	} {
		if b := strip(c.l); b.Value != c.value || b.Tone != c.tone {
			t.Errorf("%+v: got %s/%s", c.l, b.Value, b.Tone)
		}
	}
}
