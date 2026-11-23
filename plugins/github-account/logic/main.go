// Command logic is the GitHub account connection (id "github-account"): it
// holds a GitHub token for the plugins that work with repos, and checks
// who the token signs in as and what it may do.
package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

func main() { plugin.Run(handle) }

// fetch is plugin.Fetch, swapped in tests.
var fetch = plugin.Fetch

func handle(req *plugin.Request) (*plugin.Response, error) {
	if req.Hook != plugin.HookAction || req.Action == nil || req.Action.Name != "check" {
		return &plugin.Response{}, nil
	}
	return &plugin.Response{Data: check(req.Connections["github"].Values["token"])}, nil
}

// check asks GitHub who the token is. A classic token lists its scopes in
// X-OAuth-Scopes; a fine-grained one doesn't, so its scopes stay unknown.
func check(token string) plugin.Whoami {
	token = strings.TrimSpace(token)
	if token == "" {
		return plugin.Whoami{Problem: "Paste a token first."}
	}
	res, err := fetch(plugin.FetchRequest{URL: "https://api.github.com/user", Headers: map[string]string{
		"Authorization": "Bearer " + token, "Accept": "application/vnd.github+json", "X-GitHub-Api-Version": "2022-11-28",
	}})
	if err != nil {
		return plugin.Whoami{Problem: "GitHub didn't answer. Try again in a minute.", Unreachable: true}
	}
	switch {
	case res.Status == 401:
		return plugin.Whoami{Problem: "GitHub doesn't know this token: it was revoked, expired or mistyped."}
	case res.Status == 403:
		return plugin.Whoami{Problem: "GitHub refused the token (403). It may be rate limited or blocked by your organisation."}
	case res.Status >= 500:
		return plugin.Whoami{Problem: fmt.Sprintf("GitHub answered %d. Try again in a minute.", res.Status), Unreachable: true}
	case res.Status != 200:
		return plugin.Whoami{Problem: fmt.Sprintf("GitHub answered %d.", res.Status)}
	}
	var u struct {
		Login string `json:"login"`
		Name  string `json:"name"`
	}
	if json.Unmarshal(res.Body, &u) != nil || u.Login == "" {
		return plugin.Whoami{Problem: "GitHub's answer didn't say who the token is."}
	}
	out := plugin.Whoami{Who: u.Login}
	if u.Name != "" && u.Name != u.Login {
		out.Note = u.Name
	}
	if v, ok := header(res.Headers, "X-OAuth-Scopes"); ok {
		out.Scopes = []string{}
		for _, s := range strings.Split(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				out.Scopes = append(out.Scopes, s)
			}
		}
	} else {
		out.Note = strings.TrimSpace(out.Note + " · fine-grained token, so GitHub doesn't list what it may do")
		out.Note = strings.TrimPrefix(out.Note, "· ")
	}
	// Like "2026-12-01 10:00:00 UTC" or "2026-12-01 10:00:00 +0000".
	if v, ok := header(res.Headers, "GitHub-Authentication-Token-Expiration"); ok {
		for _, layout := range []string{"2006-01-02 15:04:05 MST", "2006-01-02 15:04:05 -0700"} {
			if t, err := time.Parse(layout, strings.TrimSpace(v)); err == nil {
				out.Expires = t.Unix()
				break
			}
		}
	}
	return out
}

func header(h map[string]string, name string) (string, bool) {
	for k, v := range h {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return "", false
}
