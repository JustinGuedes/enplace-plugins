package main

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin/worksync"
)

const api = "https://api.linear.app/graphql"

// client talks to Linear's GraphQL API with a personal API key.
type client struct {
	fetch func(plugin.FetchRequest) (*plugin.FetchResponse, error)
	key   string
}

type team struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Name string `json:"name"`
}

type state struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Type     string  `json:"type"` // triage, backlog, unstarted, started, completed, canceled
	Position float64 `json:"position"`
}

type label struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type lproject struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
}

// teamInfo is what a team's issues need: its statuses, labels (its own
// and the workspace's) and projects.
type teamInfo struct {
	team
	States   []state    `json:"states"`
	Labels   []label    `json:"labels"`
	Projects []lproject `json:"projects"`
}

type person struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email,omitempty"`
}

// who is the person as Enplace matches them: "Sam Rivera <sam@acme.dev>".
func (p *person) who() string {
	if p == nil {
		return ""
	}
	name := cmp.Or(p.Name, p.DisplayName)
	if p.Email == "" {
		return name
	}
	return strings.TrimSpace(name + " <" + p.Email + ">")
}

func (p *person) String() string {
	if p == nil {
		return ""
	}
	if p.DisplayName != "" {
		return p.DisplayName
	}
	return p.Name
}

type comment struct {
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
	User      *person   `json:"user"`
}

type issue struct {
	ID          string     `json:"id"`
	Identifier  string     `json:"identifier"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	URL         string     `json:"url"`
	Priority    int        `json:"priority"` // 0 none, 1 urgent, 2 high, 3 medium, 4 low
	DueDate     string     `json:"dueDate"`
	Estimate    *float64   `json:"estimate"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	ArchivedAt  *time.Time `json:"archivedAt"`
	Trashed     bool       `json:"trashed"`
	State       state      `json:"state"`
	Labels      struct {
		Nodes []label `json:"nodes"`
	} `json:"labels"`
	Assignee *person `json:"assignee"`
	Creator  *person `json:"creator"`
	Parent   *struct {
		Identifier string `json:"identifier"`
	} `json:"parent"`
	Project *struct {
		Name string `json:"name"`
	} `json:"project"`
	Comments struct {
		Nodes []comment `json:"nodes"`
	} `json:"comments"`
}

const issueFields = `id identifier title description url priority dueDate estimate updatedAt archivedAt trashed
state { id name type position } labels(first: 50) { nodes { id name } } assignee { name displayName email } creator { name displayName }
parent { identifier } project { name } comments(last: 10) { nodes { body createdAt user { name displayName } } }`

type gqlError struct {
	Message    string `json:"message"`
	Extensions struct {
		Code     string `json:"code"`
		UserMsg  string `json:"userPresentableMessage"`
		HTTPCode int    `json:"statusCode"`
	} `json:"extensions"`
}

// do runs one query or mutation and decodes its data into out.
func (c *client) do(query string, vars map[string]any, out any) error {
	if c.key == "" {
		return &worksync.AuthError{Msg: "add a Linear API key in the plugin's settings"}
	}
	raw, _ := json.Marshal(map[string]any{"query": query, "variables": vars})
	r, err := c.fetch(plugin.FetchRequest{Method: "POST", URL: api, Body: raw, Headers: map[string]string{
		"Content-Type":  "application/json",
		"Authorization": c.key,
		"User-Agent":    "Enplace Linear plugin",
	}})
	if err != nil {
		return errors.New("couldn't reach Linear")
	}
	var body struct {
		Data   json.RawMessage `json:"data"`
		Errors []gqlError      `json:"errors"`
	}
	_ = json.Unmarshal(r.Body, &body)
	for _, e := range body.Errors {
		code := strings.ToUpper(e.Extensions.Code)
		msg := strings.ToLower(e.Message)
		switch {
		case code == "AUTHENTICATION_ERROR" || strings.Contains(msg, "authentication"):
			return &worksync.AuthError{Msg: "Linear turned the API key down. Check it in the plugin's settings"}
		case code == "FORBIDDEN" || strings.Contains(msg, "forbidden"):
			return &worksync.AuthError{Msg: "the Linear API key can't do that here: check it can see the team"}
		case code == "RATELIMITED":
			return errors.New("Linear is rate limiting the key; it'll try again soon")
		}
	}
	switch {
	case r.Status == 401:
		return &worksync.AuthError{Msg: "Linear turned the API key down. Check it in the plugin's settings"}
	case r.Status == 403:
		return &worksync.AuthError{Msg: "the Linear API key can't do that here: check it can see the team"}
	case r.Status == 429:
		return errors.New("Linear is rate limiting the key; it'll try again soon")
	}
	if len(body.Errors) > 0 {
		e := body.Errors[0]
		msg := e.Extensions.UserMsg
		if msg == "" {
			msg = e.Message
		}
		return fmt.Errorf("Linear said: %s", msg)
	}
	if r.Status < 200 || r.Status >= 300 {
		return fmt.Errorf("Linear answered %d", r.Status)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body.Data, out); err != nil {
		return errors.New("Linear sent something Enplace can't read")
	}
	return nil
}

// teams are the teams the key can see, and who it belongs to.
func (c *client) teams() (string, []team, error) {
	var d struct {
		Viewer person `json:"viewer"`
		Teams  struct {
			Nodes []team `json:"nodes"`
		} `json:"teams"`
	}
	err := c.do(`query Teams { viewer { name displayName } teams(first: 100) { nodes { id key name } } }`, nil, &d)
	return d.Viewer.String(), d.Teams.Nodes, err
}

// team reads one team's statuses, labels and projects.
func (c *client) team(id string) (*teamInfo, error) {
	var d struct {
		Team *struct {
			team
			States struct {
				Nodes []state `json:"nodes"`
			} `json:"states"`
			Labels struct {
				Nodes []label `json:"nodes"`
			} `json:"labels"`
			Projects struct {
				Nodes []lproject `json:"nodes"`
			} `json:"projects"`
		} `json:"team"`
		Shared struct {
			Nodes []label `json:"nodes"`
		} `json:"issueLabels"`
	}
	err := c.do(`query Team($id: String!) {
  team(id: $id) { id key name
    states(first: 100) { nodes { id name type position } }
    labels(first: 250) { nodes { id name } }
    projects(first: 100) { nodes { id name state } } }
  issueLabels(first: 250, filter: { team: { null: true } }) { nodes { id name } }
}`, map[string]any{"id": id}, &d)
	if err != nil {
		return nil, err
	}
	if d.Team == nil {
		return nil, &worksync.AuthError{Msg: "can't find the Linear team, or the key can't see it"}
	}
	t := &teamInfo{team: d.Team.team, States: d.Team.States.Nodes, Labels: d.Team.Labels.Nodes, Projects: d.Team.Projects.Nodes}
	for _, l := range d.Shared.Nodes {
		if !hasLabel(t.Labels, l.Name) {
			t.Labels = append(t.Labels, l)
		}
	}
	return t, nil
}

func hasLabel(ls []label, name string) bool {
	for _, l := range ls {
		if strings.EqualFold(l.Name, name) {
			return true
		}
	}
	return false
}

// issues reads every page of issues matching filter, up to max pages.
func (c *client) issues(filter map[string]any, max int) ([]issue, error) {
	var out []issue
	after := any(nil)
	for page := 0; page < max; page++ {
		var d struct {
			Issues struct {
				Nodes    []issue `json:"nodes"`
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
			} `json:"issues"`
		}
		err := c.do(`query Issues($filter: IssueFilter, $after: String) {
  issues(filter: $filter, first: 100, after: $after, includeArchived: true, orderBy: updatedAt) {
    nodes { `+issueFields+` }
    pageInfo { hasNextPage endCursor }
  }
}`, map[string]any{"filter": filter, "after": after}, &d)
		if err != nil {
			return nil, err
		}
		out = append(out, d.Issues.Nodes...)
		if !d.Issues.PageInfo.HasNextPage || d.Issues.PageInfo.EndCursor == "" {
			break
		}
		after = d.Issues.PageInfo.EndCursor
	}
	return out, nil
}

// update changes one issue and returns it as it is now.
func (c *client) update(id string, input map[string]any) (*issue, error) {
	var d struct {
		R struct {
			Success bool   `json:"success"`
			Issue   *issue `json:"issue"`
		} `json:"issueUpdate"`
	}
	err := c.do(`mutation Update($id: String!, $input: IssueUpdateInput!) {
  issueUpdate(id: $id, input: $input) { success issue { `+issueFields+` } }
}`, map[string]any{"id": id, "input": input}, &d)
	if err != nil {
		return nil, err
	}
	if !d.R.Success || d.R.Issue == nil {
		return nil, errors.New("Linear didn't take the change")
	}
	return d.R.Issue, nil
}

// user finds a workspace member by email, else by name; "" when there's
// no one (or more than one) like that.
func (c *client) user(name, email string) (string, error) {
	var filter map[string]any
	switch {
	case email != "":
		filter = map[string]any{"email": map[string]any{"eqIgnoreCase": email}}
	case name != "":
		filter = map[string]any{"or": []any{
			map[string]any{"name": map[string]any{"eqIgnoreCase": name}},
			map[string]any{"displayName": map[string]any{"eqIgnoreCase": name}},
		}}
	default:
		return "", nil
	}
	var d struct {
		Users struct {
			Nodes []struct {
				ID string `json:"id"`
			} `json:"nodes"`
		} `json:"users"`
	}
	if err := c.do(`query Users($filter: UserFilter) { users(filter: $filter, first: 2) { nodes { id } } }`, map[string]any{"filter": filter}, &d); err != nil {
		return "", err
	}
	if len(d.Users.Nodes) != 1 {
		return "", nil
	}
	return d.Users.Nodes[0].ID, nil
}

// create opens an issue.
func (c *client) create(input map[string]any) (*issue, error) {
	var d struct {
		R struct {
			Success bool   `json:"success"`
			Issue   *issue `json:"issue"`
		} `json:"issueCreate"`
	}
	err := c.do(`mutation Create($input: IssueCreateInput!) {
  issueCreate(input: $input) { success issue { `+issueFields+` } }
}`, map[string]any{"input": input}, &d)
	if err != nil {
		return nil, err
	}
	if !d.R.Success || d.R.Issue == nil {
		return nil, errors.New("Linear didn't open the issue")
	}
	return d.R.Issue, nil
}

// createLabel adds a label to the team, for a tag Linear doesn't have yet.
func (c *client) createLabel(teamID, name string) (*label, error) {
	var d struct {
		R struct {
			Success bool   `json:"success"`
			Label   *label `json:"issueLabel"`
		} `json:"issueLabelCreate"`
	}
	err := c.do(`mutation Label($input: IssueLabelCreateInput!) {
  issueLabelCreate(input: $input) { success issueLabel { id name } }
}`, map[string]any{"input": map[string]any{"name": name, "teamId": teamID}}, &d)
	if err != nil {
		return nil, err
	}
	if !d.R.Success || d.R.Label == nil {
		return nil, errors.New("Linear didn't add the label " + name)
	}
	return d.R.Label, nil
}

// keyHash tells keys apart without keeping one.
func keyHash(k string) string {
	h := sha256.Sum256([]byte(k))
	return hex.EncodeToString(h[:6])
}
