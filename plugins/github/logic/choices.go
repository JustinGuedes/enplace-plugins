package main

import (
	"encoding/json"
	"net/url"
	"strconv"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

// Settings that offer a list to pick from (choices in the manifest).
const (
	actRepos    = "repos"
	actBranches = "branches"
	// listPages caps how many pages of 100 are read for one list.
	listPages = 3
)

// choices answers a setting's list: the repos the token can see, or the
// branches of the repo on screen.
func (r *run) choices(name string) {
	var args plugin.ChoicesArgs
	_ = json.Unmarshal(r.req.Action.Args, &args)
	gh := &client{fetch: r.fetch, token: r.req.Secrets["token"]}
	var out plugin.Choices
	var err error
	switch name {
	case actRepos:
		out, err = r.repos(gh)
	case actBranches:
		repo, _ := args.Values["repo"].(string)
		if gh.repo = parseRepo(repo); gh.repo == "" {
			out.Note = "Pick the repo first."
			break
		}
		out, err = branches(gh)
	}
	if err != nil {
		out.Note = err.Error()
	}
	if out.Choices == nil {
		out.Choices = []plugin.Choice{}
	}
	r.res.Data = out
}

func (r *run) repos(gh *client) (plugin.Choices, error) {
	if gh.token == "" {
		return plugin.Choices{Note: "Add your GitHub account in Settings → Connections to pick from your repos. You can still type owner/name for a public one."}, nil
	}
	var out plugin.Choices
	for page := 1; page <= listPages; page++ {
		var list []struct {
			FullName string    `json:"full_name"`
			Private  bool      `json:"private"`
			Archived bool      `json:"archived"`
			PushedAt time.Time `json:"pushed_at"`
			Owner    struct {
				Login string `json:"login"`
			} `json:"owner"`
		}
		q := url.Values{"per_page": {"100"}, "sort": {"pushed"}, "page": {strconv.Itoa(page)}}
		if err := gh.getJSON("/user/repos?"+q.Encode(), &list); err != nil {
			return out, err
		}
		for _, x := range list {
			if x.Archived {
				continue
			}
			c := plugin.Choice{Value: x.FullName, Group: x.Owner.Login, Detail: ago(x.PushedAt, r.req.Now)}
			if x.Private {
				c.Badge = "Private"
			}
			out.Choices = append(out.Choices, c)
		}
		if len(list) < 100 {
			return out, nil
		}
	}
	out.Note = "Showing the " + strconv.Itoa(len(out.Choices)) + " most recently changed. Type owner/name for an older one."
	return out, nil
}

func branches(gh *client) (plugin.Choices, error) {
	info, err := gh.repoInfo()
	if err != nil {
		return plugin.Choices{}, err
	}
	gh.repo = info.FullName
	var out plugin.Choices
	for page := 1; page <= listPages; page++ {
		var list []struct {
			Name string `json:"name"`
		}
		q := url.Values{"per_page": {"100"}, "page": {strconv.Itoa(page)}}
		if err := gh.getJSON("/repos/"+gh.repo+"/branches?"+q.Encode(), &list); err != nil {
			return out, err
		}
		for _, b := range list {
			c := plugin.Choice{Value: b.Name}
			if b.Name == info.DefaultBranch {
				c.Badge = "Default"
				out.Choices = append([]plugin.Choice{c}, out.Choices...)
				continue
			}
			out.Choices = append(out.Choices, c)
		}
		if len(list) < 100 {
			break
		}
	}
	return out, nil
}
