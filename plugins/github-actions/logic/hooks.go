package main

// Instant updates: GitHub tells Enplace the moment a run or job starts
// or ends. Enplace checks each delivery's signature before it gets here;
// the delivery is only a doorbell, and the plugin then reads everything
// from GitHub's API, so nothing in a delivery's body is trusted as data.

import (
	"encoding/json"
	"strconv"
	"strings"
)

const keyHook = "hook"

// hookEvents are what Enplace asks GitHub to send.
var hookEvents = []string{"workflow_run", "workflow_job"}

// Hook is the webhook the plugin added on GitHub, so it can remove it.
type Hook struct {
	ID   int64  `json:"id"`
	Repo string `json:"repo"`
}

// webhook handles one delivery about the linked repo's runs.
func (r *run) webhook() {
	w := r.req.Webhook
	if w == nil || w.Delivery == nil || !r.reads() {
		return
	}
	var body struct {
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	}
	_ = json.Unmarshal(w.Delivery.Body, &body)
	repo, _ := r.code()
	if repo == "" || !strings.EqualFold(body.Repository.FullName, repo) {
		return
	}
	for _, e := range hookEvents {
		if w.Delivery.Event == e {
			r.refresh(nil)
			return
		}
	}
}

// connect adds the webhook on GitHub. One it added before is replaced, so
// a new secret takes over at once.
func (r *run) connect() {
	w := r.req.Webhook
	c := r.client()
	switch {
	case c.repo == "":
		r.res.Toast = "Link the repo in the GitHub plugin first."
		return
	case !r.reads():
		r.res.Toast = "The project's GitHub Actions gets the updates for this product."
		return
	case w == nil || !w.On || w.Secret == "":
		r.res.Toast = "Instant updates aren't on."
		return
	case !strings.HasPrefix(w.URL, "https://"):
		r.res.Toast = "GitHub needs an https address. Set Enplace's public URL, or paste it in by hand."
		return
	}
	r.removeHook(c)
	out, err := c.call("POST", "/repos/"+c.repo+"/hooks", map[string]any{
		"name": "web", "active": true, "events": hookEvents,
		"config": map[string]string{"url": w.URL, "content_type": "json", "secret": w.Secret, "insecure_ssl": "0"},
	})
	if err != nil {
		r.res.Toast = "Couldn't add the webhook: " + err.Error() + " The token needs Webhooks: read and write, or paste the address and secret in by hand."
		return
	}
	var h Hook
	_ = json.Unmarshal(out, &h)
	h.Repo = c.repo
	r.res.SetProject(r.pid, keyHook, h)
	r.res.Toast = "GitHub will tell Enplace about " + c.repo + "'s builds as they happen."
	r.res.Log = "Added a webhook for Actions to " + c.repo
}

// disconnect removes the webhook it added.
func (r *run) disconnect() {
	var h Hook
	if !r.req.State.GetProject(r.pid, keyHook, &h) || h.ID == 0 {
		return
	}
	if r.removeHook(&client{token: r.req.Secrets["token"], repo: h.Repo}) {
		r.res.Toast = "Removed the webhook from " + h.Repo + "."
	} else {
		r.res.Toast = "Couldn't remove the webhook from " + h.Repo + " on GitHub; remove it there by hand."
	}
}

// removeHook deletes the webhook stored in state, if any. It reports
// whether nothing is left on GitHub.
func (r *run) removeHook(c *client) bool {
	var h Hook
	if !r.req.State.GetProject(r.pid, keyHook, &h) || h.ID == 0 {
		return true
	}
	gh := &client{token: c.token, repo: h.Repo}
	_, err := gh.call("DELETE", "/repos/"+h.Repo+"/hooks/"+strconv.FormatInt(h.ID, 10), nil)
	if ae, ok := err.(*apiError); err == nil || ok && ae.Status == 404 {
		r.res.SetProject(r.pid, keyHook, nil)
		return true
	}
	return false
}

func (r *run) hooked() bool {
	var h Hook
	return r.req.State.GetProject(r.pid, keyHook, &h) && h.ID != 0
}
