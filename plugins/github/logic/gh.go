package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

const api = "https://api.github.com"

// fetcher makes one HTTP request (plugin.Fetch inside Enplace).
type fetcher func(plugin.FetchRequest) (*plugin.FetchResponse, error)

// client talks to GitHub's REST API for one repo.
type client struct {
	fetch fetcher
	token string
	repo  string // owner/name
}

// apiError is a GitHub answer that isn't a success, said for people.
type apiError struct {
	Status int
	Text   string
	// Message is GitHub's own reason, when it gave one.
	Message string
}

func (e *apiError) Error() string { return e.Text }

func (c *client) get(path string, accept string) ([]byte, error) {
	return c.do("GET", path, accept, nil)
}

func (c *client) do(method, path, accept string, body []byte) ([]byte, error) {
	h := map[string]string{
		"Accept":               accept,
		"X-GitHub-Api-Version": "2022-11-28",
		"User-Agent":           "Enplace GitHub plugin",
	}
	if c.token != "" {
		h["Authorization"] = "Bearer " + c.token
	}
	req := plugin.FetchRequest{URL: api + path, Headers: h}
	if method != "GET" {
		req.Method, req.Body = method, body
		h["Content-Type"] = "application/json"
	}
	r, err := c.fetch(req)
	if err != nil {
		return nil, errors.New("couldn't reach GitHub")
	}
	if r.Status >= 300 && method != "GET" {
		var m struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(r.Body, &m)
		return nil, &apiError{Status: r.Status, Text: fmt.Sprintf("GitHub answered %d.", r.Status), Message: m.Message}
	}
	switch {
	case r.Status >= 200 && r.Status < 300:
		return r.Body, nil
	case r.Status == 401:
		return nil, &apiError{Status: r.Status, Text: "GitHub turned the token down. Check it in the plugin's settings."}
	case r.Status == 403 || r.Status == 429:
		if c.token == "" {
			return nil, &apiError{Status: r.Status, Text: "GitHub's limit for checks without a token is used up. Add a token in the plugin's settings."}
		}
		return nil, &apiError{Status: r.Status, Text: "GitHub said no (it may be rate limiting, or the token can't read this repo)."}
	case r.Status == 404:
		return nil, &apiError{Status: r.Status, Text: "not found"}
	}
	return nil, &apiError{Status: r.Status, Text: fmt.Sprintf("GitHub answered %d.", r.Status)}
}

func (c *client) getJSON(path string, v any) error {
	b, err := c.get(path, "application/vnd.github+json")
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return errors.New("GitHub sent something Enplace can't read")
	}
	return nil
}

func notFound(err error) bool {
	var e *apiError
	return errors.As(err, &e) && e.Status == 404
}

type repoInfo struct {
	FullName      string `json:"full_name"`
	HTMLURL       string `json:"html_url"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
}

func (c *client) repoInfo() (*repoInfo, error) {
	var r repoInfo
	err := c.getJSON("/repos/"+c.repo, &r)
	if notFound(err) {
		if c.token == "" {
			return nil, fmt.Errorf("can't find %s. If it's private, add a GitHub token in the plugin's settings", c.repo)
		}
		return nil, fmt.Errorf("can't find %s, or the token can't read it", c.repo)
	}
	return &r, err
}

type commit struct {
	SHA     string `json:"sha"`
	HTMLURL string `json:"html_url"`
	Commit  struct {
		Message   string `json:"message"`
		Committer struct {
			Date time.Time `json:"date"`
		} `json:"committer"`
	} `json:"commit"`
}

func (c *client) head(branch string) (*commit, error) {
	var cm commit
	err := c.getJSON("/repos/"+c.repo+"/commits/"+url.PathEscape(branch), &cm)
	if notFound(err) {
		return nil, fmt.Errorf("can't find the branch %s", branch)
	}
	return &cm, err
}

// files lists every file in the repo at a commit.
func (c *client) files(sha string) ([]string, error) {
	var t struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"tree"`
	}
	if err := c.getJSON("/repos/"+c.repo+"/git/trees/"+sha+"?recursive=1", &t); err != nil {
		return nil, err
	}
	var out []string
	for _, e := range t.Tree {
		if e.Type == "blob" {
			out = append(out, e.Path)
		}
	}
	return out, nil
}

// file reads one file at a commit.
func (c *client) file(p, sha string) ([]byte, error) {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	b, err := c.get("/repos/"+c.repo+"/contents/"+strings.Join(parts, "/")+"?ref="+url.QueryEscape(sha), "application/vnd.github.raw+json")
	if notFound(err) {
		return nil, fmt.Errorf("there's no %s on this branch", p)
	}
	return b, err
}

type tag struct {
	Name   string `json:"name"`
	Commit struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

func (c *client) tags() ([]tag, error) {
	var ts []tag
	err := c.getJSON("/repos/"+c.repo+"/tags?per_page=100", &ts)
	return ts, err
}

// ahead counts the commits on head since base.
func (c *client) ahead(base, head string) (int, error) {
	var cmp struct {
		AheadBy int `json:"ahead_by"`
	}
	err := c.getJSON("/repos/"+c.repo+"/compare/"+base+"..."+head, &cmp)
	return cmp.AheadBy, err
}

var (
	reRepoURL  = regexp.MustCompile(`^(?:https?://)?(?:www\.)?github\.com[/:]([A-Za-z0-9-]+)/([A-Za-z0-9._-]+?)(?:\.git)?(?:/.*)?$`)
	reRepoSSH  = regexp.MustCompile(`^git@github\.com:([A-Za-z0-9-]+)/([A-Za-z0-9._-]+?)(?:\.git)?$`)
	reRepoPair = regexp.MustCompile(`^([A-Za-z0-9-]+)/([A-Za-z0-9._-]+?)(?:\.git)?$`)
)

// parseRepo reads "owner/name" from what a person typed: owner/name, a
// GitHub link or a git remote. It's "" when it isn't one.
func parseRepo(s string) string {
	s = strings.TrimSpace(s)
	for _, re := range []*regexp.Regexp{reRepoURL, reRepoSSH, reRepoPair} {
		if m := re.FindStringSubmatch(s); m != nil {
			return m[1] + "/" + m[2]
		}
	}
	return ""
}

type pull struct {
	Number   int       `json:"number"`
	Title    string    `json:"title"`
	HTMLURL  string    `json:"html_url"`
	State    string    `json:"state"` // open or closed
	Draft    bool      `json:"draft"`
	MergedAt time.Time `json:"merged_at"`
	ClosedAt time.Time `json:"closed_at"`
	Updated  time.Time `json:"updated_at"`
	Created  time.Time `json:"created_at"`
	Head     struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
}

// pullSize is one pull request's size, which the list leaves out.
type pullSize struct {
	Additions int `json:"additions"`
	Deletions int `json:"deletions"`
	Head      struct {
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
	// MergeableState is GitHub's word on merging it now: clean,
	// has_hooks, unstable, blocked, behind, dirty, draft or unknown.
	MergeableState string `json:"mergeable_state"`
}

func (c *client) pull(n int) (*pullSize, error) {
	var p pullSize
	err := c.getJSON(fmt.Sprintf("/repos/%s/pulls/%d", c.repo, n), &p)
	return &p, err
}

type checkRun struct {
	Name       string `json:"name"`
	Status     string `json:"status"`     // queued, in_progress or completed
	Conclusion string `json:"conclusion"` // success, failure, neutral, cancelled, skipped, timed_out, action_required
}

// checkRuns are the checks on one commit (a token needs Checks read for a
// private repo).
func (c *client) checkRuns(sha string) ([]checkRun, error) {
	var out struct {
		Runs []checkRun `json:"check_runs"`
	}
	err := c.getJSON("/repos/"+c.repo+"/commits/"+url.PathEscape(sha)+"/check-runs?per_page=50", &out)
	return out.Runs, err
}

// pulls are the 50 most recently changed pull requests, open or not.
func (c *client) pulls() ([]pull, error) {
	var ps []pull
	err := c.getJSON("/repos/"+c.repo+"/pulls?state=all&sort=updated&direction=desc&per_page=50", &ps)
	return ps, err
}

// branch is one branch and the commit at its tip.
type branch struct {
	Name   string `json:"name"`
	Commit struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

// branches are the repo's first 100 branches.
func (c *client) branches() ([]branch, error) {
	var bs []branch
	err := c.getJSON("/repos/"+c.repo+"/branches?per_page=100", &bs)
	return bs, err
}

// hasPull says whether a branch ever had a pull request, open or not.
func (c *client) hasPull(branch string) (bool, error) {
	owner, _, _ := strings.Cut(c.repo, "/")
	var ps []pull
	err := c.getJSON("/repos/"+c.repo+"/pulls?state=all&per_page=1&head="+url.QueryEscape(owner+":"+branch), &ps)
	return len(ps) > 0, err
}

// commitAt is one commit, by its sha.
func (c *client) commitAt(sha string) (*commit, error) {
	var cm commit
	err := c.getJSON("/repos/"+c.repo+"/commits/"+url.PathEscape(sha), &cm)
	return &cm, err
}

// commits are the latest 50 commits on a branch.
func (c *client) commits(branch string) ([]commit, error) {
	var cs []commit
	err := c.getJSON("/repos/"+c.repo+"/commits?per_page=50&sha="+url.QueryEscape(branch), &cs)
	return cs, err
}

// merge merges pull request n, only if its head is still sha (so what
// was looked at is what lands). Its errors are said for people.
func (c *client) merge(n int, sha string) error {
	body := map[string]string{}
	if sha != "" {
		body["sha"] = sha
	}
	b, _ := json.Marshal(body)
	_, err := c.do("PUT", fmt.Sprintf("/repos/%s/pulls/%d/merge", c.repo, n), "application/vnd.github+json", b)
	var e *apiError
	if err == nil || !errors.As(err, &e) {
		return err
	}
	switch e.Status {
	case 401:
		return errors.New("GitHub turned the token down. Check it in the plugin's settings.")
	case 403, 404:
		return errors.New("The GitHub token can't merge pull requests here. Give it Pull requests: read and write in the plugin's settings.")
	case 405:
		if e.Message != "" {
			return fmt.Errorf("GitHub won't merge #%d yet: %s", n, e.Message)
		}
		return fmt.Errorf("GitHub won't merge #%d yet.", n)
	case 409:
		return fmt.Errorf("#%d changed since Enplace last looked. Have a look at it and try again.", n)
	case 422:
		return fmt.Errorf("GitHub couldn't merge #%d: %s", n, e.Message)
	}
	return e
}
