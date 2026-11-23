package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

// Bug is a failing job Enplace filed work for: one per workflow, job and
// branch, so a job that keeps failing adds notes rather than items.
type Bug struct {
	Workflow string    `json:"workflow"`
	Job      string    `json:"job"`
	Branch   string    `json:"branch,omitempty"`
	Title    string    `json:"title"`
	RunID    string    `json:"run_id"`           // the run to re-run once it's fixed
	Run      int       `json:"run"`              // the newest failed run the work was told about
	Passed   int       `json:"passed,omitempty"` // a later run that passed, once noted
	WorkID   string    `json:"work_id,omitempty"`
	Key      string    `json:"key,omitempty"`
	At       time.Time `json:"at"`
}

// The "When a build fails" setting's choices.
const (
	failAgents = "File a bug for agents"
	failAsk    = "Ask me first"
	failNone   = "Do nothing"
)

func (r *run) onFail() string {
	if v := r.setting("on_fail"); v != "" {
		return v
	}
	return failAgents
}

func bugKey(x Run, j Job) string { return x.Workflow + "|" + j.Name + "|" + x.Branch }

// bugTags mark the work this plugin files.
var bugTags = []string{"bug", "ci"}

// watch looks at the runs just read: a job that newly failed gets a bug
// for agents, or a note on the bug already open for it; a later pass is
// noted there too. Failures from before the plugin first looked are
// never filed.
func (r *run) watch(c *client, s *Status, was *Status) {
	if s.Since.IsZero() {
		s.Since = r.req.Now
		return
	}
	if s.Bugs == nil {
		s.Bugs = map[string]*Bug{}
	}
	r.follow(c, s)
	if r.onFail() == failNone {
		return
	}
	before := map[string]string{}
	if was != nil {
		for _, x := range was.Runs {
			for _, j := range x.Jobs {
				before[x.ID+"|"+j.Name] = j.Status
			}
		}
	}
	seen := map[string]bool{}
	for _, x := range s.Runs { // newest first: only the newest finished job of each counts
		for _, j := range x.Jobs {
			k := bugKey(x, j)
			if seen[k] || (j.Status != "failed" && j.Status != "passed") {
				continue
			}
			seen[k] = true
			was, known := before[x.ID+"|"+j.Name]
			switch {
			case j.Status == "failed" && was != "failed" && (known || x.Created.After(s.Since)):
				r.fileBug(c, s, x, j)
			case j.Status == "passed":
				if b := s.Bugs[k]; b != nil && b.WorkID != "" && x.Number > b.Run && x.Number > b.Passed {
					b.Passed = x.Number
					r.note(b, fmt.Sprintf("%s passed in %s #%d on %s.", j.Name, x.Workflow, x.Number, branchName(x.Branch)))
				}
			}
		}
	}
}

// follow finds the work each bug became and, once one is done, re-runs
// the run's failed jobs to check the fix. A new failure then files a new
// bug with the new errors.
func (r *run) follow(c *client, s *Status) {
	for k, b := range s.Bugs {
		w := r.bugWork(b)
		if w == nil {
			// Still waiting in Approvals, or refused or deleted: after a day
			// a new failure may file again.
			if b.WorkID != "" || r.req.Now.Sub(b.At) > 24*time.Hour && !r.waitingFor(b.Title) {
				delete(s.Bugs, k)
			}
			continue
		}
		b.WorkID, b.Key = w.ID, w.Key
		switch w.StageKind {
		case "cancelled":
			delete(s.Bugs, k)
		case "done":
			delete(s.Bugs, k)
			a := Answer{State: "started", At: r.req.Now, By: selfID}
			if err := c.rerunFailed(b.RunID); err != nil {
				a.State, a.Error = "failed", err.Error()
			} else {
				r.res.Log = fmt.Sprintf("Re-ran %s #%d to check %s's fix", b.Workflow, b.Run, w.Key)
			}
			s.Requests["fixed-"+w.Key] = a
		}
	}
}

// bugWork is the work item a bug became: by its id once known, else the
// newest item this plugin filed with its title.
func (r *run) bugWork(b *Bug) *plugin.Work {
	var found *plugin.Work
	for i, w := range r.req.Work {
		if b.WorkID != "" {
			if w.ID == b.WorkID {
				return &r.req.Work[i]
			}
			continue
		}
		if w.Title == b.Title && hasTag(w, "ci") && !w.CreatedAt.Before(b.At.Add(-time.Minute)) && (found == nil || w.CreatedAt.After(found.CreatedAt)) {
			found = &r.req.Work[i]
		}
	}
	return found
}

// openBug is an open item with this title, for when the state was lost.
func (r *run) openBug(title string) *plugin.Work {
	for i, w := range r.req.Work {
		if w.Title == title && hasTag(w, "ci") && w.StageKind != "done" && w.StageKind != "cancelled" {
			return &r.req.Work[i]
		}
	}
	return nil
}

// waitingFor says a bug with this title already waits in Approvals.
func (r *run) waitingFor(title string) bool {
	for _, op := range r.req.Waiting {
		if op.Kind != "work.create" {
			continue
		}
		var p struct {
			Title string `json:"title"`
		}
		raw, _ := json.Marshal(op.Params)
		if json.Unmarshal(raw, &p) == nil && p.Title == title {
			return true
		}
	}
	return false
}

// fileBug files job j of run x for agents, or tells its open bug.
func (r *run) fileBug(c *client, s *Status, x Run, j Job) {
	k := bugKey(x, j)
	b := s.Bugs[k]
	if b != nil && x.Number <= b.Run {
		return
	}
	out := output(c, x, j)
	if b != nil {
		b.Run, b.RunID = x.Number, x.ID
		r.note(b, fmt.Sprintf("%s failed again in %s #%d.\n\n", j.Name, x.Workflow, x.Number)+out)
		return
	}
	title := fmt.Sprintf("Fix the failing GitHub Actions build: %s in %s on %s", j.Name, x.Workflow, branchName(x.Branch))
	b = &Bug{Workflow: x.Workflow, Job: j.Name, Branch: x.Branch, Title: title, RunID: x.ID, Run: x.Number, At: r.req.Now}
	s.Bugs[k] = b
	if w := r.openBug(title); w != nil {
		b.WorkID, b.Key = w.ID, w.Key
		r.note(b, fmt.Sprintf("%s failed again in %s #%d.\n\n", j.Name, x.Workflow, x.Number)+out)
		return
	}
	if r.waitingFor(title) {
		return
	}
	p := map[string]any{
		"title":    title,
		"body":     bugBody(x, j, out),
		"tags":     bugTags,
		"priority": 3,
		"doer":     "agent",
	}
	if pr := r.productFor(j.Name); pr != "" {
		p["product"] = pr
	}
	r.res.Ops = append(r.res.Ops, plugin.Op{Kind: "work.create", ProjectID: r.pid, Params: p})
	r.res.Log = fmt.Sprintf("%s failed in %s #%d: filed a bug for agents", j.Name, x.Workflow, x.Number)
}

// note adds a note to the bug's work, handed to the agent working on it.
func (r *run) note(b *Bug, body string) {
	if b.WorkID == "" {
		return
	}
	r.res.Ops = append(r.res.Ops, plugin.Op{Kind: "work.note", ProjectID: r.pid, Params: map[string]any{
		"id": b.WorkID, "body": body, "for_agent": true,
	}})
}

func branchName(b string) string {
	if b == "" {
		return "its branch"
	}
	return b
}

// maxBody keeps a bug well under what a work item holds.
const maxBody = 12000

// logLines is how much of a failed job's log a bug carries.
const logLines = 60

func bugBody(x Run, j Job, out string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "The **%s** job of GitHub Actions run **%s #%d** on **%s** failed. Enplace filed this so an agent can fix it.\n\n", j.Name, x.Workflow, x.Number, branchName(x.Branch))
	sb.WriteString(out)
	sb.WriteString("\n## What to do\n")
	sb.WriteString("1. Find the cause from the errors above, and the full log on GitHub if they aren't enough.\n")
	sb.WriteString("2. Fix it on a branch and open a pull request with this item's key in its title.\n")
	fmt.Fprintf(&sb, "3. Once this item is done, Enplace re-runs the failed jobs of %s #%d to check. If it still fails, Enplace files a new bug with the new errors.\n\n", x.Workflow, x.Number)
	sb.WriteString("The repo's latest runs are in the get_actions_runs tool.\n")
	return clip(sb.String(), maxBody)
}

// output is a failed job's commit, failing step, errors and log tail.
func output(c *client, x Run, j Job) string {
	var sb strings.Builder
	if x.Commit != "" {
		fmt.Fprintf(&sb, "- Commit: `%s` %s\n", short(x.Commit), x.Message)
	}
	if j.Step != "" {
		fmt.Fprintf(&sb, "- Failed at the step: %s\n", j.Step)
	}
	if j.URL != "" {
		fmt.Fprintf(&sb, "- Log: %s\n", j.URL)
	}
	if ps, err := c.problems(j.ID); err == nil && len(ps) > 0 {
		sb.WriteString("\n## Errors\n```text\n")
		for _, p := range ps {
			line := p.Message
			if p.Path != "" {
				line = fmt.Sprintf("%s:%d: %s", p.Path, p.Line, p.Message)
			}
			sb.WriteString(strings.ReplaceAll(line, "```", "'''") + "\n")
		}
		sb.WriteString("```\n")
	}
	if tail := c.logTail(j.ID, logLines); tail != "" {
		fmt.Fprintf(&sb, "\n## The end of the log\n```text\n%s\n```\n", strings.ReplaceAll(tail, "```", "'''"))
	} else {
		sb.WriteString("\nEnplace couldn't read the job's log; open it on GitHub.\n")
	}
	return clip(sb.String(), maxBody)
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n… (cut short)\n"
}

func hasTag(w plugin.Work, tag string) bool {
	for _, t := range w.Tags {
		if strings.EqualFold(t, tag) {
			return true
		}
	}
	return false
}
