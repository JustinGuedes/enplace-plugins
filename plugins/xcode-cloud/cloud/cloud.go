// Package cloud reads and starts an app's Xcode Cloud builds through the
// App Store Connect API, with the App Store plugin's client and key. It's
// free of Enplace's plugin calls, so it can be tested plainly.
package cloud

import (
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/plugins/app/store"
)

// Client is App Store Connect, signed in with the shared key.
type Client struct{ *store.Client }

// New signs in with the App Store Connect key.
func New(call store.Call, issuer, keyID, pemKey string, now time.Time) (*Client, error) {
	c, err := store.NewClient(call, issuer, keyID, pemKey, now)
	if err != nil {
		return nil, err
	}
	return &Client{c}, nil
}

// ErrNotSetUp is an app Xcode Cloud hasn't been switched on for.
var ErrNotSetUp = errors.New("Xcode Cloud isn't set up for this app yet")

// Product is the app's Xcode Cloud product; empty ID when there's none.
func (c *Client) Product(appID string) (string, error) {
	var res struct {
		Data *store.Ref `json:"data"`
	}
	err := c.Do("GET", "/v1/apps/"+url.PathEscape(appID)+"/ciProduct", nil, &res)
	var e *store.Error
	if errors.As(err, &e) && e.Status == 404 || err == nil && res.Data == nil {
		return "", ErrNotSetUp
	}
	if err != nil {
		return "", err
	}
	return res.Data.ID, nil
}

// Workflow is one Xcode Cloud workflow.
type Workflow struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// Workflows lists the product's workflows by name.
func (c *Client) Workflows(productID string) ([]Workflow, error) {
	var res struct {
		Data []struct {
			ID         string `json:"id"`
			Attributes struct {
				Name    string `json:"name"`
				Enabled bool   `json:"isEnabled"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := c.Do("GET", "/v1/ciProducts/"+url.PathEscape(productID)+"/workflows?limit=50", nil, &res); err != nil {
		return nil, err
	}
	out := []Workflow{}
	for _, d := range res.Data {
		out = append(out, Workflow{d.ID, d.Attributes.Name, d.Attributes.Enabled})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Pick is the workflow named (by name or ID), or the first that's on.
func Pick(ws []Workflow, want string) (Workflow, bool) {
	want = strings.TrimSpace(want)
	for _, w := range ws {
		if want != "" && (w.ID == want || strings.EqualFold(w.Name, want)) {
			return w, true
		}
	}
	if want == "" {
		for _, w := range ws {
			if w.Enabled {
				return w, true
			}
		}
	}
	return Workflow{}, false
}

// Run is one build run, in the words of the CI contract (public.ci).
type Run struct {
	ID       string    `json:"id"`
	Number   int       `json:"number"`
	Workflow string    `json:"workflow"`
	Branch   string    `json:"branch,omitempty"`
	Commit   string    `json:"commit,omitempty"`
	Message  string    `json:"message,omitempty"`
	Status   string    `json:"status"` // queued, running, passed, failed or cancelled
	Created  time.Time `json:"created"`
	// Started is when it left the queue, so wait and run time can be told
	// apart. Zero while it's queued.
	Started  time.Time `json:"started,omitzero"`
	Finished time.Time `json:"finished,omitzero"`
	// Trigger is what started it: push, pull_request, manual, schedule or
	// rebuild.
	Trigger string `json:"trigger,omitempty"`
	// Artifact is what a passed run made for others to use, like
	// "app_store"; empty for a run that only checked the code, or when
	// Xcode Cloud doesn't say.
	Artifact string `json:"artifact,omitempty"`
	// Track is the product's track the run belongs to, when Enplace can
	// tell (by the workflow each track names).
	Track string `json:"track,omitempty"`
}

// trigger says what started a run, in the CI contract's words.
func trigger(reason string, pr bool) string {
	switch reason {
	case "GIT_REF_CHANGE":
		if pr {
			return "pull_request"
		}
		return "push"
	case "PULL_REQUEST_OPEN", "PULL_REQUEST_UPDATE":
		return "pull_request"
	case "MANUAL":
		return "manual"
	case "MANUAL_REBUILD":
		return "rebuild"
	case "SCHEDULE":
		return "schedule"
	}
	return ""
}

func status(progress, result string) string {
	switch progress {
	case "PENDING":
		return "queued"
	case "RUNNING":
		return "running"
	}
	switch result {
	case "SUCCEEDED":
		return "passed"
	case "CANCELED", "SKIPPED":
		return "cancelled"
	}
	return "failed"
}

// Runs lists the product's latest runs, newest first.
func (c *Client) Runs(productID string, limit int) ([]Run, error) {
	var res struct {
		Data []struct {
			ID         string `json:"id"`
			Attributes struct {
				Number   int       `json:"number"`
				Created  time.Time `json:"createdDate"`
				Started  time.Time `json:"startedDate"`
				Finished time.Time `json:"finishedDate"`
				Reason   string    `json:"startReason"`
				PR       bool      `json:"isPullRequestBuild"`
				Progress string    `json:"executionProgress"`
				Result   string    `json:"completionStatus"`
				Commit   *struct {
					SHA     string `json:"commitSha"`
					Message string `json:"message"`
				} `json:"sourceCommit"`
			} `json:"attributes"`
			Relationships struct {
				Workflow struct {
					Data *store.Ref `json:"data"`
				} `json:"workflow"`
				Source struct {
					Data *store.Ref `json:"data"`
				} `json:"sourceBranchOrTag"`
			} `json:"relationships"`
		} `json:"data"`
		Included []struct {
			Type       string `json:"type"`
			ID         string `json:"id"`
			Attributes struct {
				Name string `json:"name"`
			} `json:"attributes"`
		} `json:"included"`
	}
	q := url.Values{"limit": {strconv.Itoa(max(limit, 1))}, "sort": {"-number"}, "include": {"workflow,sourceBranchOrTag"}}
	if err := c.Do("GET", "/v1/ciProducts/"+url.PathEscape(productID)+"/buildRuns?"+q.Encode(), nil, &res); err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, x := range res.Included {
		names[x.Type+"/"+x.ID] = x.Attributes.Name
	}
	name := func(r *store.Ref) string {
		if r == nil {
			return ""
		}
		return names[r.Type+"/"+r.ID]
	}
	out := []Run{}
	for _, d := range res.Data {
		a := d.Attributes
		run := Run{ID: d.ID, Number: a.Number, Workflow: name(d.Relationships.Workflow.Data), Branch: name(d.Relationships.Source.Data),
			Status: status(a.Progress, a.Result), Created: a.Created, Started: a.Started, Finished: a.Finished, Trigger: trigger(a.Reason, a.PR)}
		if a.Commit != nil {
			run.Commit, run.Message = a.Commit.SHA, firstLine(a.Commit.Message)
		}
		out = append(out, run)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Number > out[j].Number })
	return out, nil
}

// Start starts a run of the workflow, on a branch when one is named (else
// the workflow's own start condition decides), and returns its number.
func (c *Client) Start(workflowID, branch string) (int, error) {
	rel := map[string]any{"workflow": map[string]any{"data": store.Ref{Type: "ciWorkflows", ID: workflowID}}}
	if branch = strings.TrimSpace(branch); branch != "" {
		ref, err := c.branch(workflowID, branch)
		if err != nil {
			return 0, err
		}
		rel["sourceBranchOrTag"] = map[string]any{"data": store.Ref{Type: "scmGitReferences", ID: ref}}
	}
	var out struct {
		Data struct {
			Attributes struct {
				Number int `json:"number"`
			} `json:"attributes"`
		} `json:"data"`
	}
	err := c.Do("POST", "/v1/ciBuildRuns", map[string]any{"data": map[string]any{"type": "ciBuildRuns", "relationships": rel}}, &out)
	return out.Data.Attributes.Number, err
}

// branch finds a branch of the workflow's repo.
func (c *Client) branch(workflowID, name string) (string, error) {
	var repo struct {
		Data *store.Ref `json:"data"`
	}
	if err := c.Do("GET", "/v1/ciWorkflows/"+url.PathEscape(workflowID)+"/repository", nil, &repo); err != nil {
		return "", err
	}
	if repo.Data == nil {
		return "", errors.New("the workflow has no repository")
	}
	var refs struct {
		Data []struct {
			ID         string `json:"id"`
			Attributes struct {
				Name    string `json:"name"`
				Kind    string `json:"kind"`
				Deleted bool   `json:"isDeleted"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := c.Do("GET", "/v1/scmRepositories/"+url.PathEscape(repo.Data.ID)+"/gitReferences?limit=200", nil, &refs); err != nil {
		return "", err
	}
	for _, r := range refs.Data {
		if r.Attributes.Name == name && r.Attributes.Kind == "BRANCH" && !r.Attributes.Deleted {
			return r.ID, nil
		}
	}
	return "", errors.New("Xcode Cloud can't see a branch called " + name)
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}
