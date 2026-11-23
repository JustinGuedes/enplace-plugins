package main

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin/plugintest"
)

func TestSmoke(t *testing.T) { plugintest.Smoke(t, handle, "../manifest.json") }

func answer(status int, headers map[string]string, body string) func(plugin.FetchRequest) (*plugin.FetchResponse, error) {
	return func(r plugin.FetchRequest) (*plugin.FetchResponse, error) {
		if r.Headers["Authorization"] != "Bearer tok" {
			return &plugin.FetchResponse{Status: 401}, nil
		}
		return &plugin.FetchResponse{Status: status, Headers: headers, Body: []byte(body)}, nil
	}
}

func checked(t *testing.T) plugin.Whoami {
	t.Helper()
	args, _ := json.Marshal(map[string]any{})
	res, err := handle(&plugin.Request{Hook: plugin.HookAction, Action: &plugin.Action{Name: "check", Args: args},
		Connections: map[string]plugin.Account{"github": {Name: "Personal", Values: map[string]string{"token": " tok\n"}}}})
	if err != nil {
		t.Fatal(err)
	}
	return res.Data.(plugin.Whoami)
}

func TestAClassicTokenSaysItsScopesAndExpiry(t *testing.T) {
	fetch = answer(200, map[string]string{"X-Oauth-Scopes": "repo, workflow", "Github-Authentication-Token-Expiration": "2026-12-01 10:00:00 UTC"}, `{"login":"octo","name":"Octo Cat"}`)
	w := checked(t)
	if w.Who != "octo" || len(w.Scopes) != 2 || w.Scopes[1] != "workflow" || w.Expires != 1796119200 || w.Problem != "" {
		t.Errorf("%+v", w)
	}
}

func TestAFineGrainedTokenHasNoScopesToList(t *testing.T) {
	fetch = answer(200, nil, `{"login":"octo"}`)
	if w := checked(t); w.Who != "octo" || w.Scopes != nil || w.Note == "" {
		t.Errorf("%+v", w)
	}
}

func TestProblemsSayWhy(t *testing.T) {
	fetch = func(plugin.FetchRequest) (*plugin.FetchResponse, error) {
		return &plugin.FetchResponse{Status: 401}, nil
	}
	if w := checked(t); w.Problem == "" || w.Who != "" {
		t.Errorf("401: %+v", w)
	}
	fetch = func(plugin.FetchRequest) (*plugin.FetchResponse, error) { return nil, errors.New("down") }
	if w := checked(t); w.Problem == "" || !w.Unreachable {
		t.Errorf("down: %+v", w)
	}
	if w := check(""); w.Problem == "" {
		t.Error("empty token")
	}
}
