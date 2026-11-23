package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

const api = "https://api.github.com"

// client talks to GitHub's REST API for one repo.
type client struct {
	token string
	repo  string // owner/name
}

// apiError is a GitHub answer that isn't a success, said for people.
type apiError struct {
	Status int
	Text   string
}

func (e *apiError) Error() string { return e.Text }

func (c *client) call(method, path string, body any) ([]byte, error) {
	h := map[string]string{
		"Accept":               "application/vnd.github+json",
		"X-GitHub-Api-Version": "2022-11-28",
		"User-Agent":           "Enplace GitHub Actions plugin",
	}
	if c.token != "" {
		h["Authorization"] = "Bearer " + c.token
	}
	req := plugin.FetchRequest{Method: method, URL: api + path, Headers: h}
	if body != nil {
		req.Body, _ = json.Marshal(body)
		h["Content-Type"] = "application/json"
	}
	res, err := plugin.Fetch(req)
	if err != nil {
		return nil, errors.New("couldn't reach GitHub")
	}
	switch s := res.Status; {
	case s >= 200 && s < 300:
		return res.Body, nil
	case s == 401:
		return nil, &apiError{s, "GitHub turned the token down. Check it in Settings → Connections."}
	case s == 403 && method != "GET":
		return nil, &apiError{s, "the token can't start builds here. Give it Actions: read and write."}
	case s == 403 || s == 429:
		return nil, &apiError{s, "GitHub said no: it may be rate limiting, or the token can't read Actions (give it Actions: read)."}
	case s == 404:
		return nil, &apiError{s, "GitHub can't find that, or the token can't see it."}
	case s == 422:
		var m struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(res.Body, &m)
		if m.Message != "" {
			return nil, &apiError{s, "GitHub wouldn't do it: " + m.Message}
		}
	}
	return nil, &apiError{res.Status, fmt.Sprintf("GitHub answered %d.", res.Status)}
}

func (c *client) getJSON(path string, v any) error {
	b, err := c.call("GET", path, nil)
	if err != nil {
		return err
	}
	if json.Unmarshal(b, v) != nil {
		return errors.New("GitHub sent something Enplace can't read")
	}
	return nil
}

func (c *client) base() string { return "/repos/" + c.repo + "/actions" }

// Workflow is one of the repo's workflows.
type Workflow struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Path  string `json:"path"`
	State string `json:"state"` // active, disabled_manually…
}

func (c *client) workflows() ([]Workflow, error) {
	var out struct {
		Workflows []Workflow `json:"workflows"`
	}
	err := c.getJSON(c.base()+"/workflows?per_page=50", &out)
	return out.Workflows, err
}

// Job is one job of a run: with a platform matrix, one per platform.
type Job struct {
	ID       int64     `json:"id"`
	Name     string    `json:"name"`
	Status   string    `json:"status"`
	URL      string    `json:"url,omitempty"`
	Step     string    `json:"step,omitempty"` // the step a failed job stopped at
	Started  time.Time `json:"started,omitzero"`
	Finished time.Time `json:"finished,omitzero"`
}

// Run is one workflow run, in the shape every CI plugin shares (public.ci)
// plus GitHub's own bits.
type Run struct {
	ID         string    `json:"id"`
	Number     int       `json:"number"`
	Workflow   string    `json:"workflow"`
	WorkflowID int64     `json:"workflow_id,omitempty"`
	Branch     string    `json:"branch,omitempty"`
	Commit     string    `json:"commit,omitempty"`
	Message    string    `json:"message,omitempty"`
	Status     string    `json:"status"` // queued, running, passed, failed or cancelled
	Event      string    `json:"event,omitempty"`
	URL        string    `json:"url,omitempty"`
	Created    time.Time `json:"created"`
	Finished   time.Time `json:"finished,omitzero"`
	Jobs       []Job     `json:"jobs,omitempty"`
}

// status says GitHub's status and conclusion in the shared words.
func status(st, conclusion string) string {
	switch st {
	case "in_progress":
		return "running"
	case "completed":
	default:
		return "queued" // queued, waiting, requested, pending
	}
	switch conclusion {
	case "failure", "timed_out", "startup_failure":
		return "failed"
	case "cancelled":
		return "cancelled"
	case "action_required":
		return "queued"
	}
	return "passed" // success, neutral, skipped
}

type ghRun struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Number     int       `json:"run_number"`
	WorkflowID int64     `json:"workflow_id"`
	Branch     string    `json:"head_branch"`
	SHA        string    `json:"head_sha"`
	Title      string    `json:"display_title"`
	Status     string    `json:"status"`
	Conclusion string    `json:"conclusion"`
	Event      string    `json:"event"`
	URL        string    `json:"html_url"`
	Created    time.Time `json:"created_at"`
	Updated    time.Time `json:"updated_at"`
}

// runs are the newest n runs, of one workflow when id isn't 0.
func (c *client) runs(id int64, n int) ([]Run, error) {
	path := c.base() + "/runs"
	if id != 0 {
		path = c.base() + "/workflows/" + strconv.FormatInt(id, 10) + "/runs"
	}
	var out struct {
		Runs []ghRun `json:"workflow_runs"`
	}
	if err := c.getJSON(path+"?per_page="+strconv.Itoa(n), &out); err != nil {
		return nil, err
	}
	runs := make([]Run, 0, len(out.Runs))
	for _, g := range out.Runs {
		x := Run{ID: strconv.FormatInt(g.ID, 10), Number: g.Number, Workflow: g.Name, WorkflowID: g.WorkflowID, Branch: g.Branch,
			Commit: g.SHA, Message: firstLine(g.Title), Status: status(g.Status, g.Conclusion), Event: g.Event, URL: g.URL, Created: g.Created}
		if g.Status == "completed" {
			x.Finished = g.Updated
		}
		runs = append(runs, x)
	}
	return runs, nil
}

// jobs are a run's jobs, in their latest attempt.
func (c *client) jobs(runID string) ([]Job, error) {
	var out struct {
		Jobs []struct {
			ID         int64     `json:"id"`
			Name       string    `json:"name"`
			Status     string    `json:"status"`
			Conclusion string    `json:"conclusion"`
			URL        string    `json:"html_url"`
			Started    time.Time `json:"started_at"`
			Completed  time.Time `json:"completed_at"`
			Steps      []struct {
				Name       string `json:"name"`
				Conclusion string `json:"conclusion"`
			} `json:"steps"`
		} `json:"jobs"`
	}
	if err := c.getJSON(c.base()+"/runs/"+runID+"/jobs?filter=latest&per_page=50", &out); err != nil {
		return nil, err
	}
	jobs := make([]Job, 0, len(out.Jobs))
	for _, g := range out.Jobs {
		j := Job{ID: g.ID, Name: g.Name, Status: status(g.Status, g.Conclusion), URL: g.URL, Started: g.Started}
		if g.Status == "completed" {
			j.Finished = g.Completed
		}
		for _, st := range g.Steps {
			if st.Conclusion == "failure" || st.Conclusion == "timed_out" {
				j.Step = st.Name
				break
			}
		}
		jobs = append(jobs, j)
	}
	return jobs, nil
}

// dispatch starts a workflow (its id or file name) on a branch.
func (c *client) dispatch(workflow, ref string) error {
	_, err := c.call("POST", c.base()+"/workflows/"+url.PathEscape(workflow)+"/dispatches", map[string]string{"ref": ref})
	return err
}

// rerunFailed runs a run's failed jobs again.
func (c *client) rerunFailed(runID string) error {
	_, err := c.call("POST", c.base()+"/runs/"+runID+"/rerun-failed-jobs", nil)
	return err
}

// Problem is one error GitHub noted on a job.
type Problem struct {
	Path    string `json:"path,omitempty"`
	Line    int    `json:"line,omitempty"`
	Message string `json:"message"`
}

// problems are a job's error annotations.
func (c *client) problems(jobID int64) ([]Problem, error) {
	var out []struct {
		Path    string `json:"path"`
		Line    int    `json:"start_line"`
		Level   string `json:"annotation_level"`
		Message string `json:"message"`
	}
	if err := c.getJSON("/repos/"+c.repo+"/check-runs/"+strconv.FormatInt(jobID, 10)+"/annotations?per_page=30", &out); err != nil {
		return nil, err
	}
	var ps []Problem
	for _, a := range out {
		if a.Level != "failure" {
			continue
		}
		p := Problem{Message: a.Message, Line: a.Line}
		if a.Path != ".github" {
			p.Path = a.Path
		}
		ps = append(ps, p)
	}
	return ps, nil
}

// logTail is the end of a job's log, up to n lines. GitHub sends the log
// from its own storage, so a log too big to fetch gives nothing.
func (c *client) logTail(jobID int64, n int) string {
	b, err := c.call("GET", c.base()+"/jobs/"+strconv.FormatInt(jobID, 10)+"/logs", nil)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	for i, l := range lines {
		// Each line starts with a timestamp: "2026-11-23T09:12:01.1234567Z ".
		if len(l) > 29 && l[4] == '-' && l[28] == ' ' {
			lines[i] = l[29:]
		}
	}
	return strings.Join(lines, "\n")
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return strings.TrimSpace(s)
}
