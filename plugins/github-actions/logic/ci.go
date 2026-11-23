package main

import (
	"strconv"
	"time"
)

// CIRun is one run as shared in public.ci, the contract every CI plugin
// shares. A workflow run that builds products (an iOS job, an Android job)
// is shared as one run per product, with that job's status, times and
// link, so each product gets its own build history.
type CIRun struct {
	ID       string    `json:"id"`
	Number   int       `json:"number"`
	Workflow string    `json:"workflow"`
	Branch   string    `json:"branch,omitempty"`
	Commit   string    `json:"commit,omitempty"`
	Message  string    `json:"message,omitempty"`
	Status   string    `json:"status"`
	Trigger  string    `json:"trigger,omitempty"`
	Product  string    `json:"product,omitempty"`
	URL      string    `json:"url,omitempty"`
	Created  time.Time `json:"created"`
	Started  time.Time `json:"started,omitzero"`
	Finished time.Time `json:"finished,omitzero"`
}

// triggers are GitHub's events in the contract's words.
var triggers = map[string]string{"push": "push", "pull_request": "pull_request", "workflow_dispatch": "manual", "schedule": "schedule"}

// ciRuns are the runs to share: on the project, one per product job (a run
// with none is shared whole); on a product of its own, only its job.
func (r *run) ciRuns(s *Status) []CIRun {
	out := []CIRun{}
	if s == nil {
		return out
	}
	sc := r.scope()
	own := ""
	if sc.Product != "" {
		own = r.jobFor(sc)
	}
	for _, x := range s.Runs {
		whole := CIRun{ID: x.ID, Number: x.Number, Workflow: x.Workflow, Branch: x.Branch, Commit: x.Commit, Message: x.Message,
			Status: x.Status, Trigger: triggers[x.Event], URL: x.URL, Created: x.Created, Finished: x.Finished}
		split := false
		for _, j := range x.Jobs {
			product := ""
			if sc.Product == "" {
				product = r.productFor(j.Name)
			}
			if product == "" && (own == "" || j.Name != own) {
				continue
			}
			c := whole
			c.ID = x.ID + "-" + strconv.FormatInt(j.ID, 10)
			c.Product, c.Status, c.Started, c.Finished = product, j.Status, j.Started, j.Finished
			if j.URL != "" {
				c.URL = j.URL
			}
			out = append(out, c)
			split = true
		}
		if !split && sc.Product == "" {
			out = append(out, whole)
		}
	}
	return out
}
