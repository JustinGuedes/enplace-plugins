package main

// Instant updates (ENP-3): GitHub tells Enplace the moment code, pull
// requests or tags change. Enplace checks each delivery's signature before
// it gets here; the delivery itself is only a doorbell. The plugin then
// runs its usual check, reading everything from GitHub's API, so nothing
// in a delivery's body is trusted as data.

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

const keyHook = "hook"

// hookEvents are what Enplace asks GitHub to send.
var hookEvents = []string{"push", "pull_request", "create", "delete", "release", "check_suite"}

// Hook is the webhook the plugin added on GitHub, so it can remove it.
type Hook struct {
	ID   int64  `json:"id"`
	Repo string `json:"repo"`
	URL  string `json:"url"`
}

// webhook handles one delivery: anything for the linked repo that changes
// what the plugin shows runs a check now.
func (r *run) webhook() {
	w := r.req.Webhook
	if w == nil || w.Delivery == nil {
		return
	}
	var body struct {
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	}
	_ = json.Unmarshal(w.Delivery.Body, &body)
	repo := parseRepo(r.setting("repo"))
	if repo == "" || !strings.EqualFold(body.Repository.FullName, repo) {
		if c := r.current(); c == nil || !strings.EqualFold(body.Repository.FullName, c.Repo) {
			return // a delivery about another repo, or a ping without one
		}
	}
	for _, e := range hookEvents {
		if w.Delivery.Event == e {
			r.res.Refresh = true
			return
		}
	}
}

// send makes one call that changes something on GitHub.
func (c *client) send(method, path string, body any) ([]byte, error) {
	if c.token == "" {
		return nil, &apiError{Status: 401, Text: "Add a GitHub token first."}
	}
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	res, err := c.fetch(plugin.FetchRequest{Method: method, URL: api + path, Body: raw, Headers: map[string]string{
		"Accept":               "application/vnd.github+json",
		"Content-Type":         "application/json",
		"X-GitHub-Api-Version": "2022-11-28",
		"User-Agent":           "Enplace GitHub plugin",
		"Authorization":        "Bearer " + c.token,
	}})
	if err != nil {
		return nil, errors.New("couldn't reach GitHub")
	}
	switch {
	case res.Status >= 200 && res.Status < 300:
		return res.Body, nil
	case res.Status == 401:
		return nil, &apiError{Status: res.Status, Text: "GitHub turned the token down."}
	case res.Status == 403 || res.Status == 404:
		return nil, &apiError{Status: res.Status, Text: "The token can't manage this repo's webhooks. Give it Webhooks: read and write, or paste the address and secret in by hand."}
	case res.Status == 422:
		return nil, &apiError{Status: res.Status, Text: "GitHub wouldn't take the webhook (it may already have one at this address)."}
	}
	return nil, &apiError{Status: res.Status, Text: fmt.Sprintf("GitHub answered %d.", res.Status)}
}

// connect adds the webhook on GitHub (the Connect action). Any webhook it
// added before is replaced, so a new secret takes over at once.
func (r *run) connect() {
	w := r.req.Webhook
	repo := parseRepo(r.setting("repo"))
	switch {
	case repo == "":
		r.res.Toast = "Add the repo first."
		return
	case w == nil || !w.On || w.Secret == "":
		r.res.Toast = "Instant updates aren't on."
		return
	case !strings.HasPrefix(w.URL, "https://"):
		r.res.Toast = "GitHub needs an https address. Set Enplace's public URL, or paste it in by hand."
		return
	}
	gh := &client{fetch: r.fetch, token: r.req.Secrets["token"], repo: repo}
	if c := r.current(); c != nil && c.Repo != "" {
		gh.repo = c.Repo
	}
	r.removeHook(gh)
	out, err := gh.send("POST", "/repos/"+gh.repo+"/hooks", map[string]any{
		"name": "web", "active": true, "events": hookEvents,
		"config": map[string]string{"url": w.URL, "content_type": "json", "secret": w.Secret, "insecure_ssl": "0"},
	})
	if err != nil {
		r.res.Toast = err.Error()
		return
	}
	var h struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(out, &h)
	r.res.SetProject(r.pid, keyHook, Hook{ID: h.ID, Repo: gh.repo, URL: w.URL})
	r.res.Toast = "GitHub will tell Enplace about " + gh.repo + " as it happens."
	r.res.Log = "Added a webhook to " + gh.repo
}

// disconnect removes the webhook it added (the Disconnect action).
func (r *run) disconnect() {
	var h Hook
	if !r.req.State.GetProject(r.pid, keyHook, &h) || h.ID == 0 {
		return
	}
	gh := &client{fetch: r.fetch, token: r.req.Secrets["token"], repo: h.Repo}
	if r.removeHook(gh) {
		r.res.Toast = "Removed the webhook from " + h.Repo + "."
	} else {
		r.res.Toast = "Couldn't remove the webhook from " + h.Repo + " on GitHub; remove it there by hand."
	}
}

// removeHook deletes the webhook stored in state, if any. It reports
// whether nothing is left on GitHub.
func (r *run) removeHook(gh *client) bool {
	var h Hook
	if !r.req.State.GetProject(r.pid, keyHook, &h) || h.ID == 0 {
		return true
	}
	c := &client{fetch: gh.fetch, token: gh.token, repo: h.Repo}
	_, err := c.send("DELETE", "/repos/"+h.Repo+"/hooks/"+strconv.FormatInt(h.ID, 10), nil)
	var ae *apiError
	if err == nil || (errors.As(err, &ae) && ae.Status == 404) {
		r.res.SetProject(r.pid, keyHook, nil)
		return true
	}
	return false
}
