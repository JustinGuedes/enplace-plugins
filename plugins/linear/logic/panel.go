package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin/worksync"
)

const (
	slotPanel   = plugin.SlotSettingsPanel
	allProjects = "All of the team's issues"
)

// action answers Linear's own buttons, and the agent tool.
func (src *source) action(s *worksync.Sync, name string, raw json.RawMessage) error {
	var args struct {
		Team    string `json:"team"`
		Project string `json:"project"`
		Work    string `json:"work"`
	}
	_ = json.Unmarshal(raw, &args)
	switch name {
	case "linear.check":
		t := src.checkTeams(s, true)
		switch {
		case t.Error != "":
			s.Res.Toast = "Linear: " + t.Error
		case t.Key == "":
			s.Res.Toast = "Add a Linear API key first"
		default:
			s.Res.Toast = fmt.Sprintf("The key sees %d %s", len(t.Teams), plural(len(t.Teams), "team"))
		}
	case "linear.team":
		if s.ProjectID == "" {
			return nil
		}
		var t Teams
		getTeams(s, &t)
		var chosen *team
		for i := range t.Teams {
			if teamLabel(t.Teams[i]) == args.Team || strings.EqualFold(t.Teams[i].Key, args.Team) || t.Teams[i].ID == args.Team {
				chosen = &t.Teams[i]
			}
		}
		if chosen == nil {
			s.Res.Toast = "Pick one of the teams the key can see"
			return nil
		}
		p := pick(s)
		if p.Team.ID != chosen.ID {
			p = Pick{Team: *chosen}
		}
		src.setPick(s, p)
		if _, err := src.teamInfo(s, true); err != nil {
			s.Res.Toast = "Linear: " + err.Error()
			return nil
		}
		// The first sync now, so the team's open issues come in as one
		// approval straight away.
		s.Run()
		s.Res.Toast = "Linked to " + chosen.Name + " in Linear"
		s.Res.Log = "Linked the project to Linear's " + chosen.Name + " team"
	case "linear.project":
		p := pick(s)
		if s.ProjectID == "" || p.Team.ID == "" {
			return nil
		}
		p.Project, p.ProjectName = "", ""
		if args.Project != "" && args.Project != allProjects {
			info, err := src.teamInfo(s, false)
			if err != nil {
				s.Res.Toast = "Linear: " + err.Error()
				return nil
			}
			for _, lp := range info.Projects {
				if lp.Name == args.Project || lp.ID == args.Project {
					p.Project, p.ProjectName = lp.ID, lp.Name
				}
			}
			if p.Project == "" {
				s.Res.Toast = "That project isn't in " + p.Team.Name
				return nil
			}
		}
		src.setPick(s, p)
		if p.Project == "" {
			s.Res.Toast = "Syncing all of " + p.Team.Name + "'s issues"
		} else {
			s.Res.Toast = "Syncing only " + p.ProjectName
		}
	case "tool_issue":
		s.Res.Data = tool(s, args.Work)
	}
	return nil
}

// setPick keeps the project's pick, and uses it for the rest of the call.
func (src *source) setPick(s *worksync.Sync, p Pick) {
	s.Res.SetProject(s.ProjectID, keyPick, p)
	s.Req.State.PutProject(s.ProjectID, keyPick, p)
	src.info = nil
}

// tool is get_linear_issue: a work item's Linear issue, with its latest
// comments, as last read by the sync.
func tool(s *worksync.Sync, ref string) any {
	var w *plugin.Work
	for i := range s.Req.Work {
		if strings.EqualFold(s.Req.Work[i].Key, ref) || s.Req.Work[i].ID == ref {
			w = &s.Req.Work[i]
		}
	}
	if w == nil {
		return map[string]any{"error": "no work item " + ref + " in a project linked to Linear"}
	}
	id := w.Fields[s.Field()]
	if id == "" {
		return map[string]any{"work": w.Key, "error": w.Key + " isn't linked to a Linear issue"}
	}
	var x extras
	if !s.Req.State.GetProject(w.ProjectID, keyIssue+id, &x) {
		return map[string]any{"work": w.Key, "issue": id, "comments": []any{}, "note": "Not read from Linear yet; the next sync fills this in."}
	}
	type out struct {
		Author string `json:"author"`
		At     string `json:"at"`
		Body   string `json:"body"`
	}
	comments := []out{}
	for _, c := range x.Comments {
		comments = append(comments, out{Author: c.User.String(), At: c.CreatedAt.UTC().Format("2006-01-02T15:04Z"), Body: c.Body})
	}
	res := map[string]any{"work": w.Key, "issue": x.Ref, "url": x.URL, "status": x.Status, "assignee": x.Assignee,
		"comments": comments, "read_at": x.Read.UTC().Format("2006-01-02T15:04Z")}
	if x.Parent != "" {
		res["parent"] = x.Parent
	}
	if x.Project != "" {
		res["linear_project"] = x.Project
	}
	if x.Points != "" {
		res["points"] = x.Points
	}
	return res
}

func (src *source) render(s *worksync.Sync, slot string) []plugin.Block {
	if slot != slotPanel {
		return nil
	}
	var t Teams
	getTeams(s, &t)
	check := plugin.Block{Type: plugin.BlockButtons, Buttons: []plugin.Button{{Label: "Check key", Action: "linear.check"}}}
	var out []plugin.Block
	switch {
	case t.Error != "":
		out = append(out, plugin.Block{Type: plugin.BlockText, Tone: "warn", Text: "Linear: " + t.Error + "."}, check)
		return out
	case len(t.Teams) == 0:
		out = append(out, plugin.Block{Type: plugin.BlockText, Text: "Add a Linear personal API key below (in Linear: Settings › Security & access › Personal API keys), then press Check key to list its teams."}, check)
		return out
	}
	names := make([]string, 0, len(t.Teams))
	for _, tm := range t.Teams {
		names = append(names, teamLabel(tm))
	}
	who := "The key"
	if t.Viewer != "" {
		who = t.Viewer + "'s key"
	}
	out = append(out, plugin.Block{Type: plugin.BlockText, Text: fmt.Sprintf("%s sees %d %s: %s.", who, len(t.Teams), plural(len(t.Teams), "team"), strings.Join(names, ", "))})
	if s.ProjectID == "" {
		return append(out, plugin.Block{Type: plugin.BlockText, Tone: "muted", Text: "Pick a team in each project's Linear settings."}, check)
	}

	p := pick(s)
	cur := ""
	for _, tm := range t.Teams {
		if tm.ID == p.Team.ID {
			cur = teamLabel(tm)
		}
	}
	options := names
	if cur == "" {
		options = append([]string{""}, names...)
	}
	text := "Which Linear team is this project's work? Its open issues come in as one approval."
	if p.Team.ID != "" {
		text = "Changing the team leaves work that's already linked as it is."
	}
	out = append(out, plugin.Block{Type: plugin.BlockForm, Text: text,
		Fields:  []plugin.Field{{Key: "team", Label: "Team", Type: "select", Options: options, Value: cur, Required: true}},
		Buttons: []plugin.Button{{Label: "Use this team", Action: "linear.team"}}})
	if p.Team.ID == "" {
		return out
	}

	var kept keptTeam
	if !s.Req.State.GetProject(s.ProjectID, keyTeam, &kept) || kept.Info.ID != p.Team.ID {
		return append(out, plugin.Block{Type: plugin.BlockText, Tone: "muted", Text: "Reading " + p.Team.Name + "'s statuses…"})
	}
	projects := []string{allProjects}
	for _, lp := range kept.Info.Projects {
		if (lp.State != "canceled" && lp.State != "completed") || lp.ID == p.Project {
			projects = append(projects, lp.Name)
		}
	}
	chosen := allProjects
	if p.ProjectName != "" {
		chosen = p.ProjectName
	}
	out = append(out, plugin.Block{Type: plugin.BlockForm, Text: "Only bring in issues from one Linear project (optional).",
		Fields:  []plugin.Field{{Key: "project", Label: "Linear project", Type: "select", Options: projects, Value: chosen}},
		Buttons: []plugin.Button{{Label: "Save", Action: "linear.project"}}})

	// How each status lands, so a wrong guess is easy to spot.
	states := append([]state(nil), kept.Info.States...)
	rank := map[string]int{"triage": 0, "backlog": 1, "unstarted": 2, "started": 3, "completed": 4, "canceled": 5}
	sort.SliceStable(states, func(i, j int) bool {
		if rank[states[i].Type] != rank[states[j].Type] {
			return rank[states[i].Type] < rank[states[j].Type]
		}
		return states[i].Position < states[j].Position
	})
	var items []plugin.Item
	for _, st := range states {
		stage := s.StageName(s.StageFor(worksync.Item{Status: st.Name, StatusKind: kindOf(st)}))
		sub := ""
		if s.Mapped(st.Name) {
			sub = "from Statuses to stages"
		} else if st.Type == "canceled" {
			sub = "shows as Canceled on the item"
		}
		items = append(items, plugin.Item{Title: st.Name, Value: stage, Sub: sub})
	}
	if len(items) > 0 {
		out = append(out, plugin.Block{Type: plugin.BlockHeading, Text: "Statuses to stages", Sub: "Change one with the Statuses to stages setting below, like \"Triage = Parked\"."},
			plugin.Block{Type: plugin.BlockList, Items: items})
	}
	return out
}

func plural(n int, w string) string {
	if n == 1 {
		return w
	}
	return w + "s"
}
