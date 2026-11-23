package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/plugins/xcode-cloud/cloud"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

// step is one thing to set up, in words for someone doing it the first time.
type step struct {
	Title, How string
	Done       bool
}

func (r *run) steps(s *Status) []step {
	if s == nil {
		s = &Status{Keys: r.hasKeys(), BundleID: r.bundleID()}
	}
	return []step{
		{"Give the app its bundle ID", "In this product's App Store plugin settings, type the app's bundle ID, like com.guedio.planit.", s.BundleID != ""},
		{"Add your Apple account", "The same Apple account as the App Store and TestFlight plugins, with the App Manager role, added once in Settings → Connections.", s.Keys && s.AppID != ""},
		{"Connect Xcode Cloud once, in Xcode", "Apple only lets Xcode do this. Open the app in Xcode, choose Product → Xcode Cloud → Create Workflow, connect your GitHub repo when it asks, and accept the Default workflow. Then you can close Xcode.", s.ProductID != ""},
		{"Let Enplace make its workflow", "Press Set it up. Enplace makes \"" + cloud.OwnName + "\" from the Default workflow: it archives the app when Enplace asks for a build, as Builds it makes says, and Enplace keeps it that way.", s.Own != ""},
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

var tones = map[string]string{"passed": "Passed", "failed": "Failed", "cancelled": "Cancelled", "running": "Running", "queued": "Waiting for a Mac"}

func (r *run) runSub(x cloud.Run) string {
	sub := tones[x.Status]
	if x.Workflow != "" {
		sub += " · " + x.Workflow
	}
	if x.Branch != "" {
		sub += " on " + x.Branch
	}
	if a := ago(x.Created, r.req.Now); a != "" {
		sub += " · " + a
	}
	return sub
}

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

func (r *run) overview() []plugin.Block {
	s := r.status()
	steps := r.steps(s)
	if s == nil || s.ProductID == "" {
		n := left(steps)
		next := ""
		for _, st := range steps {
			if !st.Done {
				next = st.Title + "."
				break
			}
		}
		return []plugin.Block{
			{Type: plugin.BlockStat, Value: "Not set up yet", Sub: fmt.Sprintf("%d of %d steps left", n, len(steps))},
			{Type: plugin.BlockText, Text: "Next: " + next, Tone: "muted"},
		}
	}
	blocks := []plugin.Block{}
	if s.Error != "" {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: "Xcode Cloud: " + s.Error, Tone: "warn"})
	}
	if len(s.Runs) == 0 {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockStat, Value: "No builds yet"})
	} else {
		x := s.Runs[0]
		sub := r.runSub(x)
		if b := s.Bugs[bugKey(x)]; x.Status == "failed" && b != nil && b.Key != "" {
			sub += " · an agent fixes it in " + b.Key
		}
		blocks = append(blocks, plugin.Block{Type: plugin.BlockStat, Value: fmt.Sprintf("Build %d", x.Number), Sub: sub})
		items := []plugin.Item{}
		for i, x := range s.Runs {
			if i == 0 {
				continue
			}
			if i == 4 {
				break
			}
			items = append(items, plugin.Item{Title: fmt.Sprintf("Build %d", x.Number), Sub: r.runSub(x)})
		}
		if len(items) > 0 {
			blocks = append(blocks, plugin.Block{Type: plugin.BlockList, Items: items})
		}
	}
	w, _ := cloud.Pick(s.Workflows, r.setting("workflow"))
	btns := []plugin.Button{}
	if w.ID != "" {
		btns = append(btns, plugin.Button{Label: "Build", Action: "start", Confirm: "Start the " + w.Name + " workflow in Xcode Cloud?"})
	}
	btns = append(btns, plugin.Button{Label: "Check now", Action: "refresh"})
	return append(blocks, plugin.Block{Type: plugin.BlockButtons, Buttons: btns})
}

func (r *run) panel() []plugin.Block {
	s := r.status()
	steps := r.steps(s)
	blocks := []plugin.Block{}
	if n := left(steps); n == 0 {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: "Xcode Cloud is linked.", Tone: "muted"})
	} else {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: fmt.Sprintf("%d of %d steps left. Each one ticks itself off once Enplace sees it done.", n, len(steps))})
	}
	if left(steps) > 0 {
		// Once every step is done they're only noise above the workflow.
		items := []plugin.Item{}
		for i, st := range steps {
			items = append(items, plugin.Item{Title: fmt.Sprintf("%d. %s", i+1, st.Title), Sub: st.How, Done: st.Done})
		}
		blocks = append(blocks, plugin.Block{Type: plugin.BlockList, Items: items})
	}
	if s != nil && s.Error != "" {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: "Xcode Cloud: " + s.Error, Tone: "warn"})
	}
	if s != nil && s.SetUpError != "" && s.Own == "" {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: "Set it up didn't work: " + s.SetUpError, Tone: "warn"})
	}
	if s != nil && s.Own != "" {
		blocks = append(blocks, r.workflowBlocks(s)...)
	}
	blocks = append(blocks, r.failBlock(s))
	btns := []plugin.Button{}
	switch {
	case s != nil && s.ProductID != "" && s.Own == "":
		btns = append(btns, plugin.Button{Label: "Set it up", Action: "setup"})
	case s != nil && s.Own != "":
		btns = append(btns, plugin.Button{Label: "Update Xcode Cloud", Action: "update"})
	}
	btns = append(btns, plugin.Button{Label: "Check now", Action: "refresh"})
	return append(blocks, plugin.Block{Type: plugin.BlockButtons, Buttons: btns})
}

// workflowBlocks say whether Enplace's workflow matches the settings.
func (r *run) workflowBlocks(s *Status) []plugin.Block {
	line := cloud.OwnName + " matches these settings: it archives " + cloud.AudienceName(r.audience()) + " when Enplace asks for a build."
	if !s.InStep {
		line = "Enplace couldn't check " + cloud.OwnName + " last time. Press Update Xcode Cloud to copy it again."
	}
	out := []plugin.Block{{Type: plugin.BlockText, Text: line}}
	sub := "Changing a setting here updates it in Xcode Cloud straight away. Update Xcode Cloud copies it again from the Default workflow (scheme, project, Xcode and macOS), or makes it again if it was deleted."
	if !s.Kept.IsZero() {
		sub = "Enplace last changed it " + ago(s.Kept, r.req.Now)
		if s.KeptWhat != "" {
			sub += ": " + s.KeptWhat
		}
		sub += ". " + "Update Xcode Cloud copies it again from the Default workflow."
	}
	return append(out, plugin.Block{Type: plugin.BlockText, Text: sub, Tone: "muted"})
}

// failBlock says what happens when a build fails, and the bugs open now.
func (r *run) failBlock(s *Status) plugin.Block {
	var text string
	switch r.onFail() {
	case failNone:
		return plugin.Block{Type: plugin.BlockText, Text: "Failed builds only show here (When a build fails).", Tone: "muted"}
	case failAsk:
		text = "When a build fails, Enplace asks you in Approvals before filing a bug with its errors."
	default:
		text = "When a build fails, Enplace files a bug with its errors and failed tests for an agent to fix, and builds again once it's done."
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
