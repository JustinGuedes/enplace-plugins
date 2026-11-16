package main

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

// Code pushed with no pull request (ENP-106): an agent pushed a branch and
// handed back without opening one, so nothing linked and nothing offered
// Merge. Branches without a pull request whose name or newest commit
// mention a work key link to that item, with a way to open one.

const (
	keyBranches = "branches"
	// branchReads is how many branch tips are read each run; the rest wait
	// for the next run. A tip is only read again when the branch moves.
	branchReads = 10
	// branchFresh is how recent a branch's newest commit must be to count:
	// older branches without a pull request are left alone.
	branchFresh = 21 * 24 * time.Hour
)

// branchSeen is what was read about a branch's tip.
type branchSeen struct {
	SHA   string    `json:"sha"`
	Title string    `json:"title"`
	At    time.Time `json:"at"`
	Keys  []string  `json:"keys,omitempty"`
	// PR is set once GitHub says the branch had a pull request, even one
	// too old to be among the latest read.
	PR bool `json:"pr,omitempty"`
}

// linkBranches drops last run's branch links and files a fresh one for
// each branch that mentions work and has no pull request. A failure to
// read branches leaves branch links out; it never fails the check.
func (r *run) linkBranches(gh *client, c *Code, prs []pull, ls Links, re *regexp.Regexp) {
	for k, list := range ls {
		kept := list[:0:0]
		for _, l := range list {
			if l.Kind != "branch" {
				kept = append(kept, l)
			}
		}
		if len(kept) == 0 {
			delete(ls, k)
		} else {
			ls[k] = kept
		}
	}
	bs, err := gh.branches()
	if err != nil {
		return
	}
	withPR := map[string]bool{}
	for _, p := range prs {
		withPR[p.Head.Ref] = true
	}
	seen := map[string]branchSeen{}
	if !r.fresh {
		r.req.State.GetProject(r.pid, keyBranches, &seen)
	}
	kept := map[string]branchSeen{}
	reads := 0
	for _, b := range bs {
		if b.Name == c.DefaultBranch || b.Name == c.Branch || withPR[b.Name] || b.Commit.SHA == "" {
			continue
		}
		s, ok := seen[b.Name]
		if !ok || s.SHA != b.Commit.SHA {
			if reads >= branchReads {
				continue
			}
			reads++
			cm, err := gh.commitAt(b.Commit.SHA)
			if err != nil {
				continue
			}
			title, _, _ := strings.Cut(cm.Commit.Message, "\n")
			s = branchSeen{SHA: b.Commit.SHA, Title: title, At: cm.Commit.Committer.Date,
				Keys: mentions(re, b.Name, cm.Commit.Message), PR: s.PR}
			if len(s.Keys) > 0 && !s.PR && r.req.Now.Sub(s.At) <= branchFresh {
				had, err := gh.hasPull(b.Name)
				if err != nil {
					continue
				}
				s.PR = had
			}
		}
		kept[b.Name] = s
		if len(s.Keys) == 0 || s.PR || r.req.Now.Sub(s.At) > branchFresh {
			continue
		}
		ls.add(s.Keys, Link{Kind: "branch", Title: s.Title, Branch: b.Name, SHA: s.SHA, At: s.At,
			URL: compareURL(c, b.Name)})
	}
	r.res.SetProject(r.pid, keyBranches, kept)
}

// compareURL is GitHub's page for opening a pull request from a branch.
func compareURL(c *Code, name string) string {
	base := c.DefaultBranch
	if base == "" {
		base = "main"
	}
	u := c.URL
	if u == "" {
		u = "https://github.com/" + c.Repo
	}
	esc := func(s string) string { return strings.ReplaceAll(url.PathEscape(s), "%2F", "/") }
	return u + "/compare/" + esc(base) + "..." + esc(name) + "?expand=1"
}

// branchStrip is a branch pushed with no pull request: a pr.status button
// with state "nopr" tells the card, and its URL opens one on GitHub.
func branchStrip(l Link, now time.Time) plugin.Block {
	args, _ := json.Marshal(map[string]any{"state": "nopr", "branch": l.Branch})
	return plugin.Block{Type: plugin.BlockStrip, Label: "Branch " + l.Branch, Text: l.Title,
		Sub: "Pushed " + ago(l.At, now) + " · no pull request", Value: "No pull request", Tone: "warn",
		Buttons: []plugin.Button{
			{Label: "Open a pull request", URL: l.URL},
			{Label: "Branch " + l.Branch + " has no pull request", Action: actStatus, Args: args, URL: l.URL},
		}}
}
