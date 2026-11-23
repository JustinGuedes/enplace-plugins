package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin/worksync"
)

const api = "https://api.github.com"

// client talks to GitHub's issues API for one repo.
type client struct {
	fetch func(plugin.FetchRequest) (*plugin.FetchResponse, error)
	token string
	repo  string // owner/name
}

type issue struct {
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	State       string    `json:"state"`        // open, closed
	StateReason string    `json:"state_reason"` // completed, not_planned, reopened
	HTMLURL     string    `json:"html_url"`
	UpdatedAt   time.Time `json:"updated_at"`
	Labels      []struct {
		Name string `json:"name"`
	} `json:"labels"`
	User *struct {
		Login string `json:"login"`
	} `json:"user"`
	Assignee *struct {
		Login string `json:"login"`
	} `json:"assignee"`
	Milestone *struct {
		Title string `json:"title"`
	} `json:"milestone"`
	// PullRequest is set on pull requests, which GitHub lists with issues.
	PullRequest json.RawMessage `json:"pull_request"`
}

func (c *client) do(method, path string, body any) ([]byte, error) {
	h := map[string]string{
		"Accept":               "application/vnd.github+json",
		"X-GitHub-Api-Version": "2022-11-28",
		"User-Agent":           "Enplace GitHub Issues plugin",
	}
	if c.token != "" {
		h["Authorization"] = "Bearer " + c.token
	}
	req := plugin.FetchRequest{Method: method, URL: api + path, Headers: h}
	if body != nil {
		raw, _ := json.Marshal(body)
		req.Body = raw
		h["Content-Type"] = "application/json"
	}
	r, err := c.fetch(req)
	if err != nil {
		return nil, errors.New("couldn't reach GitHub")
	}
	write := method != "" && method != "GET"
	switch {
	case r.Status >= 200 && r.Status < 300:
		return r.Body, nil
	case r.Status == 401:
		return nil, &worksync.AuthError{Msg: "GitHub turned the token down. Check it in Settings → Connections"}
	case write && (r.Status == 403 || r.Status == 404) && c.token == "":
		return nil, &worksync.AuthError{Msg: "changing issues needs a GitHub token with Issues read and write"}
	case write && r.Status == 403:
		return nil, &worksync.AuthError{Msg: "the token can't change issues: give it Issues read and write"}
	case r.Status == 403 || r.Status == 429:
		if c.token == "" {
			return nil, errors.New("GitHub's limit without a token is used up. Add your GitHub account in Settings → Connections")
		}
		return nil, errors.New("GitHub said no (it may be rate limiting, or the token can't read this repo)")
	case r.Status == 404:
		if c.token == "" {
			return nil, &worksync.AuthError{Msg: fmt.Sprintf("can't find %s. If it's private, add a GitHub token", c.repo)}
		}
		return nil, &worksync.AuthError{Msg: fmt.Sprintf("can't find %s, or the token can't read its issues", c.repo)}
	case r.Status == 410:
		return nil, errors.New("issues are switched off for " + c.repo)
	}
	return nil, fmt.Errorf("GitHub answered %d", r.Status)
}

func (c *client) list(q url.Values, max int) ([]issue, error) {
	var out []issue
	q.Set("per_page", "100")
	for page := 1; page <= max; page++ {
		q.Set("page", strconv.Itoa(page))
		b, err := c.do("GET", "/repos/"+c.repo+"/issues?"+q.Encode(), nil)
		if err != nil {
			return nil, err
		}
		var got []issue
		if err := json.Unmarshal(b, &got); err != nil {
			return nil, errors.New("GitHub sent something Enplace can't read")
		}
		out = append(out, got...)
		if len(got) < 100 {
			break
		}
	}
	return out, nil
}

func (c *client) get(n string) (*issue, error) {
	b, err := c.do("GET", "/repos/"+c.repo+"/issues/"+url.PathEscape(n), nil)
	if err != nil {
		return nil, err
	}
	var is issue
	if err := json.Unmarshal(b, &is); err != nil {
		return nil, errors.New("GitHub sent something Enplace can't read")
	}
	return &is, nil
}

func (c *client) send(method, path string, body map[string]any) (*issue, error) {
	b, err := c.do(method, "/repos/"+c.repo+path, body)
	if err != nil {
		return nil, err
	}
	var is issue
	if err := json.Unmarshal(b, &is); err != nil {
		return nil, errors.New("GitHub sent something Enplace can't read")
	}
	return &is, nil
}

// cleanRepo takes owner/name or a GitHub link.
func cleanRepo(v string) string {
	v = strings.TrimSpace(v)
	if u, err := url.Parse(v); err == nil && u.Host != "" {
		v = u.Path
	}
	v = strings.TrimSuffix(strings.Trim(v, "/"), ".git")
	parts := strings.Split(v, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	return parts[0] + "/" + parts[1]
}

// me is the login the token belongs to.
func (c *client) me() (string, error) {
	b, err := c.do("GET", "/user", nil)
	if err != nil {
		return "", err
	}
	var u struct {
		Login string `json:"login"`
	}
	if err := json.Unmarshal(b, &u); err != nil {
		return "", errors.New("GitHub sent something Enplace can't read")
	}
	return u.Login, nil
}

// tokenHash tells tokens apart without keeping one.
func tokenHash(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:6])
}
