package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

const (
	keyLinks = "links"
	// perItem is how many links are kept for each work item, newest first.
	perItem = 8
	// maxItems is how many work items keep links.
	maxItems = 300
	// maxDetails is how many open pull requests get their size, checks and
	// merge state read each run: the ones read longest ago (or never) go
	// first, so none is left out for good (ENP-105).
	maxDetails = 20
)

// Link is a pull request or commit that mentions a work item's key, in its
// title, branch or message.
type Link struct {
	Kind   string    `json:"kind"` // pr, commit, or branch (pushed with no pull request)
	Number int       `json:"number,omitempty"`
	Title  string    `json:"title"`
	URL    string    `json:"url"`
	State  string    `json:"state,omitempty"` // open, draft, merged or closed (PRs)
	Branch string    `json:"branch,omitempty"`
	SHA    string    `json:"sha,omitempty"`
	At     time.Time `json:"at"`
	// Opened is when a pull request was opened.
	Opened time.Time `json:"opened,omitempty"`
	// Adds and Dels are a pull request's lines added and taken out, and
	// Checks the checks on its latest commit, read while it's open.
	Adds   int     `json:"adds,omitempty"`
	Dels   int     `json:"dels,omitempty"`
	Checks []Check `json:"checks,omitempty"`
	// Merge is GitHub's word on merging an open pull request now (clean,
	// blocked, dirty…), and Base the branch it merges into.
	Merge string `json:"merge,omitempty"`
	Base  string `json:"base,omitempty"`
	// Read is when the details above were last read from GitHub. It stays
	// empty while GitHub is still working out the merge state, so the next
	// run asks again first.
	Read time.Time `json:"read,omitempty"`
}

// Check is one check on a pull request: pass, fail or run (not done yet).
type Check struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

// Links are each work item's links, by its key (like ENP-12).
type Links map[string][]Link

// keyRe finds work keys of one project, like ENP-12 or enp-12 in a branch.
// mentions checks what's around each match.
func keyRe(project string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)(` + regexp.QuoteMeta(project) + `)[-_]?([0-9]{1,6})`)
}

// mentions are the work keys a text mentions, in upper case. A key must
// stand on its own: "xENP-1" and "ENP-12" in "ENP-123" don't count.
func mentions(re *regexp.Regexp, texts ...string) []string {
	var out []string
	alnum := func(b byte) bool {
		return b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
	}
	for _, t := range texts {
		for _, m := range re.FindAllStringSubmatchIndex(t, -1) {
			if m[0] > 0 && alnum(t[m[0]-1]) || m[1] < len(t) && t[m[1]] >= '0' && t[m[1]] <= '9' {
				continue
			}
			n := strings.TrimLeft(t[m[4]:m[5]], "0")
			if n == "" {
				continue
			}
			k := strings.ToUpper(t[m[2]:m[3]]) + "-" + n
			if !contains(out, k) {
				out = append(out, k)
			}
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func prLink(p pull) Link {
	l := Link{Kind: "pr", Number: p.Number, Title: p.Title, URL: p.HTMLURL, Branch: p.Head.Ref, SHA: p.Head.SHA, At: p.Updated, Opened: p.Created}
	switch {
	case !p.MergedAt.IsZero():
		l.State, l.At = "merged", p.MergedAt
	case p.State == "closed":
		l.State = "closed"
	case p.Draft:
		l.State = "draft"
	default:
		l.State = "open"
	}
	return l
}

func commitLink(c commit) Link {
	title, _, _ := strings.Cut(c.Commit.Message, "\n")
	return Link{Kind: "commit", Title: title, URL: c.HTMLURL, SHA: c.SHA, At: c.Commit.Committer.Date}
}

// add files l under each key it mentions, replacing what was there for
// the same link, newest first.
func (ls Links) add(keys []string, l Link) {
	for _, k := range keys {
		list := ls[k]
		kept := list[:0:0]
		nl := l
		for _, x := range list {
			if x.URL != l.URL {
				kept = append(kept, x)
				continue
			}
			// Keep what was read before about it until it's read again.
			if nl.Adds == 0 && nl.Dels == 0 && nl.Checks == nil {
				nl.Adds, nl.Dels, nl.Checks = x.Adds, x.Dels, x.Checks
			}
			if nl.Merge == "" && nl.SHA == x.SHA {
				nl.Merge, nl.Base, nl.Read = x.Merge, x.Base, x.Read
			}
			if nl.Opened.IsZero() {
				nl.Opened = x.Opened
			}
		}
		kept = append(kept, nl)
		sort.SliceStable(kept, func(i, j int) bool { return kept[i].At.After(kept[j].At) })
		if len(kept) > perItem {
			kept = kept[:perItem]
		}
		ls[k] = kept
	}
}

// trim keeps the work items with the newest links.
func (ls Links) trim() {
	if len(ls) <= maxItems {
		return
	}
	type kv struct {
		k  string
		at time.Time
	}
	var all []kv
	for k, v := range ls {
		all = append(all, kv{k, v[0].At})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].at.After(all[j].at) })
	for _, x := range all[maxItems:] {
		delete(ls, x.k)
	}
}

// link reads the latest pull requests (and the branch's commits when it
// moved) and files them under the work items they mention.
func (r *run) link(gh *client, c *Code, moved bool) error {
	key := ""
	for _, p := range r.req.Projects {
		if p.ID == r.pid {
			key = p.Key
		}
	}
	if key == "" {
		return nil
	}
	re := keyRe(key)
	ls := Links{}
	if !r.fresh {
		r.req.State.GetProject(r.pid, keyLinks, &ls)
	}
	if ls == nil {
		ls = Links{}
	}
	prs, err := gh.pulls()
	if err != nil {
		return err
	}
	for _, p := range prs {
		if ks := mentions(re, p.Title, p.Head.Ref); len(ks) > 0 {
			ls.add(ks, prLink(p))
		}
	}
	if moved {
		cs, err := gh.commits(c.Branch)
		if err != nil {
			return err
		}
		for _, cm := range cs {
			if ks := mentions(re, cm.Commit.Message); len(ks) > 0 {
				ls.add(ks, commitLink(cm))
			}
		}
	}
	r.linkBranches(gh, c, prs, ls, re)
	ls.trim()
	details(gh, ls, moved, r.req.Now)
	if r.merged > 0 {
		ls.markMerged(r.merged, r.req.Now)
	}
	r.res.SetProject(r.pid, keyLinks, ls)
	r.linked = true
	r.moveWork(ls)
	return nil
}

// markMerged files pull request n as merged at now under every item it
// mentions, when it's still known as open. It says whether anything changed.
func (ls Links) markMerged(n int, now time.Time) bool {
	changed := false
	for k, list := range ls {
		for i, l := range list {
			if l.Kind == "pr" && l.Number == n && (l.State == "open" || l.State == "draft") {
				list[i].State, list[i].At = "merged", now
				changed = true
			}
		}
		sort.SliceStable(list, func(i, j int) bool { return list[i].At.After(list[j].At) })
		ls[k] = list
	}
	return changed
}

// settling is how long after a merge an item that hasn't reached done yet
// says it's on its way, rather than looking stuck in review.
const settling = time.Hour

// mergedPending is the pull request that merged lately while the item
// hasn't reached done: its move is on the way, or waits for approval.
// Nil when nothing waits, or another pull request for it is still open.
func (r *run) mergedPending(key string, list []Link) *Link {
	if !r.follow() {
		return nil
	}
	var w *plugin.Work
	for i := range r.req.Work {
		if strings.EqualFold(r.req.Work[i].Key, key) {
			w = &r.req.Work[i]
		}
	}
	if w == nil || w.StageKind == "done" || w.StageKind == "cancelled" {
		return nil
	}
	var m *Link
	for i, l := range list {
		if l.Kind != "pr" {
			continue
		}
		if l.State == "open" || l.State == "draft" {
			return nil
		}
		if l.State == "merged" && m == nil && r.req.Now.Sub(l.At) < settling {
			m = &list[i]
		}
	}
	return m
}

// instant is whether GitHub tells Enplace of changes the moment they
// happen (a webhook), rather than being read every 15 minutes.
func (r *run) instant() bool {
	return r.req.Webhook != nil && r.req.Webhook.On
}

// details reads the size, checks and merge state of the open pull
// requests that mention work, up to maxDetails each run, the ones read
// longest ago first. When the branch moved, every merge state may have
// changed (a merge can leave others in conflict), so all of them count as
// unread. What can't be read keeps what was known.
func details(gh *client, ls Links, moved bool, now time.Time) {
	type spot struct {
		k string
		i int
	}
	byURL := map[string][]spot{}
	var order []string
	for _, k := range sortedKeys(ls) {
		for i, l := range ls[k] {
			if l.Kind != "pr" || l.State != "open" && l.State != "draft" {
				continue
			}
			if moved {
				ls[k][i].Read = time.Time{}
			}
			if _, ok := byURL[l.URL]; !ok {
				order = append(order, l.URL)
			}
			byURL[l.URL] = append(byURL[l.URL], spot{k, i})
		}
	}
	first := func(u string) Link { s := byURL[u][0]; return ls[s.k][s.i] }
	sort.SliceStable(order, func(a, b int) bool { return first(order[a]).Read.Before(first(order[b]).Read) })
	if len(order) > maxDetails {
		order = order[:maxDetails]
	}
	for _, u := range order {
		l := first(u)
		size, err := gh.pull(l.Number)
		if err != nil {
			continue
		}
		l.Adds, l.Dels, l.Merge, l.Base = size.Additions, size.Deletions, size.MergeableState, size.Base.Ref
		l.Read = now
		if l.Merge == "" || l.Merge == "unknown" {
			l.Read = time.Time{}
		}
		sha := size.Head.SHA
		if sha == "" {
			sha = l.SHA
		}
		if runs, err := gh.checkRuns(sha); err == nil {
			l.Checks = checksOf(runs)
		}
		for _, s := range byURL[u] {
			ls[s.k][s.i] = l
		}
	}
}

func sortedKeys(ls Links) []string {
	out := make([]string, 0, len(ls))
	for k := range ls {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func checksOf(runs []checkRun) []Check {
	out := []Check{}
	for _, c := range runs {
		st := "run"
		if c.Status == "completed" {
			switch c.Conclusion {
			case "success", "neutral", "skipped":
				st = "pass"
			default:
				st = "fail"
			}
		}
		out = append(out, Check{Name: c.Name, State: st})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// tally counts a pull request's checks.
func (l Link) tally() (pass, fail, total int) {
	for _, c := range l.Checks {
		switch c.State {
		case "pass":
			pass++
		case "fail":
			fail++
		}
	}
	return pass, fail, len(l.Checks)
}

// facts is a pull request's facts in one line: "Draft · 3 of 4 checks · +212 −40".
func (l Link) facts() string {
	parts := []string{stateShort[l.State]}
	parts = append(parts, l.checkWords())
	if l.Adds+l.Dels > 0 {
		parts = append(parts, fmt.Sprintf("+%d −%d", l.Adds, l.Dels))
	}
	return strings.Join(nonEmpty(parts), " · ")
}

// checkWords says how its checks went: "3 of 4 checks".
func (l Link) checkWords() string {
	pass, _, total := l.tally()
	if total == 0 {
		return ""
	}
	return fmt.Sprintf("%d of %d checks", pass, total)
}

func nonEmpty(list []string) []string {
	out := list[:0:0]
	for _, s := range list {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

var stateShort = map[string]string{"open": "Open", "draft": "Draft", "merged": "Merged", "closed": "Closed"}

// strip is a pull request as a strip: its name, a dot per check, its facts
// and a pill for where it is.
func strip(l Link) plugin.Block {
	b := plugin.Block{Type: plugin.BlockStrip, Label: fmt.Sprintf("PR #%d", l.Number), Text: l.Title, Sub: l.facts(),
		Buttons: []plugin.Button{{Label: "Open", URL: l.URL}}}
	for _, c := range l.Checks {
		b.Items = append(b.Items, plugin.Item{Title: c.Name, Done: c.State == "pass", Locked: c.State == "fail"})
	}
	pass, fail, total := l.tally()
	if l.canMerge() {
		base := l.Base
		if base == "" {
			base = "its base branch"
		}
		args, _ := json.Marshal(map[string]any{"number": l.Number, "sha": l.SHA, "base": l.Base})
		b.Buttons = append(b.Buttons, plugin.Button{Label: "Merge", Action: actMerge, Args: args, Confirm: fmt.Sprintf("Merge #%d into %s?", l.Number, base)})
	} else if st := l.mergeStatus(); st != "" {
		b.Buttons = append(b.Buttons, l.statusButton(st))
	}
	switch {
	case l.State == "merged":
		b.Value, b.Tone = "Merged", "done"
	case l.State == "closed":
		b.Value, b.Tone = "Closed", "muted"
	case l.State == "open" && l.Merge == "dirty":
		b.Value, b.Tone = "Conflicts", "warn"
	case fail > 0:
		b.Value, b.Tone = "Checks failing", "warn"
	case total > 0 && pass == total:
		b.Value, b.Tone = "Checks pass", "good"
	case l.State == "draft":
		b.Value, b.Tone = "Draft", "open"
	case total > 0:
		b.Value, b.Tone = "Checks running", "open"
	default:
		b.Value, b.Tone = "Open", "open"
	}
	return b
}

// actMerge is the action that merges a pull request from its work item.
const actMerge = "pr.merge"

// actStatus marks the button that says why an open pull request can't
// merge yet. Cards read it (its args say the state) instead of running
// it; its URL is where to sort it out on GitHub (ENP-105).
const actStatus = "pr.status"

// mergeStatus is why an open pull request can't merge now, in one word
// the card understands: draft, conflicts, behind, failing, blocked,
// waiting (checks still running) or checking (GitHub hasn't said yet). Empty
// when it can merge, or isn't open.
func (l Link) mergeStatus() string {
	if l.Kind == "pr" && l.State == "draft" {
		// GitHub won't merge a draft until it's marked ready for review.
		return "draft"
	}
	if l.Kind != "pr" || l.State != "open" || l.canMerge() {
		return ""
	}
	pass, fail, total := l.tally()
	switch {
	case l.Merge == "dirty":
		return "conflicts"
	case fail > 0:
		return "failing"
	case l.Merge == "behind":
		return "behind"
	case l.Merge == "blocked":
		return "blocked"
	case l.Merge == "unstable" || total > pass:
		return "waiting"
	default:
		return "checking"
	}
}

// statusButton is the pr.status button for st: a sentence for people, and
// a link to the page on GitHub that sorts it out.
func (l Link) statusButton(st string) plugin.Button {
	base := l.Base
	if base == "" {
		base = "its base branch"
	}
	label, url := "", l.URL
	switch st {
	case "draft":
		label = fmt.Sprintf("#%d is a draft: mark it ready for review on GitHub to merge it", l.Number)
	case "conflicts":
		label, url = fmt.Sprintf("#%d has conflicts with %s", l.Number, base), l.URL+"/conflicts"
	case "failing":
		label, url = fmt.Sprintf("#%d has failing checks", l.Number), l.URL+"/checks"
	case "behind":
		label = fmt.Sprintf("#%d is behind %s", l.Number, base)
	case "blocked":
		label = fmt.Sprintf("GitHub won't merge #%d yet: it needs a review or a required check", l.Number)
	case "waiting":
		label = fmt.Sprintf("#%d's checks are still running", l.Number)
	default:
		label = fmt.Sprintf("Asking GitHub if #%d can merge", l.Number)
	}
	args, _ := json.Marshal(map[string]any{"number": l.Number, "state": st, "base": l.Base})
	return plugin.Button{Label: label, Action: actStatus, Args: args, URL: url}
}

// canMerge is an open pull request GitHub says can merge now, with no
// check failing: the work item's card then offers Merge.
func (l Link) canMerge() bool {
	if l.Kind != "pr" || l.State != "open" {
		return false
	}
	if _, fail, _ := l.tally(); fail > 0 {
		return false
	}
	return l.Merge == "clean" || l.Merge == "has_hooks"
}

// workPanel is what a work item's panel shows: a strip for each pull
// request that mentions it, the commits that do, and what happened to
// them as events for the item's stream.
func (r *run) workPanel(c *Code) []plugin.Block {
	if c == nil || r.req.WorkKey == "" {
		return nil
	}
	ls := Links{}
	r.req.State.GetProject(r.pid, keyLinks, &ls)
	key := strings.ToUpper(r.req.WorkKey)
	list := ls[key]
	if len(list) == 0 {
		return []plugin.Block{{Type: plugin.BlockStrip, Text: "No code yet", Value: "Waiting", Tone: "waiting",
			Sub: "Links itself when a branch, pull request or commit in " + c.Repo + " mentions " + key + "."}}
	}
	var out []plugin.Block
	if m := r.mergedPending(key, list); m != nil {
		out = append(out, plugin.Block{Type: plugin.BlockStrip, Label: fmt.Sprintf("PR #%d", m.Number),
			Text: "Merged on GitHub, moving to Done soon", Value: "Merged", Tone: "done",
			Sub: "If the plugin's Move work permission asks first, the move waits for you in Approvals."})
	}
	var commits []plugin.Item
	var events []plugin.Item
	at := func(t time.Time) string { return t.UTC().Format(time.RFC3339) }
	for _, l := range list {
		switch l.Kind {
		case "pr":
			out = append(out, strip(l))
			name := fmt.Sprintf("#%d %s", l.Number, l.Title)
			link := func(facts string) *plugin.Button { return &plugin.Button{Label: facts, URL: l.URL} }
			checks := l.checkWords()
			opened := "Opened a pull request"
			if l.State == "draft" {
				opened = "Opened a draft pull request"
			}
			switch l.State {
			case "merged":
				if !l.Opened.IsZero() {
					events = append(events, plugin.Item{Title: "Opened a pull request", Sub: name, Value: at(l.Opened), Icon: "code", Button: link("")})
				}
				events = append(events, plugin.Item{Title: "Merged the pull request", Sub: name, Value: at(l.At), Icon: "check", Button: link(l.facts())})
			case "closed":
				if !l.Opened.IsZero() {
					events = append(events, plugin.Item{Title: "Opened a pull request", Sub: name, Value: at(l.Opened), Icon: "code", Button: link("")})
				}
				events = append(events, plugin.Item{Title: "Closed the pull request without merging", Sub: name, Value: at(l.At), Icon: "code", Button: link("")})
			default:
				when := l.Opened
				if when.IsZero() {
					when = l.At
				}
				events = append(events, plugin.Item{Title: opened, Sub: name, Value: at(when), Icon: "code", Button: link(checks)})
			}
		case "branch":
			out = append(out, branchStrip(l, r.req.Now))
			events = append(events, plugin.Item{Title: "Pushed code with no pull request", Sub: l.Branch + ": " + l.Title, Value: at(l.At), Icon: "code",
				Button: &plugin.Button{Label: "Open a pull request", URL: l.URL}})
		default:
			sha := l.SHA
			if len(sha) > 7 {
				sha = sha[:7]
			}
			sub := "Commit " + sha
			if c.Branch != "" {
				sub += " on " + c.Branch
			}
			commits = append(commits, plugin.Item{Icon: "code", Title: l.Title, Sub: sub + " · " + ago(l.At, r.req.Now), Button: &plugin.Button{Label: "Open", URL: l.URL}})
			events = append(events, plugin.Item{Title: "Committed", Sub: l.Title, Value: at(l.At), Icon: "code", Button: &plugin.Button{Label: sha, URL: l.URL}})
		}
	}
	if len(commits) > 0 {
		out = append(out, plugin.Block{Type: plugin.BlockList, Items: commits})
	}
	if len(events) > 0 {
		out = append(out, plugin.Block{Type: plugin.BlockEvents, Items: events})
	}
	if r.follow() {
		text := "This item moves with its pull request: opened to review, merged to done."
		if !r.instant() {
			text += " Instant updates are off, so GitHub is read every 15 minutes and a merge can take that long to show: turn them on in the plugin's settings to move it the moment it merges."
		}
		out = append(out, plugin.Block{Type: plugin.BlockText, Tone: "muted", Text: text})
	}
	return out
}
