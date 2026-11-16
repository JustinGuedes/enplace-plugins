// GitHub Issues keeps a project's work in step with a repository's issues.
//
// It extends Work sync, so this is only the client for GitHub: list the
// repo's issues (never its pull requests), change one, open one, close one.
// The repo and token default to the GitHub plugin's, so a project that has
// GitHub on needs nothing more to start.
package main

import (
	"cmp"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin/worksync"
)

func main() { plugin.Run(handle) }

func handle(req *plugin.Request) (*plugin.Response, error) { return run(req, &source{}) }

func run(req *plugin.Request, src *source) (*plugin.Response, error) {
	return worksync.Handle(req, worksync.Config{Name: "GitHub Issues", Noun: "issue", Source: src, Hold: src.hold, Skip: []string{"priority", "due"}})
}

// hold says whether a new issue waits in the tray: only while the person
// wants the tray (the other choices go through Approvals or straight in,
// as the project's permission says), and never for an issue they opened
// themselves or one with a "bring straight in" label.
func (src *source) hold(s *worksync.Sync, it worksync.Item) bool {
	if m := s.Setting("new_issues"); m != "" && m != trayMode {
		return false
	}
	if me := src.me(s); me != "" && strings.EqualFold(it.Author, me) {
		return false
	}
	for _, l := range strings.Split(s.Setting("bring_labels"), ",") {
		if l = strings.TrimSpace(l); l != "" && slices.ContainsFunc(src.raw[it.ID], func(x string) bool { return strings.EqualFold(x, l) }) {
			return false
		}
	}
	return true
}

// me is the token's own GitHub login, read once and kept.
func (src *source) me(s *worksync.Sync) string {
	if src.login != nil {
		return *src.login
	}
	login := ""
	src.login = &login
	tok := s.Secret("token")
	if tok == "" {
		return ""
	}
	var kept struct {
		Login string `json:"login"`
		Token string `json:"token"` // a hash of the token it's for
	}
	sum := tokenHash(tok)
	if s.Req.State.GetProject(s.ProjectID, keyMe, &kept) && kept.Token == sum {
		login = kept.Login
		return login
	}
	c, err := src.client(s)
	if err != nil {
		return ""
	}
	if login, err = c.me(); err != nil {
		return ""
	}
	src.login = &login
	kept.Login, kept.Token = login, sum
	s.Res.SetProject(s.ProjectID, keyMe, kept)
	return login
}

// Statuses as the panel shows them. Open issues sit in whatever stage
// the work is in, so only closing and opening move work across.
const (
	stOpen       = "Open"
	stClosed     = "Closed"
	stNotPlanned = "Closed as not planned"
	// Stale linked issues fetched one by one in a run, at most.
	maxSingles = 20
	// Pages of 100 open issues read in a run, at most.
	maxPages = 10
	keyMe    = "gh.me"
	trayMode = "Wait in the tray"
)

type source struct {
	c *client
	// fetch replaces plugin.Fetch in tests.
	fetch func(plugin.FetchRequest) (*plugin.FetchResponse, error)
	// login is the token's own GitHub login, once read.
	login *string
	// raw is every label each issue had when it was last read.
	raw map[string][]string
}

func (src *source) client(s *worksync.Sync) (*client, error) {
	if src.c != nil {
		return src.c, nil
	}
	repo := cleanRepo(s.Setting("repo"))
	if repo == "" {
		repo = peerRepo(s)
	}
	if repo == "" {
		return nil, worksync.ErrNotSet
	}
	fetch := s.Fetch
	if src.fetch != nil {
		fetch = src.fetch
	}
	src.c = &client{fetch: fetch, token: s.Secret("token"), repo: repo}
	return src.c, nil
}

// peerRepo is the GitHub plugin's repo for the project, from what it
// publishes as "public.code".
func peerRepo(s *worksync.Sync) string {
	var code struct {
		Repo string `json:"repo"`
	}
	if st, ok := s.Req.Peers["github"]; ok && st.GetProject(s.ProjectID, "public.code", &code) {
		return cleanRepo(code.Repo)
	}
	return ""
}

// filter is the "Only issues labelled" setting.
func filter(s *worksync.Sync) []string {
	var out []string
	for _, l := range strings.Split(s.Setting("labels"), ",") {
		if l = strings.TrimSpace(l); l != "" && !slices.Contains(out, l) {
			out = append(out, l)
		}
	}
	return out
}

// lastOK is when the sync last finished, from Work sync's own status.
func lastOK(s *worksync.Sync) time.Time {
	var st struct {
		LastOK time.Time `json:"last_ok"`
	}
	s.Req.State.GetProject(s.ProjectID, "sync.status", &st)
	return st.LastOK
}

// List reads every open issue, the ones closed since the last sync, and
// any linked issue whose work changed here but that's in neither, so a
// change made in Enplace always has its issue to go to.
func (src *source) List(s *worksync.Sync) ([]worksync.Item, bool, error) {
	c, err := src.client(s)
	if err != nil {
		return nil, false, err
	}
	labels := filter(s)
	q := url.Values{"state": {"open"}, "sort": {"created"}, "direction": {"asc"}}
	if len(labels) > 0 {
		q.Set("labels", strings.Join(labels, ","))
	}
	got, err := c.list(q, maxPages)
	if err != nil {
		return nil, false, err
	}
	if t := lastOK(s); !t.IsZero() {
		q := url.Values{"state": {"closed"}, "since": {t.Add(-2 * time.Minute).UTC().Format(time.RFC3339)}}
		closed, err := c.list(q, 3)
		if err != nil {
			return nil, false, err
		}
		got = append(got, closed...)
	}
	seen := map[string]bool{}
	var items []worksync.Item
	for _, is := range got {
		id := strconv.Itoa(is.Number)
		if is.PullRequest != nil || seen[id] {
			continue
		}
		seen[id] = true
		items = append(items, src.item(s, is))
	}

	// Linked issues the lists above missed (closed long ago, or the
	// label filter dropped them) whose work changed since they agreed.
	changed := map[string]time.Time{}
	for _, w := range s.Req.Work {
		if w.ProjectID == s.ProjectID {
			changed[w.ID] = w.UpdatedAt
		}
	}
	singles := 0
	for id, l := range s.Links() {
		if seen[id] || l.State != worksync.Linked || singles >= maxSingles {
			continue
		}
		up, ok := changed[l.Work]
		if !ok || !up.After(l.Synced) {
			continue
		}
		singles++
		is, err := c.get(id)
		if err != nil {
			if _, auth := err.(*worksync.AuthError); auth {
				// A single issue gone (moved to another repo) isn't a
				// broken token: leave it be.
				continue
			}
			return nil, false, err
		}
		if is.PullRequest == nil {
			items = append(items, src.item(s, *is))
		}
	}
	// Never a full list: closed issues stay out, so a missing one wasn't
	// removed.
	return items, false, nil
}

// item is an issue in Enplace's terms.
func (src *source) item(s *worksync.Sync, is issue) worksync.Item {
	id := strconv.Itoa(is.Number)
	it := worksync.Item{ID: id, Ref: "#" + id, URL: is.HTMLURL, Title: is.Title, Body: is.Body, UpdatedAt: is.UpdatedAt}
	if is.User != nil {
		it.Author = is.User.Login
	}
	if is.Assignee != nil {
		it.Assignee = "@" + is.Assignee.Login
	}
	if is.Milestone != nil && is.Milestone.Title != "" {
		it.Assignee = strings.TrimPrefix(strings.Join([]string{it.Assignee, "milestone " + is.Milestone.Title}, " · "), " · ")
	}
	filtered := filter(s)
	if src.raw == nil {
		src.raw = map[string][]string{}
	}
	src.raw[id] = nil
	for _, l := range is.Labels {
		src.raw[id] = append(src.raw[id], l.Name)
		switch {
		case slices.Contains(filtered, l.Name):
			// The filter's own labels aren't tags; they go back on by
			// themselves.
		case s.Mapped(l.Name):
			// A label the person maps to a stage, like in-review: it's
			// where an open issue is, never a tag.
			if is.State == "open" && it.Status == "" {
				it.Status = l.Name
			}
		default:
			it.Labels = append(it.Labels, l.Name)
		}
	}
	if is.State == "closed" {
		it.Status, it.StatusKind = stClosed, "done"
		if is.StateReason == "not_planned" {
			it.Status = stNotPlanned
		}
		return it
	}
	if it.Status != "" {
		return it // the mapping places it
	}
	it.Status, it.StatusKind = stOpen, "todo"
	// An open issue fits any stage that isn't done, so linked work keeps
	// the stage it's in until the issue closes.
	if l := s.Links()[id]; l != nil {
		if k := s.StageKind(l.Base.Stage); k != "" && k != "done" {
			it.Stage = l.Base.Stage
		}
	}
	return it
}

// Update changes an issue: title, body, labels, and open or closed.
func (src *source) Update(s *worksync.Sync, it worksync.Item, ch worksync.Change) (worksync.Item, error) {
	c, err := src.client(s)
	if err != nil {
		return it, err
	}
	body := map[string]any{}
	if ch.Title != nil {
		body["title"] = *ch.Title
	}
	if ch.Body != nil {
		body["body"] = *ch.Body
	}
	tags, relabel := it.Labels, false
	if ch.Labels != nil {
		tags, relabel = *ch.Labels, true
	}
	// Where the issue goes: open or closed, and which stage label (one
	// the "stages" setting names, like in-review) it carries while open.
	closed := it.StatusKind == "done"
	status := it.Status
	if ch.StatusKind != nil {
		switch done := *ch.StatusKind == "done"; {
		case done && !closed:
			body["state"], body["state_reason"] = "closed", "completed"
			status, closed = stClosed, true
		case !done:
			if closed {
				body["state"] = "open"
				closed = false
			}
			status = stOpen
			if ch.Status != nil && s.Mapped(*ch.Status) {
				status = *ch.Status
			}
		}
		if status != it.Status && (s.Mapped(status) || s.Mapped(it.Status)) {
			relabel = true
		}
	}
	if relabel {
		out := filter(s)
		add := func(l string) {
			if l != "" && !slices.Contains(out, l) {
				out = append(out, l)
			}
		}
		if closed {
			// A closed issue keeps the stage labels it had.
			for _, l := range src.raw[it.ID] {
				if s.Mapped(l) {
					add(l)
				}
			}
		} else if s.Mapped(status) {
			add(status)
		}
		for _, l := range tags {
			if !s.Mapped(l) {
				add(l)
			}
		}
		body["labels"] = out
	}
	if len(body) == 0 {
		return it, nil
	}
	is, err := c.send("PATCH", "/issues/"+it.ID, body)
	if err != nil {
		return it, err
	}
	return src.item(s, *is), nil
}

// Create opens an issue for new work, when the person switched that on.
func (src *source) Create(s *worksync.Sync, w plugin.Work, it worksync.Item) (worksync.Item, error) {
	c, err := src.client(s)
	if err != nil {
		return it, err
	}
	labels := filter(s)
	for _, l := range it.Labels {
		if !slices.Contains(labels, l) {
			labels = append(labels, l)
		}
	}
	body := map[string]any{"title": cmp.Or(strings.TrimSpace(w.Title), w.Key), "body": w.Body}
	if len(labels) > 0 {
		body["labels"] = labels
	}
	is, err := c.send("POST", "/issues", body)
	if err != nil {
		return it, err
	}
	return src.item(s, *is), nil
}

// Close closes the issue of work deleted in Enplace, as not planned.
func (src *source) Close(s *worksync.Sync, it worksync.Item) error {
	if it.StatusKind == "done" {
		return nil
	}
	c, err := src.client(s)
	if err != nil {
		return err
	}
	_, err = c.send("PATCH", "/issues/"+it.ID, map[string]any{"state": "closed", "state_reason": "not_planned"})
	return err
}
