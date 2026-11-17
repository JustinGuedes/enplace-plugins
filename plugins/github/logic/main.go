// Command logic is the GitHub plugin: it links a project to its repo.
// Every 15 minutes (and on "Check now") it asks GitHub for the branch's
// latest commit; when that moved, it reads the files the Versions plugin
// asks for (see versions.go), and it looks at the version tags and how far
// the branch is past the latest one. It opens the pull requests Versions
// asks for to bump the version.
//
// It publishes what it found as "public.code", in a shape any code host
// plugin can publish too:
//
//	{"provider":"github","repo":"owner/name","url":"https://github.com/owner/name",
//	 "branch":"main","tag":"v1.4","tag_version":"1.4","ahead":12,"sha":"…",
//	 "commit_at":"…","checked_at":"…"}
package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

func main() { plugin.Run(handle) }

const (
	keyCode   = "code"
	keyPublic = "public.code"
	// repoEvery is how often the repo's details (default branch, link)
	// are looked up again.
	repoEvery = 24 * time.Hour
)

// Code is what the last check found.
type Code struct {
	Repo          string    `json:"repo"` // owner/name as GitHub spells it
	Typed         string    `json:"typed"`
	URL           string    `json:"url,omitempty"`
	DefaultBranch string    `json:"default_branch,omitempty"`
	Private       bool      `json:"private,omitempty"`
	RepoAt        time.Time `json:"repo_at,omitempty"`
	Branch        string    `json:"branch,omitempty"`
	SHA           string    `json:"sha,omitempty"`
	Message       string    `json:"message,omitempty"`
	CommitURL     string    `json:"commit_url,omitempty"`
	CommitAt      time.Time `json:"commit_at,omitempty"`
	Tag           string    `json:"tag,omitempty"`
	TagVersion    string    `json:"tag_version,omitempty"`
	TagSHA        string    `json:"tag_sha,omitempty"`
	Ahead         int       `json:"ahead"`
	// Error is what went wrong last time; what was found before is kept.
	Error     string    `json:"error,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

// Public is what other plugins read, as "public.code".
type Public struct {
	Provider   string    `json:"provider"`
	Repo       string    `json:"repo"`
	URL        string    `json:"url,omitempty"`
	Branch     string    `json:"branch,omitempty"`
	Tag        string    `json:"tag,omitempty"`
	TagVersion string    `json:"tag_version,omitempty"`
	Ahead      int       `json:"ahead"`
	SHA        string    `json:"sha,omitempty"`
	CommitAt   time.Time `json:"commit_at,omitempty"`
	CheckedAt  time.Time `json:"checked_at"`
	// VersionFile is the Version file setting this plugin had before the
	// Versions plugin took it over, so Versions can carry it on.
	VersionFile string `json:"version_file,omitempty"`
}

type run struct {
	// fresh: the repo changed, so links found before are dropped.
	fresh bool
	req   *plugin.Request
	res   *plugin.Response
	pid   string
	fetch fetcher
}

func handle(req *plugin.Request) (*plugin.Response, error) {
	r := &run{req: req, res: &plugin.Response{}, pid: req.ProjectID, fetch: plugin.Fetch}
	switch req.Hook {
	case plugin.HookSchedule:
		r.save(r.check(false))
	case plugin.HookWebhook:
		r.webhook()
	case plugin.HookAction:
		if req.Action == nil {
			break
		}
		switch req.Action.Name {
		case "check":
			c := r.check(true)
			r.save(c)
			switch {
			case c == nil:
			case c.Error != "":
				r.res.Toast = c.Error
			case r.version() != nil:
				r.res.Toast = "The code says " + r.version().Version
			default:
				r.res.Toast = "Checked " + c.Repo
			}
		case "hook_connect":
			r.connect()
		case "hook_disconnect":
			r.disconnect()
		case actMerge:
			r.merge()
		case actRepos, actBranches:
			r.choices(req.Action.Name)
		case "tool_code":
			var args struct {
				Work string `json:"work"`
			}
			_ = json.Unmarshal(req.Action.Args, &args)
			r.res.Data = r.tool(args.Work)
		}
	case plugin.HookRender:
		c := r.current()
		for _, slot := range req.Slots {
			switch slot {
			case "project_card:code":
				r.card(slot, c)
			case plugin.SlotSettingsPanel:
				r.setCard(slot, r.panel(c))
			case plugin.SlotWorkPanel:
				r.setCard(slot, r.workPanel(c))
			case plugin.SlotBrief:
				r.brief()
			}
		}
	}
	return r.res, nil
}

// merge merges the pull request a person pressed Merge on (they were
// asked first), then reads the repo again so the work it mentions moves to
// done straight away. Data says whether it merged, for the card.
func (r *run) merge() {
	var args struct {
		Number int    `json:"number"`
		SHA    string `json:"sha"`
	}
	_ = json.Unmarshal(r.req.Action.Args, &args)
	fail := func(msg string) {
		r.res.Toast = msg
		r.res.Data = map[string]any{"merged": false, "error": msg}
	}
	c := r.current()
	switch {
	case args.Number <= 0:
		fail("Which pull request? Enplace didn't get its number.")
		return
	case c == nil:
		fail("Link this project to its GitHub repo first.")
		return
	case r.req.Secrets["token"] == "":
		fail("Add a GitHub token with Pull requests: read and write in the plugin's settings to merge from Enplace.")
		return
	}
	gh := &client{fetch: r.fetch, token: r.req.Secrets["token"], repo: c.Repo}
	if err := gh.merge(args.Number, args.SHA); err != nil {
		fail(err.Error())
		return
	}
	r.save(r.check(true))
	r.res.Toast = fmt.Sprintf("Merged #%d", args.Number)
	r.res.Data = map[string]any{"merged": true}
}

func (r *run) setting(key string) string {
	v, _ := r.req.ProjectSettings[r.pid][key].(string)
	return strings.TrimSpace(v)
}

// current is the last check, if it was for the repo set now.
func (r *run) current() *Code {
	var c *Code
	if !r.req.State.GetProject(r.pid, keyCode, &c) || c == nil || c.Typed != r.setting("repo") {
		return nil
	}
	return c
}

func (r *run) save(c *Code) {
	r.res.SetProject(r.pid, keyCode, c)
	if c == nil {
		r.res.SetProject(r.pid, keyLinks, nil)
		r.res.SetProject(r.pid, keyMoves, nil)
	}
	if c == nil || c.SHA == "" {
		r.res.SetProject(r.pid, keyPublic, nil)
		return
	}
	r.res.SetProject(r.pid, keyPublic, Public{Provider: "github", Repo: c.Repo, URL: c.URL, Branch: c.Branch,
		Tag: c.Tag, TagVersion: c.TagVersion, Ahead: c.Ahead, SHA: c.SHA, CommitAt: c.CommitAt, CheckedAt: c.CheckedAt,
		VersionFile: strings.Trim(r.setting("version_file"), "/ ")})
}

// check asks GitHub what changed since last time. force reads everything
// again. It's nil when no repo is set.
func (r *run) check(force bool) *Code {
	typed := r.setting("repo")
	if typed == "" {
		return nil
	}
	now := r.req.Now
	repo := parseRepo(typed)
	if repo == "" {
		return &Code{Typed: typed, Error: "That isn't a GitHub repo. Use owner/name or the repo's link.", CheckedAt: now}
	}
	c := r.current()
	if c == nil || !strings.EqualFold(c.Repo, repo) {
		c = &Code{Repo: repo}
		r.fresh = true
	}
	c.Typed, c.CheckedAt, c.Error = typed, now, ""
	gh := &client{fetch: r.fetch, token: r.req.Secrets["token"], repo: c.Repo}
	fail := func(err error) *Code {
		c.Error = upper(err.Error())
		if !strings.HasSuffix(c.Error, ".") {
			c.Error += "."
		}
		return c
	}

	if force || c.DefaultBranch == "" || now.Sub(c.RepoAt) >= repoEvery {
		info, err := gh.repoInfo()
		if err != nil {
			return fail(err)
		}
		c.Repo, c.URL, c.DefaultBranch, c.Private, c.RepoAt = info.FullName, info.HTMLURL, info.DefaultBranch, info.Private, now
		gh.repo = c.Repo
	}
	branch := r.setting("branch")
	if branch == "" {
		branch = c.DefaultBranch
	}
	if branch != c.Branch {
		c.Branch, c.SHA = branch, ""
	}

	head, err := gh.head(branch)
	if err != nil && r.setting("branch") == "" && now.Sub(c.RepoAt) > 0 {
		// The default branch it knew may be gone, deleted or renamed
		// (ENP-124): look the repo up again rather than fail until tomorrow.
		if info, ierr := gh.repoInfo(); ierr == nil && info.DefaultBranch != branch {
			c.Repo, c.URL, c.DefaultBranch, c.Private, c.RepoAt = info.FullName, info.HTMLURL, info.DefaultBranch, info.Private, now
			gh.repo, branch = c.Repo, info.DefaultBranch
			c.Branch, c.SHA = branch, ""
			head, err = gh.head(branch)
		}
	}
	if err != nil {
		return fail(err)
	}
	moved := head.SHA != c.SHA
	if moved || force {
		c.SHA, c.CommitURL, c.CommitAt = head.SHA, head.HTMLURL, head.Commit.Committer.Date
		c.Message, _, _ = strings.Cut(head.Commit.Message, "\n")
	}
	if err := r.share(gh, c, moved || force); err != nil {
		return fail(err)
	}
	r.bump(gh, c)

	if err := r.link(gh, c, moved || force); err != nil {
		return fail(err)
	}

	tags, err := gh.tags()
	if err != nil {
		return fail(err)
	}
	best := latestTag(tags)
	if best == nil {
		c.Tag, c.TagVersion, c.TagSHA, c.Ahead = "", "", "", 0
		return c
	}
	if best.Name != c.Tag || best.Commit.SHA != c.TagSHA || moved || force {
		n := 0
		if best.Commit.SHA != head.SHA {
			if n, err = gh.ahead(best.Commit.SHA, head.SHA); err != nil {
				return fail(err)
			}
		}
		c.Tag, c.TagVersion, c.TagSHA, c.Ahead = best.Name, fromTag(best.Name), best.Commit.SHA, n
	}
	return c
}

// latestTag is the highest version tag, or nil.
func latestTag(tags []tag) *tag {
	var best *tag
	for i := range tags {
		v := fromTag(tags[i].Name)
		if v == "" {
			continue
		}
		if best == nil || newer(v, fromTag(best.Name)) {
			best = &tags[i]
		}
	}
	return best
}

func upper(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// ----- Drawing -----

func (r *run) setCard(slot string, blocks []plugin.Block) {
	if r.res.Cards == nil {
		r.res.Cards = map[string][]plugin.Block{}
	}
	r.res.Cards[slot] = blocks
}

// where says where the code is at, like "main · 12 commits since v1.4".
func where(c *Code) string {
	var parts []string
	if c.Branch != "" {
		parts = append(parts, c.Branch)
	}
	switch {
	case c.Tag == "":
	case c.Ahead == 0:
		parts = append(parts, "tagged "+c.Tag)
	case c.Ahead == 1:
		parts = append(parts, "1 commit since "+c.Tag)
	default:
		parts = append(parts, fmt.Sprintf("%d commits since %s", c.Ahead, c.Tag))
	}
	return strings.Join(parts, " · ")
}

func (r *run) card(slot string, c *Code) {
	if r.setting("repo") == "" {
		r.setCard(slot, []plugin.Block{
			{Type: plugin.BlockText, Text: "No repo linked yet. Link one to follow its pull requests and read its version.", Tone: "muted"},
			{Type: plugin.BlockButtons, Buttons: []plugin.Button{{Label: "Link repo", Pick: "repo"}}},
		})
		r.res.Signal(slot, plugin.LevelQuiet, "")
		return
	}
	if c == nil {
		r.setCard(slot, []plugin.Block{
			{Type: plugin.BlockStat, Label: "Not checked yet", Value: parseRepoOr(r.setting("repo")), Sub: "Enplace checks every 15 minutes.", Tone: "muted"},
			{Type: plugin.BlockButtons, Buttons: []plugin.Button{{Label: "Check now", Action: "check"}}},
		})
		return
	}
	blocks := []plugin.Block{}
	v := r.version()
	stat := plugin.Block{Type: plugin.BlockStat, Label: "Branch", Value: c.Branch, Sub: where(c)}
	if v != nil {
		stat.Label, stat.Value = "Version", v.Version
		if v.File != "" {
			stat.Sub = v.File + " · " + stat.Sub
		}
	} else if c.Branch != "" {
		stat.Sub = strings.TrimPrefix(strings.TrimPrefix(stat.Sub, c.Branch), " · ")
	}
	blocks = append(blocks, stat)
	if c.Error != "" {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: c.Error, Tone: "warn"})
	} else if c.Message != "" {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: "Latest: " + c.Message + " · " + ago(c.CommitAt, r.req.Now), Tone: "muted"})
	}
	btns := []plugin.Button{}
	if c.URL != "" {
		btns = append(btns, plugin.Button{Label: "Open repo", URL: c.URL})
	}
	btns = append(btns, plugin.Button{Label: "Check now", Action: "check"})
	blocks = append(blocks, plugin.Block{Type: plugin.BlockButtons, Buttons: btns})
	r.setCard(slot, blocks)

	// New code is everyday news; only a broken link needs the person.
	switch {
	case c.Error != "":
		r.res.Signal(slot, plugin.LevelNormal, "Can't read "+c.Repo)
		r.res.Say(slot, "Enplace can't read "+c.Repo+": "+c.Error, "Can't read")
	case v != nil:
		r.res.Signal(slot, plugin.LevelQuiet, "")
		r.res.Say(slot, "The code says "+v.Version+" ("+where(c)+").", v.Version)
	default:
		r.res.Signal(slot, plugin.LevelQuiet, "")
	}
}

func parseRepoOr(s string) string {
	if r := parseRepo(s); r != "" {
		return r
	}
	return s
}

func (r *run) panel(c *Code) []plugin.Block {
	if r.setting("repo") == "" {
		return []plugin.Block{{Type: plugin.BlockText, Text: "Add the repo above. Enplace then checks it every 15 minutes, and with Versions on, reads the version from it.", Tone: "muted"}}
	}
	if c == nil {
		return []plugin.Block{
			{Type: plugin.BlockText, Text: "Not checked yet.", Tone: "muted"},
			{Type: plugin.BlockButtons, Buttons: []plugin.Button{{Label: "Check now", Action: "check"}}},
		}
	}
	var blocks []plugin.Block
	if c.Error != "" {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: c.Error, Tone: "warn"})
	}
	if v := r.version(); v != nil {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: fmt.Sprintf("Versions reads %s from %s. Where the version lives and how it's bumped are Versions' settings.", v.Version, v.File)})
	} else {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: "Switch on Versions to read the version from the code and bump it with a pull request.", Tone: "muted"})
	}
	line := "Checked " + ago(c.CheckedAt, r.req.Now)
	if c.Branch != "" {
		line = "On " + c.Branch + " · " + strings.ToLower(line[:1]) + line[1:]
	}
	blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: line, Tone: "muted"})
	btns := []plugin.Button{{Label: "Check now", Action: "check"}}
	if c.URL != "" {
		btns = append(btns, plugin.Button{Label: "Open repo", URL: c.URL})
	}
	return append(blocks, plugin.Block{Type: plugin.BlockButtons, Buttons: btns})
}

func (r *run) tool(work string) map[string]any {
	c := r.current()
	if r.setting("repo") == "" {
		return map[string]any{"linked": false, "how_to": "Set the github plugin's repo setting to owner/name or the repo's link (update_project with plugin_settings). The version is the versions plugin's: get_version."}
	}
	if c == nil {
		return map[string]any{"linked": true, "repo": r.setting("repo"), "checked": false}
	}
	out := map[string]any{"linked": true, "checked": true, "repo": c.Repo, "url": c.URL, "branch": c.Branch,
		"latest_tag": c.Tag, "tag_version": c.TagVersion, "commits_since_tag": c.Ahead,
		"head": map[string]any{"sha": c.SHA, "message": c.Message, "at": c.CommitAt, "url": c.CommitURL}, "checked_at": c.CheckedAt}
	if v := r.version(); v != nil {
		out["version"], out["version_file"] = v.Version, v.File
		if v.Build != "" {
			out["build"] = v.Build
		}
	}
	if c.Error != "" {
		out["error"] = c.Error
	}
	if work = strings.ToUpper(strings.TrimSpace(work)); work != "" {
		ls := Links{}
		r.req.State.GetProject(r.pid, keyLinks, &ls)
		list := ls[work]
		if list == nil {
			list = []Link{}
		}
		out["work"] = map[string]any{"key": work, "links": list, "how_to_link": "Put " + work + " in a branch name, pull request title or commit message."}
	}
	return out
}

func ago(t, now time.Time) string {
	if t.IsZero() {
		return "a while ago"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	}
	return fmt.Sprintf("%d days ago", int(d.Hours()/24))
}
