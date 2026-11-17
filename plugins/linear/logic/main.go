// Linear keeps a project's work in step with a Linear team's issues.
//
// It extends Work sync, so this is only the client for Linear: list the
// team's issues (the first time the open ones, then only what changed
// since the last sync), change one, open one, cancel one. The team (and
// optionally one Linear project) is picked on the plugin's settings panel
// from the teams the API key can see.
package main

import (
	"cmp"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin/worksync"
)

func main() { plugin.Run(handle) }

func handle(req *plugin.Request) (*plugin.Response, error) { return run(req, &source{}) }

func run(req *plugin.Request, src *source) (*plugin.Response, error) {
	return worksync.Handle(req, worksync.Config{Name: "Linear", Noun: "issue", Source: src, Action: src.action, Render: src.render})
}

const (
	keyTeams = "lin.teams" // what the key can see (Teams)
	keyPick  = "lin.pick"  // the project's team and Linear project (Pick)
	keyTeam  = "lin.team"  // the picked team's statuses, labels, projects (kept)
	keyIssue = "c."        // + identifier: an issue's extras, for the agent tool
	// Pages of 100 issues read in a run, at most.
	maxPages = 10
	// Linked issues fetched by id in a run, at most.
	maxSingles = 50
	// How long the team's statuses and labels are kept before reading again.
	teamFresh = time.Hour
)

// Teams is what the API key can see, kept so the settings panel (which
// never gets the key) can list them.
type Teams struct {
	Key     string    `json:"key"` // hash of the key it's for
	Viewer  string    `json:"viewer,omitempty"`
	Teams   []team    `json:"teams,omitempty"`
	Error   string    `json:"error,omitempty"`
	Checked time.Time `json:"checked"`
}

// Pick is the team (and Linear project, if any) a project syncs with.
type Pick struct {
	Team        team   `json:"team"`
	Project     string `json:"project,omitempty"` // Linear project id
	ProjectName string `json:"project_name,omitempty"`
}

type keptTeam struct {
	Info teamInfo  `json:"info"`
	Read time.Time `json:"read"`
}

// extras is what the agent tool tells about an issue beyond the work item.
type extras struct {
	Ref      string    `json:"ref"`
	URL      string    `json:"url,omitempty"`
	Status   string    `json:"status,omitempty"`
	Assignee string    `json:"assignee,omitempty"`
	Parent   string    `json:"parent,omitempty"`
	Project  string    `json:"project,omitempty"`
	Points   string    `json:"points,omitempty"`
	Comments []comment `json:"comments,omitempty"`
	Read     time.Time `json:"read"`
}

type source struct {
	c *client
	// fetch replaces plugin.Fetch in tests.
	fetch func(plugin.FetchRequest) (*plugin.FetchResponse, error)
	info  *teamInfo
}

// key is the project's own API key, or the workspace's.
func key(s *worksync.Sync) string { return cmp.Or(s.Secret("project_api_key"), s.Secret("api_key")) }

func (src *source) client(s *worksync.Sync) *client {
	if src.c == nil {
		fetch := s.Fetch
		if src.fetch != nil {
			fetch = src.fetch
		}
		src.c = &client{fetch: fetch, key: key(s)}
	}
	return src.c
}

func pick(s *worksync.Sync) Pick {
	var p Pick
	if s.ProjectID != "" {
		s.Req.State.GetProject(s.ProjectID, keyPick, &p)
	}
	return p
}

// teamInfo is the picked team's statuses and labels, read once a run at
// most and kept for an hour (and for the settings panel).
func (src *source) teamInfo(s *worksync.Sync, fresh bool) (*teamInfo, error) {
	if src.info != nil {
		return src.info, nil
	}
	p := pick(s)
	if p.Team.ID == "" {
		return nil, worksync.ErrNotSet
	}
	var kept keptTeam
	if !fresh && s.Req.State.GetProject(s.ProjectID, keyTeam, &kept) && kept.Info.ID == p.Team.ID && s.Req.Now.Sub(kept.Read) < teamFresh {
		src.info = &kept.Info
		return src.info, nil
	}
	info, err := src.client(s).team(p.Team.ID)
	if err != nil {
		return nil, err
	}
	src.info = info
	s.Res.SetProject(s.ProjectID, keyTeam, keptTeam{Info: *info, Read: s.Req.Now})
	return info, nil
}

// lastOK is when the sync last finished, from Work sync's own status.
func lastOK(s *worksync.Sync) time.Time {
	var st struct {
		LastOK time.Time `json:"last_ok"`
	}
	s.Req.State.GetProject(s.ProjectID, "sync.status", &st)
	return st.LastOK
}

// List reads the team's open issues the first time, then the ones changed
// since the last sync, plus any linked issue whose work changed here but
// isn't among them, so a change made in Enplace has its issue to go to.
func (src *source) List(s *worksync.Sync) ([]worksync.Item, bool, error) {
	p := pick(s)
	if p.Team.ID == "" {
		src.checkTeams(s, false)
		return nil, false, worksync.ErrNotSet
	}
	if _, err := src.teamInfo(s, false); err != nil {
		return nil, false, err
	}
	c := src.client(s)
	scope := map[string]any{"team": map[string]any{"id": map[string]any{"eq": p.Team.ID}}}
	if p.Project != "" {
		scope["project"] = map[string]any{"id": map[string]any{"eq": p.Project}}
	}
	filter := map[string]any{}
	for k, v := range scope {
		filter[k] = v
	}
	if t := lastOK(s); t.IsZero() {
		filter["state"] = map[string]any{"type": map[string]any{"nin": []string{"completed", "canceled"}}}
	} else {
		filter["updatedAt"] = map[string]any{"gt": t.Add(-2 * time.Minute).UTC().Format(time.RFC3339)}
	}
	got, err := c.issues(filter, maxPages)
	if err != nil {
		return nil, false, err
	}

	seen := map[string]bool{}
	for _, is := range got {
		seen[is.ID] = true
	}
	changed := map[string]time.Time{}
	for _, w := range s.Req.Work {
		if w.ProjectID == s.ProjectID {
			changed[w.ID] = w.UpdatedAt
		}
	}
	var want []string
	for id, l := range s.Links() {
		if seen[id] || l.State != worksync.Linked {
			continue
		}
		if up, ok := changed[l.Work]; ok && up.After(l.Synced) {
			want = append(want, id)
		}
	}
	sort.Strings(want)
	if len(want) > maxSingles {
		want = want[:maxSingles]
	}
	if len(want) > 0 {
		more, err := c.issues(map[string]any{"id": map[string]any{"in": want}}, 1)
		if err != nil {
			return nil, false, err
		}
		got = append(got, more...)
	}

	items := make([]worksync.Item, 0, len(got))
	for _, is := range got {
		items = append(items, src.item(s, is, true))
	}
	// Never a full list: only what changed comes back.
	return items, false, nil
}

// kindOf is where a Linear status sits in Enplace: any status called
// something like "In Review" is review; otherwise its type says.
func kindOf(st state) string {
	switch st.Type {
	case "completed", "canceled":
		return "done"
	}
	if strings.Contains(strings.ToLower(st.Name), "review") {
		return "review"
	}
	if st.Type == "started" {
		return "doing"
	}
	return "todo"
}

// priority turns Linear's priority (1 urgent .. 4 low) into Enplace's
// (4 urgent .. 1 low) and back: the same sum both ways.
func priority(p int) int {
	if p <= 0 || p > 4 {
		return 0
	}
	return 5 - p
}

func points(e *float64) string {
	if e == nil {
		return ""
	}
	return strconv.FormatFloat(*e, 'f', -1, 64)
}

// item is an issue in Enplace's terms. keep stores what the agent tool
// tells about it.
func (src *source) item(s *worksync.Sync, is issue, keep bool) worksync.Item {
	it := worksync.Item{ID: is.ID, Ref: is.Identifier, URL: is.URL, Title: is.Title, Body: is.Description,
		Status: is.State.Name, StatusKind: kindOf(is.State), Priority: priority(is.Priority), Due: is.DueDate,
		Author: is.Creator.String(), UpdatedAt: is.UpdatedAt, Deleted: is.ArchivedAt != nil || is.Trashed,
		Fields: map[string]string{"points": points(is.Estimate)}}
	for _, l := range is.Labels.Nodes {
		it.Labels = append(it.Labels, l.Name)
	}
	it.Assignee = is.Assignee.who()
	if is.Parent != nil && is.Parent.Identifier != "" {
		// Sub-issues come in as plain work; the panel names the parent.
		it.Info = "sub-issue of " + is.Parent.Identifier
	}
	if keep && s.ProjectID != "" && is.Identifier != "" {
		x := extras{Ref: is.Identifier, URL: is.URL, Status: is.State.Name, Assignee: is.Assignee.String(), Points: points(is.Estimate), Read: s.Req.Now}
		if is.Parent != nil {
			x.Parent = is.Parent.Identifier
		}
		if is.Project != nil {
			x.Project = is.Project.Name
		}
		for _, c := range is.Comments.Nodes {
			if len(c.Body) > 2000 {
				c.Body = c.Body[:2000] + "…"
			}
			x.Comments = append(x.Comments, c)
		}
		s.Res.SetProject(s.ProjectID, keyIssue+is.Identifier, x)
	}
	return it
}

// stateFor is the Linear status to move an issue to: the one the
// project's mapping names, or the team's first status of that kind. It's
// nil when the issue's status already fits.
func stateFor(info *teamInfo, cur worksync.Item, name *string, kind string) *state {
	if name != nil && *name != "" {
		for i := range info.States {
			if strings.EqualFold(info.States[i].Name, *name) {
				if strings.EqualFold(info.States[i].Name, cur.Status) {
					return nil
				}
				return &info.States[i]
			}
		}
	}
	if cur.Status != "" && cur.StatusKind == kind {
		return nil
	}
	states := slices.Clone(info.States)
	sort.SliceStable(states, func(i, j int) bool { return states[i].Position < states[j].Position })
	order := map[string][]string{
		"todo":   {"unstarted", "backlog", "triage"},
		"doing":  {"started"},
		"review": {"started"},
		"done":   {"completed"},
	}[kind]
	for _, t := range order {
		for i := range states {
			if states[i].Type == t && kindOf(states[i]) == kind {
				return &states[i]
			}
		}
	}
	// No status of that kind (a team with no review status): the nearest
	// type will do.
	for _, t := range order {
		for i := range states {
			if states[i].Type == t {
				return &states[i]
			}
		}
	}
	return nil
}

// labelIDs are the ids for tags, adding labels the team doesn't have yet.
func (src *source) labelIDs(s *worksync.Sync, info *teamInfo, names []string) ([]string, error) {
	ids := []string{}
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		var id string
		for _, l := range info.Labels {
			if strings.EqualFold(l.Name, n) {
				id = l.ID
				break
			}
		}
		if id == "" {
			l, err := src.client(s).createLabel(info.ID, n)
			if err != nil {
				return nil, err
			}
			info.Labels = append(info.Labels, *l)
			s.Res.SetProject(s.ProjectID, keyTeam, keptTeam{Info: *info, Read: s.Req.Now})
			id = l.ID
		}
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// Update changes an issue.
func (src *source) Update(s *worksync.Sync, it worksync.Item, ch worksync.Change) (worksync.Item, error) {
	info, err := src.teamInfo(s, false)
	if err != nil {
		return it, err
	}
	in := map[string]any{}
	if ch.Title != nil {
		in["title"] = *ch.Title
	}
	if ch.Body != nil {
		in["description"] = *ch.Body
	}
	if ch.Priority != nil {
		in["priority"] = priority(*ch.Priority)
	}
	if ch.Due != nil {
		if *ch.Due == "" {
			in["dueDate"] = nil
		} else {
			in["dueDate"] = *ch.Due
		}
	}
	if ch.Labels != nil {
		ids, err := src.labelIDs(s, info, *ch.Labels)
		if err != nil {
			return it, err
		}
		in["labelIds"] = ids
	}
	if ch.StatusKind != nil {
		if st := stateFor(info, it, ch.Status, *ch.StatusKind); st != nil {
			in["stateId"] = st.ID
		}
	}
	if ch.Assignee != nil {
		if *ch.Assignee == "" {
			in["assigneeId"] = nil
		} else if id, err := src.userFor(s, *ch.Assignee, ch.AssigneeEmail); err != nil {
			return it, err
		} else if id != "" {
			in["assigneeId"] = id
		}
		// Someone Linear doesn't know: the issue keeps its assignee.
	}
	if len(in) == 0 {
		return it, nil
	}
	is, err := src.client(s).update(it.ID, in)
	if err != nil {
		return it, err
	}
	return src.item(s, *is, true), nil
}

// Create opens an issue for new work, when the person switched that on.
func (src *source) Create(s *worksync.Sync, w plugin.Work, it worksync.Item) (worksync.Item, error) {
	info, err := src.teamInfo(s, false)
	if err != nil {
		return it, err
	}
	in := map[string]any{"teamId": info.ID, "title": cmp.Or(strings.TrimSpace(w.Title), w.Key)}
	if w.Body != "" {
		in["description"] = w.Body
	}
	if w.Priority > 0 {
		in["priority"] = priority(w.Priority)
	}
	if w.Due != "" {
		in["dueDate"] = w.Due
	}
	if p := pick(s); p.Project != "" {
		in["projectId"] = p.Project
	}
	if len(it.Labels) > 0 {
		ids, err := src.labelIDs(s, info, it.Labels)
		if err != nil {
			return it, err
		}
		in["labelIds"] = ids
	}
	if st := stateFor(info, worksync.Item{}, nilIfEmpty(it.Status), cmp.Or(it.StatusKind, "todo")); st != nil {
		in["stateId"] = st.ID
	}
	if w.Assignee != "" {
		id, err := src.userFor(s, w.Assignee, w.AssigneeEmail)
		if err != nil {
			return it, err
		}
		if id != "" {
			in["assigneeId"] = id
		}
	}
	is, err := src.client(s).create(in)
	if err != nil {
		return it, err
	}
	return src.item(s, *is, true), nil
}

// userFor finds the Linear user for an assignee as Enplace has them: by
// the member's email, an email in "Name <email>", or the name.
func (src *source) userFor(s *worksync.Sync, who, email string) (string, error) {
	name := strings.TrimSpace(who)
	if n, e, ok := strings.Cut(name, " <"); ok && strings.HasSuffix(name, ">") {
		name, email = strings.TrimSpace(n), cmp.Or(email, strings.TrimSuffix(e, ">"))
	}
	if strings.Contains(name, "@") && email == "" {
		email = name
	}
	c := src.client(s)
	if email != "" {
		if id, err := c.user("", email); err != nil || id != "" {
			return id, err
		}
	}
	return c.user(name, "")
}

func nilIfEmpty(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

// Close cancels the issue of work deleted in Enplace.
func (src *source) Close(s *worksync.Sync, it worksync.Item) error {
	if it.StatusKind == "done" {
		return nil
	}
	info, err := src.teamInfo(s, false)
	if err != nil {
		return err
	}
	for _, st := range info.States {
		if st.Type == "canceled" {
			_, err := src.client(s).update(it.ID, map[string]any{"stateId": st.ID})
			return err
		}
	}
	return nil
}

// checkTeams reads the teams the key can see, when it hasn't for this key
// yet (or always, when asked), and keeps them for the settings panel.
func (src *source) checkTeams(s *worksync.Sync, always bool) Teams {
	k := key(s)
	var kept Teams
	have := getTeams(s, &kept)
	if k == "" {
		if have && kept.Key != "" {
			src.setTeams(s, Teams{Checked: s.Req.Now})
		}
		return Teams{}
	}
	if have && kept.Key == keyHash(k) && !always {
		return kept
	}
	viewer, teams, err := src.client(s).teams()
	out := Teams{Key: keyHash(k), Viewer: viewer, Teams: teams, Checked: s.Req.Now}
	if err != nil {
		out.Teams, out.Viewer = nil, ""
		out.Error = err.Error()
	} else if len(teams) == 0 {
		out.Error = "the key works, but it can't see any teams"
	}
	src.setTeams(s, out)
	return out
}

// Teams are kept for the project when it has its own key, else for the
// workspace.
func getTeams(s *worksync.Sync, v *Teams) bool {
	if s.ProjectID != "" && s.Req.State.GetProject(s.ProjectID, keyTeams, v) {
		return true
	}
	return s.Req.State.Get(keyTeams, v)
}

func (src *source) setTeams(s *worksync.Sync, t Teams) {
	if s.ProjectID != "" && s.Secret("project_api_key") != "" {
		s.Res.SetProject(s.ProjectID, keyTeams, t)
		return
	}
	if s.ProjectID != "" && s.Req.State.Projects[s.ProjectID][keyTeams] != nil {
		// The project's own key is gone: its teams go with it.
		s.Res.SetProject(s.ProjectID, keyTeams, nil)
	}
	s.Res.Set(keyTeams, t)
}

func teamLabel(t team) string { return fmt.Sprintf("%s (%s)", t.Name, t.Key) }
