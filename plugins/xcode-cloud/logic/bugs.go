package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/plugins/xcode-cloud/cloud"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

// Bug is a failing build Enplace filed work for: one per workflow and
// branch, so a build that keeps failing adds notes rather than items.
type Bug struct {
	Workflow string    `json:"workflow"`
	Branch   string    `json:"branch,omitempty"`
	Title    string    `json:"title"`
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

func bugKey(x cloud.Run) string { return x.Workflow + "|" + x.Branch }

// bugTags mark the work Xcode Cloud files. Not "build": that's App's
// tag for a build an agent on the Mac makes.
var bugTags = []string{"bug", "ci"}

// watch looks at the runs just read: a build that newly failed gets a bug
// for agents with its output, or a note on the bug already open for its
// workflow and branch; a later pass is noted there too. Failures from
// before the plugin first looked are never filed.
func (r *run) watch(c *cloud.Client, s *Status, was *Status) {
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
			before[x.ID] = x.Status
		}
	}
	seen := map[string]bool{}
	for _, x := range s.Runs { // newest first: only the newest finished run of each counts
		k := bugKey(x)
		if seen[k] || (x.Status != "failed" && x.Status != "passed") {
			continue
		}
		seen[k] = true
		was, known := before[x.ID]
		switch {
		case x.Status == "failed" && was != "failed" && (known || x.Created.After(s.Since)):
			r.fileBug(c, s, x)
		case x.Status == "passed":
			if b := s.Bugs[k]; b != nil && b.WorkID != "" && x.Number > b.Run && x.Number > b.Passed {
				b.Passed = x.Number
				r.note(b, fmt.Sprintf("Xcode Cloud build %d of %s on %s passed.", x.Number, x.Workflow, branchName(x.Branch)))
			}
		}
	}
}

// follow finds the work each bug became and, once one is done, runs its
// workflow again on its branch to check the fix. A new failure then files
// a new bug with the new output.
func (r *run) follow(c *cloud.Client, s *Status) {
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
			a := r.start(c, s, Request{Workflow: b.Workflow, Branch: b.Branch})
			a.By = selfID
			s.Requests["fixed-"+w.Key] = a
			if a.Error == "" {
				r.res.Log = fmt.Sprintf("Started Xcode Cloud build %d to check %s's fix", a.Run, w.Key)
			}
		}
	}
}

// bugWork is the work item a bug became: by its id once known, else the
// newest open item Xcode Cloud filed with its title.
func (r *run) bugWork(b *Bug) *plugin.Work {
	var found *plugin.Work
	for i, w := range r.req.Work {
		if w.ProjectID != r.pid {
			continue
		}
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

// openBug is an open item Xcode Cloud filed with this title, for when its
// state was lost.
func (r *run) openBug(title string) *plugin.Work {
	for i, w := range r.req.Work {
		if w.ProjectID == r.pid && w.Title == title && hasTag(w, "ci") && w.StageKind != "done" && w.StageKind != "cancelled" {
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

// fileBug files the failed run x for agents, or tells its open bug.
func (r *run) fileBug(c *cloud.Client, s *Status, x cloud.Run) {
	k := bugKey(x)
	b := s.Bugs[k]
	if b != nil && x.Number <= b.Run {
		return
	}
	out, err := c.Output(x.ID)
	if b != nil {
		b.Run = x.Number
		r.note(b, fmt.Sprintf("Xcode Cloud build %d failed again.\n\n", x.Number)+report(x, out, err))
		return
	}
	title := fmt.Sprintf("Fix the failing Xcode Cloud build: %s on %s", x.Workflow, branchName(x.Branch))
	b = &Bug{Workflow: x.Workflow, Branch: x.Branch, Title: title, Run: x.Number, At: r.req.Now}
	s.Bugs[k] = b
	if w := r.openBug(title); w != nil {
		b.WorkID, b.Key = w.ID, w.Key
		r.note(b, fmt.Sprintf("Xcode Cloud build %d failed again.\n\n", x.Number)+report(x, out, err))
		return
	}
	if r.waitingFor(title) {
		return
	}
	r.res.Ops = append(r.res.Ops, plugin.Op{Kind: "work.create", ProjectID: r.pid, Params: map[string]any{
		"title":    title,
		"body":     bugBody(x, out, err),
		"tags":     bugTags,
		"priority": 3,
		"doer":     "agent",
	}})
	r.res.Log = fmt.Sprintf("Xcode Cloud build %d failed: filed a bug for agents", x.Number)
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

func bugBody(x cloud.Run, out cloud.Output, err error) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Xcode Cloud build **%d** of the **%s** workflow on **%s** failed. Enplace filed this so an agent can fix it.\n\n", x.Number, x.Workflow, branchName(x.Branch))
	sb.WriteString(report(x, out, err))
	sb.WriteString("\n## What to do\n")
	sb.WriteString("1. Find the cause from the output above. Build the same scheme locally with xcodebuild if you can, to see it fail and then pass.\n")
	sb.WriteString("2. Fix it on a branch and open a pull request with this item's key in its title.\n")
	fmt.Fprintf(&sb, "3. Once this item is done, Enplace runs **%s** on **%s** again to check. If it still fails, Enplace files a new bug with the new output.\n\n", x.Workflow, branchName(x.Branch))
	sb.WriteString("The app's latest runs are in the get_ci_runs tool.\n")
	return clip(sb.String(), maxBody)
}

// report is a run's commit, steps and what Xcode Cloud reported.
func report(x cloud.Run, out cloud.Output, err error) string {
	var sb strings.Builder
	if x.Commit != "" {
		commit := x.Commit
		if len(commit) > 7 {
			commit = commit[:7]
		}
		fmt.Fprintf(&sb, "- Commit: `%s` %s\n", commit, x.Message)
	}
	if !x.Finished.IsZero() {
		fmt.Fprintf(&sb, "- Finished: %s\n", x.Finished.UTC().Format("2 Jan 2006 15:04 UTC"))
	}
	if len(out.Steps) > 0 {
		steps := make([]string, 0, len(out.Steps))
		for _, st := range out.Steps {
			steps = append(steps, st.Name+": "+st.Result)
		}
		fmt.Fprintf(&sb, "- Steps: %s\n", strings.Join(steps, ", "))
	}
	sb.WriteString("\n## Output\n")
	switch {
	case err != nil:
		fmt.Fprintf(&sb, "Enplace couldn't read the build's output from App Store Connect (%s). Open build %d in App Store Connect › Xcode Cloud › Builds to see its logs.\n", err, x.Number)
	case len(out.Problems) == 0:
		fmt.Fprintf(&sb, "Xcode Cloud reported no errors or failed tests, so it probably stopped in a script, signing or upload step. Open build %d in App Store Connect › Xcode Cloud › Builds to see its logs.\n", x.Number)
	default:
		sb.WriteString("```text\n")
		for _, p := range out.Problems {
			line := p.Kind + ":"
			if p.Test != "" {
				line += " " + p.Test
			}
			if w := p.Where(); w != "" {
				line += " " + w + ":"
			}
			sb.WriteString(strings.ReplaceAll(line+" "+p.Message, "```", "'''") + "\n")
		}
		sb.WriteString("```\n")
	}
	return clip(sb.String(), maxBody)
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
