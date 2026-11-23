package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

func hookRun(req *plugin.Request, f fetcher) *run {
	return &run{req: req, res: &plugin.Response{}, pid: req.ProjectID, fetch: f}
}

func TestWebhookRefreshesOnlyForItsRepo(t *testing.T) {
	for _, c := range []struct {
		event, repo string
		refresh     bool
	}{
		{"push", "Acme/App", true},
		{"pull_request", "acme/app", true},
		{"release", "acme/app", true},
		{"ping", "acme/app", false},
		{"issues", "acme/app", false},
		{"push", "other/app", false},
	} {
		req := request(plugin.State{}, map[string]any{"repo": "acme/app"}, time.Now())
		req.Hook = plugin.HookWebhook
		req.Webhook = &plugin.Webhook{On: true, Delivery: &plugin.Delivery{Event: c.event, Body: json.RawMessage(`{"repository":{"full_name":"` + c.repo + `"}}`)}}
		r := hookRun(req, nil)
		r.webhook()
		if r.res.Refresh != c.refresh {
			t.Errorf("%s on %s: refresh %v", c.event, c.repo, r.res.Refresh)
		}
	}
}

func TestConnectAddsAndReplacesHook(t *testing.T) {
	var calls []string
	var sent map[string]any
	f := func(r plugin.FetchRequest) (*plugin.FetchResponse, error) {
		calls = append(calls, r.Method+" "+strings.TrimPrefix(r.URL, api))
		if r.Headers["Authorization"] != "Bearer tok" {
			t.Errorf("no token on %s", r.URL)
		}
		if r.Method == "POST" {
			_ = json.Unmarshal(r.Body, &sent)
			return &plugin.FetchResponse{Status: 201, Body: []byte(`{"id":77}`)}, nil
		}
		return &plugin.FetchResponse{Status: 204}, nil
	}
	state := plugin.State{Projects: map[string]map[string]json.RawMessage{"p1": {keyHook: json.RawMessage(`{"id":5,"repo":"acme/app"}`)}}}
	req := request(state, map[string]any{"repo": "acme/app"}, time.Now())
	req.Hook, req.Secrets = plugin.HookAction, map[string]string{"token": "tok"}
	req.Webhook = &plugin.Webhook{On: true, URL: "https://enplace.example.com/v1/hooks/abc", Secret: "shh"}
	r := hookRun(req, f)
	r.connect()
	if strings.Join(calls, ",") != "DELETE /repos/acme/app/hooks/5,POST /repos/acme/app/hooks" {
		t.Fatalf("calls %v", calls)
	}
	cfg, _ := sent["config"].(map[string]any)
	if cfg["secret"] != "shh" || cfg["url"] != req.Webhook.URL || cfg["content_type"] != "json" || cfg["insecure_ssl"] != "0" {
		t.Fatalf("sent %v", sent)
	}
	var h Hook
	raw, _ := json.Marshal(r.res.State.Projects["p1"][keyHook])
	if json.Unmarshal(raw, &h); h.ID != 77 {
		t.Fatalf("stored %s", raw)
	}

	// Without a token it says what's needed.
	req.Secrets = nil
	r = hookRun(req, f)
	r.connect()
	if !strings.Contains(r.res.Toast, "GitHub account") {
		t.Fatalf("toast %q", r.res.Toast)
	}
	// Not https: GitHub won't take it.
	req.Webhook.URL = "http://127.0.0.1:7070/v1/hooks/abc"
	r = hookRun(req, f)
	r.connect()
	if !strings.Contains(r.res.Toast, "https") {
		t.Fatalf("toast %q", r.res.Toast)
	}
}
