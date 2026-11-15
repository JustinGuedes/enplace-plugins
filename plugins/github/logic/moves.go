package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

const (
	keyMoves = "moves"
	// catchUp is how far back a pull request's last change may be, the
	// first time work is followed, and still move its item. Older ones
	// are taken as already dealt with.
	catchUp = 72 * time.Hour
)

// Moves remembers which pull request states have moved work, so each one
// moves its item once and a person moving it by hand afterwards wins.
type Moves struct {
	Since time.Time `json:"since"`
	// Seen is the state each pull request was last acted on in, by work
	// key and then pull request link.
	Seen map[string]map[string]string `json:"seen"`
}

// follow is whether this project's work moves with its pull requests.
func (r *run) follow() bool {
	v, ok := r.req.ProjectSettings[r.pid]["move_work"].(bool)
	return !ok || v
}

// moveWork moves each work item along with the pull requests that mention
// it: opened to review, merged to done, closed without merging back to
// doing. ls are the links just found.
func (r *run) moveWork(ls Links) {
	if !r.follow() || len(r.req.Work) == 0 {
		return
	}
	var m Moves
	first := r.fresh || !r.req.State.GetProject(r.pid, keyMoves, &m) || m.Since.IsZero()
	if first {
		m = Moves{Since: r.req.Now}
	}
	if m.Seen == nil {
		m.Seen = map[string]map[string]string{}
	}
	byKey := map[string]plugin.Work{}
	for _, w := range r.req.Work {
		if w.ProjectID == r.pid {
			byKey[strings.ToUpper(w.Key)] = w
		}
	}
	for key, links := range ls {
		w, ok := byKey[key]
		if !ok {
			continue
		}
		seen := m.Seen[key]
		if seen == nil {
			seen = map[string]string{}
		}
		var fresh []Link
		for _, l := range links {
			if l.Kind != "pr" || seen[l.URL] == l.State {
				continue
			}
			seen[l.URL] = l.State
			if first && r.req.Now.Sub(l.At) > catchUp {
				continue
			}
			fresh = append(fresh, l)
		}
		m.Seen[key] = seen
		if op := moveFor(w, links, fresh); op != nil {
			r.res.Ops = append(r.res.Ops, *op)
		}
	}
	// Forget items that no longer have links.
	for k := range m.Seen {
		if _, ok := ls[k]; !ok {
			delete(m.Seen, k)
		}
	}
	r.res.SetProject(r.pid, keyMoves, m)
}

// moveFor is the one move an item needs for what just happened to its pull
// requests (newest first), or nil. all are every pull request linked to it.
func moveFor(w plugin.Work, all, fresh []Link) *plugin.Op {
	if len(fresh) == 0 {
		return nil
	}
	// Another pull request still open holds the item where it is.
	openElsewhere := func(l Link) bool {
		for _, x := range all {
			if x.Kind == "pr" && x.URL != l.URL && (x.State == "open" || x.State == "draft") {
				return true
			}
		}
		return false
	}
	move := func(stage, reason, note string) *plugin.Op {
		params := map[string]any{"id": w.ID, "stage": stage, "reason": reason}
		if note != "" {
			params["note"] = note
		}
		return &plugin.Op{Kind: "work.move", ProjectID: w.ProjectID, Params: params}
	}
	for _, l := range fresh {
		pr := fmt.Sprintf("#%d", l.Number)
		switch l.State {
		case "merged":
			if w.StageKind != "done" && !openElsewhere(l) {
				return move("done", pr+" merged", "")
			}
		case "open":
			if w.StageKind == "todo" || w.StageKind == "doing" {
				return move("review", pr+" opened", "")
			}
		case "closed":
			if w.StageKind == "review" && !openElsewhere(l) {
				return move("doing", pr+" closed without merging",
					fmt.Sprintf("Pull request [%s %s](%s) was closed without merging, so this went back to doing.", pr, l.Title, l.URL))
			}
		}
	}
	return nil
}
