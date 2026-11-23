package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

// step is one thing to set up, in words for someone doing it the first time.
type step struct {
	Title, How string
	Done       bool
	Button     *plugin.Button
}

// tokenPage is where GitHub's fine-grained tokens are edited.
const tokenPage = "https://github.com/settings/personal-access-tokens"

func (r *run) steps(s *Status) []step {
	repo, _ := r.code()
	read := s != nil && len(s.Workflows) > 0 && s.Error == ""
	return []step{
		{Title: "Link the repo in the GitHub plugin", How: "GitHub Actions uses the GitHub plugin's repo and token, so there's nothing to type here.", Done: repo != ""},
		{Title: "Let the token use Actions", How: "Edit the token the GitHub plugin uses and set Actions to Read and write: read to follow builds, write to start and re-run them. Then press Check now.", Done: read,
			Button: &plugin.Button{Label: "Open GitHub tokens", URL: tokenPage}},
	}
}

func left(steps []step) int {
	n := 0
	for _, st := range steps {
		if !st.Done {
			n++
		}
	}
	return n
}

var words4 = map[string]string{"passed": "Passed", "failed": "Failed", "cancelled": "Cancelled", "running": "Running", "queued": "Waiting"}

func ago(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case t.IsZero():
		return ""
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	}
	return fmt.Sprintf("%d days ago", int(d.Hours()/24))
}

func took(a, b time.Time) string {
	if a.IsZero() || b.IsZero() || b.Before(a) {
		return ""
	}
	d := b.Sub(a)
	if d < time.Minute {
		return fmt.Sprintf("%d s", int(d.Seconds()))
	}
	return fmt.Sprintf("%d min", int(d.Minutes()))
}

func join(parts ...string) string {
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " · ")
}

// fixing is the bug an agent fixes a failed job in, as "an agent fixes it
// in ELD-31".
func fixing(s *Status, x Run, j Job) string {
	if b := s.Bugs[bugKey(x, j)]; j.Status == "failed" && b != nil && b.Key != "" {
		return "an agent fixes it in " + b.Key
	}
	return ""
}

func (r *run) runName(x Run) string { return fmt.Sprintf("%s #%d", x.Workflow, x.Number) }

// overview is the card: the project's runs, or a product's own job.
func (r *run) overview() []plugin.Block {
	s := r.status()
	if sc := r.scope(); sc.Product != "" && s != nil && len(s.Runs) > 0 {
		return r.productCard(s, sc)
	}
	steps := r.steps(s)
	if n := left(steps); n > 0 {
		next := ""
		for _, st := range steps {
			if !st.Done {
				next = st.Title + "."
				break
			}
		}
		blocks := []plugin.Block{{Type: plugin.BlockStat, Value: "Not set up yet", Sub: fmt.Sprintf("%d of %d steps left", n, len(steps))},
			{Type: plugin.BlockText, Text: "Next: " + next, Tone: "muted"}}
		if s != nil && s.Error != "" {
			blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: "GitHub Actions: " + s.Error, Tone: "warn"})
		}
		return append(blocks, plugin.Block{Type: plugin.BlockButtons, Buttons: []plugin.Button{{Label: "Check now", Action: "refresh"}}})
	}
	blocks := []plugin.Block{}
	if s.Error != "" {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: "GitHub Actions: " + s.Error, Tone: "warn"})
	}
	if len(s.Runs) == 0 {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockStat, Value: "No runs yet"})
	} else {
		x := s.Runs[0]
		bug := ""
		for _, j := range x.Jobs {
			if f := fixing(s, x, j); f != "" {
				bug = j.Name + " failed: " + f
				break
			}
		}
		blocks = append(blocks, plugin.Block{Type: plugin.BlockStat, Value: r.runName(x),
			Sub: join(words4[x.Status], x.Branch, x.Message, ago(x.Created, r.req.Now), bug)})
		if len(x.Jobs) > 1 {
			items := []plugin.Item{}
			for _, j := range x.Jobs {
				it := plugin.Item{Title: j.Name, Value: words4[j.Status], Sub: join(took(j.Started, j.Finished), fixing(s, x, j)), Done: j.Status == "passed"}
				items = append(items, it)
			}
			blocks = append(blocks, plugin.Block{Type: plugin.BlockStats, Items: items})
		}
		items := []plugin.Item{}
		for _, y := range s.Runs[1:min(len(s.Runs), 4)] {
			it := plugin.Item{Title: r.runName(y), Sub: join(words4[y.Status], y.Branch, ago(y.Created, r.req.Now)), Done: y.Status == "passed"}
			if y.URL != "" {
				it.Button = &plugin.Button{Label: "Open", URL: y.URL}
			}
			items = append(items, it)
		}
		if len(items) > 0 {
			blocks = append(blocks, plugin.Block{Type: plugin.BlockList, Items: items})
		}
	}
	return append(blocks, plugin.Block{Type: plugin.BlockButtons, Buttons: r.buttons(s, "")})
}

// buttons are Run, Open on GitHub (url, else the latest run's) and Check now.
func (r *run) buttons(s *Status, url string) []plugin.Button {
	var btns []plugin.Button
	if w, branch := r.target(Request{}); w != "" {
		name := w
		if wf, ok := pick(s.Workflows, w); ok {
			name = wf.Name
		}
		btns = append(btns, plugin.Button{Label: "Run " + name, Action: "start", Confirm: fmt.Sprintf("Start %s on %s?", name, branch)})
	}
	if url == "" && len(s.Runs) > 0 {
		url = s.Runs[0].URL
	}
	if url != "" {
		btns = append(btns, plugin.Button{Label: "Open on GitHub", URL: url})
	}
	return append(btns, plugin.Button{Label: "Check now", Action: "refresh"})
}

// productCard shows the job that builds this product.
func (r *run) productCard(s *Status, sc plugin.Project) []plugin.Block {
	job := r.jobFor(sc)
	x, j, ok := latest(s, job)
	if job == "" || !ok {
		what := "this product"
		if sc.ProductName != "" {
			what = sc.ProductName
		}
		return []plugin.Block{
			{Type: plugin.BlockStat, Value: "Not in the workflow yet", Sub: "No job in the latest runs builds " + what + ". Add one to the workflow (a platform in its matrix) and it shows here, or pick the job."},
			{Type: plugin.BlockButtons, Buttons: []plugin.Button{{Label: "Pick the job", Pick: "job"}, {Label: "Check now", Action: "refresh"}}},
		}
	}
	blocks := []plugin.Block{}
	if s.Error != "" {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: "GitHub Actions: " + s.Error, Tone: "warn"})
	}
	blocks = append(blocks, plugin.Block{Type: plugin.BlockStat, Label: j.Name, Value: words4[j.Status],
		Sub: join(r.runName(x), x.Branch, took(j.Started, j.Finished), ago(x.Created, r.req.Now), fixing(s, x, j))})
	if j.Status == "failed" && j.Step != "" {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: "Stopped at: " + j.Step, Tone: "muted"})
	}
	return append(blocks, plugin.Block{Type: plugin.BlockButtons, Buttons: r.buttons(s, j.URL)})
}

func (r *run) panel() []plugin.Block {
	s := r.status()
	steps := r.steps(s)
	blocks := []plugin.Block{}
	if n := left(steps); n == 0 {
		repo, _ := r.code()
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: "Following " + repo + "'s builds.", Tone: "muted"})
	} else {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: fmt.Sprintf("%d of %d steps left. Each one ticks itself off once Enplace sees it done.", n, len(steps))})
		items := []plugin.Item{}
		for i, st := range steps {
			items = append(items, plugin.Item{Title: fmt.Sprintf("%d. %s", i+1, st.Title), Sub: st.How, Done: st.Done, Button: st.Button})
		}
		blocks = append(blocks, plugin.Block{Type: plugin.BlockList, Items: items})
	}
	if s != nil && s.Error != "" {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: "GitHub Actions: " + s.Error, Tone: "warn"})
	}
	if sc := r.scope(); sc.Product != "" {
		line := "Nothing in the latest runs builds this product yet: pick its job, or add it to the workflow."
		if job := r.jobFor(sc); job != "" {
			line = "This product shows the " + job + " job."
		}
		if !r.reads() {
			line += " It reads the project's runs, so it adds no calls to GitHub."
		}
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: line, Tone: "muted"})
	}
	if r.reads() {
		line := "Instant updates are off, so Enplace checks every 30 minutes. Turn them on below and GitHub tells Enplace the moment a build starts or ends."
		if r.hooked() {
			line = "Instant updates are on: GitHub tells Enplace the moment a build starts or ends."
		}
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: line, Tone: "muted"}, r.failBlock(s))
	}
	return append(blocks, plugin.Block{Type: plugin.BlockButtons, Buttons: []plugin.Button{{Label: "Check now", Action: "refresh"}}})
}

// failBlock says what happens when a build fails, and the bugs open now.
func (r *run) failBlock(s *Status) plugin.Block {
	var text string
	switch r.onFail() {
	case failNone:
		return plugin.Block{Type: plugin.BlockText, Text: "Failed builds only show here (When a build fails).", Tone: "muted"}
	case failAsk:
		text = "When a job fails, Enplace asks you in Approvals before filing a bug with its errors."
	default:
		text = "When a job fails, Enplace files a bug on its product with the failing step and errors for an agent to fix, and re-runs the failed jobs once it's done."
	}
	if s != nil {
		var open []string
		for _, b := range s.Bugs {
			if b.Key != "" {
				open = append(open, b.Key)
			}
		}
		sort.Strings(open)
		if len(open) > 0 {
			text += " Open now: " + strings.Join(open, ", ") + "."
		}
	}
	return plugin.Block{Type: plugin.BlockText, Text: text, Tone: "muted"}
}
