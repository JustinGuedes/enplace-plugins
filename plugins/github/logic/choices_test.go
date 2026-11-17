package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

func choicesFor(t *testing.T, f *fakeGitHub, action, token string, values map[string]any) plugin.Choices {
	t.Helper()
	req := request(plugin.State{}, map[string]any{}, time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC))
	req.Hook = plugin.HookAction
	args, _ := json.Marshal(plugin.ChoicesArgs{Setting: "repo", Values: values})
	req.Action = &plugin.Action{Name: action, Args: args}
	if token != "" {
		req.Secrets = map[string]string{"token": token}
	}
	r := &run{req: req, res: &plugin.Response{}, pid: req.ProjectID, fetch: f.fetch}
	r.choices(action)
	out, ok := r.res.Data.(plugin.Choices)
	if !ok {
		t.Fatalf("data %T", r.res.Data)
	}
	return out
}

// The repo setting lists the repos the token can see, newest first, and
// leaves archived ones out (ENP-187).
func TestRepoChoices(t *testing.T) {
	f := newFake("abc")
	if out := choicesFor(t, f, actRepos, "", nil); len(out.Choices) != 0 || out.Note == "" || len(f.calls) != 0 {
		t.Fatalf("without a token: %+v, calls %v", out, f.calls)
	}
	f.bodies["/user/repos?page=1&per_page=100&sort=pushed"] = `[
		{"full_name":"Acme/app","private":true,"pushed_at":"2026-11-17T10:00:00Z","owner":{"login":"Acme"}},
		{"full_name":"Acme/old","archived":true,"owner":{"login":"Acme"}},
		{"full_name":"me/site","pushed_at":"2026-11-10T12:00:00Z","owner":{"login":"me"}}]`
	out := choicesFor(t, f, actRepos, "t", nil)
	want := []plugin.Choice{
		{Value: "Acme/app", Group: "Acme", Detail: "2 h ago", Badge: "Private"},
		{Value: "me/site", Group: "me", Detail: "7 days ago"},
	}
	if len(out.Choices) != len(want) || out.Choices[0] != want[0] || out.Choices[1] != want[1] || out.Note != "" {
		t.Fatalf("repos: %+v", out)
	}
	f.status["/user/repos?page=1&per_page=100&sort=pushed"] = 401
	if out := choicesFor(t, f, actRepos, "bad", nil); len(out.Choices) != 0 || out.Note == "" {
		t.Fatalf("a refused token: %+v", out)
	}
}

// The branch setting lists the branches of the repo on screen, the default
// one first.
func TestBranchChoices(t *testing.T) {
	f := newFake("abc")
	if out := choicesFor(t, f, actBranches, "", nil); len(out.Choices) != 0 || out.Note != "Pick the repo first." {
		t.Fatalf("no repo: %+v", out)
	}
	f.bodies["/repos/Acme/app/branches?page=1&per_page=100"] = `[{"name":"dev"},{"name":"main"},{"name":"release"}]`
	out := choicesFor(t, f, actBranches, "", map[string]any{"repo": "https://github.com/acme/app"})
	if len(out.Choices) != 3 || out.Choices[0] != (plugin.Choice{Value: "main", Badge: "Default"}) || out.Choices[1].Value != "dev" {
		t.Fatalf("branches: %+v", out)
	}
}
