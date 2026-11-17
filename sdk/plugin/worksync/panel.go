package worksync

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

// SlotSync is the project card that speaks up when the sync needs the
// person (a token refused). Otherwise it rests.
const SlotSync = "project_card:sync"

// SlotTray is the project card, at the top of the project's work, that
// holds new outside items waiting for the person (Config.Hold).
const SlotTray = "project_card:tray"

// trayShown is how many held items the tray lists before "and N more".
const trayShown = 8

func (s *Sync) action(name, workID string, ids []string) error {
	switch name {
	case "sync.bring":
		n, err := s.Bring(ids)
		if err != nil {
			s.Res.Toast = s.Name + " sync stopped: " + s.status.Error
			return nil
		}
		if n > 0 {
			s.Res.Toast = fmt.Sprintf("Added %s", s.count(n))
			s.Res.Log = fmt.Sprintf("Brought in %s from %s", s.count(n), s.Name)
		}
	case "sync.pass":
		if n := s.Skip(ids); n > 0 {
			s.Res.Toast = fmt.Sprintf("Skipped %s", s.count(n))
		}
	case "sync.now":
		was := s.status.Paused
		s.status.Paused = false
		s.Run()
		s.status.Paused = was
		if s.status.Error != "" {
			s.Res.Toast = s.Name + " sync stopped: " + s.status.Error
		} else {
			s.Res.Toast = "Synced with " + s.Name
		}
	case "sync.pause":
		s.status.Paused = true
		s.Res.Toast = s.Name + " sync paused"
		s.Res.Log = "Paused syncing with " + s.Name
	case "sync.resume":
		s.status.Paused = false
		s.Run()
		s.Res.Toast = s.Name + " sync is back on"
		s.Res.Log = "Resumed syncing with " + s.Name
	case "sync.unlink":
		l := s.linkFor(workID)
		if l == nil {
			return nil
		}
		l.State = Skipped
		s.touch(l)
		s.op("work.fields", map[string]any{"id": l.Work, "fields": map[string]string{s.Field(): ""}})
		s.Res.Toast = "Unlinked from " + l.Ref
		s.Res.Log = "Unlinked " + l.Ref + " from " + s.Name
	default:
		if s.cfg.Action != nil {
			return s.cfg.Action(s, name, json.RawMessage(s.Req.Action.Args))
		}
	}
	return nil
}

func (s *Sync) render() {
	for _, slot := range s.Req.Slots {
		switch slot {
		case plugin.SlotWorkPanel:
			s.setCard(slot, s.panel())
		case SlotSync:
			s.setCard(slot, s.card())
			switch {
			case s.status.Auth:
				s.Res.Signal(slot, plugin.LevelLoud, s.Name+" sync needs you")
			case s.status.Error != "":
				s.Res.Signal(slot, plugin.LevelNormal, s.Name+" sync stopped")
			default:
				s.Res.Signal(slot, plugin.LevelQuiet, "")
			}
			s.Res.Say(slot, s.statusLine()+".", s.short())
		case SlotTray:
			s.setCard(slot, s.tray())
			if s.status.Held > 0 {
				s.Res.Signal(slot, plugin.LevelNormal, s.count(s.status.Held)+" waiting")
				s.Res.Say(slot, s.trayLine(s.status.Held, ""), fmt.Sprint(s.status.Held))
			} else {
				s.Res.Signal(slot, plugin.LevelQuiet, "")
			}
		case plugin.SlotBrief:
			s.brief()
		case plugin.SlotWorkFields:
			// The link field has no choices: only the sync sets it.
		default:
			if s.cfg.Render != nil {
				if b := s.cfg.Render(s, slot); b != nil {
					s.setCard(slot, b)
				}
			}
		}
	}
}

func (s *Sync) setCard(slot string, blocks []plugin.Block) {
	if s.Res.Cards == nil {
		s.Res.Cards = map[string][]plugin.Block{}
	}
	s.Res.Cards[slot] = blocks
}

func (s *Sync) short() string {
	switch {
	case s.status.Paused:
		return "Paused"
	case s.status.Error != "":
		return "Stopped"
	}
	return fmt.Sprintf("%d linked", s.status.Linked)
}

func (s *Sync) args(extra map[string]string) json.RawMessage {
	raw, _ := json.Marshal(extra)
	return raw
}

// panel is the block on a linked work item.
func (s *Sync) panel() []plugin.Block {
	var l *Link
	for _, x := range s.links {
		if x.Work == s.Req.WorkID && (x.State == Linked || x.State == Removed) {
			l = x
		}
	}
	if l == nil {
		return nil
	}
	var facts []string
	if l.Assignee != "" {
		facts = append(facts, l.Assignee)
	}
	if l.Info != "" {
		facts = append(facts, l.Info)
	}
	tone := "good"
	switch {
	case l.State == Removed:
		facts = append(facts, "removed in "+s.Name)
		tone = "muted"
	case s.status.Paused:
		facts = append(facts, "sync paused")
		tone = "muted"
	case s.status.Error != "":
		facts = append(facts, "sync stopped")
		tone = "warn"
	default:
		facts = append(facts, "last synced "+ago(s.Req.Now, s.status.LastOK))
		facts = append(facts, strings.ToLower(s.directionShort()))
	}
	strip := plugin.Block{Type: plugin.BlockStrip, Label: l.Ref, Text: "Linked to " + s.Name, Sub: strings.Join(facts, " · "), Value: l.Status, Tone: tone}
	if l.URL != "" {
		strip.Buttons = []plugin.Button{{Label: "Open", URL: l.URL}}
	}
	out := []plugin.Block{strip}
	if s.status.Error != "" {
		out = append(out, plugin.Block{Type: plugin.BlockText, Tone: "warn", Text: s.Name + " sync stopped: " + s.status.Error + ". Check the plugin's settings."})
	}
	if l.State == Removed {
		return out
	}
	work := map[string]string{"work": l.Work}
	buttons := []plugin.Button{{Label: "Sync now", Action: "sync.now", Args: s.args(work)}}
	if s.status.Paused {
		buttons = append(buttons, plugin.Button{Label: "Resume sync", Action: "sync.resume", Args: s.args(work)})
	} else {
		buttons = append(buttons, plugin.Button{Label: "Pause sync", Action: "sync.pause", Args: s.args(work)})
	}
	buttons = append(buttons, plugin.Button{Label: "Unlink", Action: "sync.unlink", Args: s.args(work), Confirm: "Unlink from " + l.Ref + "? Both stay, they just stop syncing."})
	return append(out, plugin.Block{Type: plugin.BlockButtons, Buttons: buttons})
}

func (s *Sync) directionShort() string {
	switch s.direction() {
	case PullOnly:
		return "Brings changes in"
	case PushOnly:
		return "Sends changes out"
	}
	return "Two-way"
}

// card is the project card: a line, and what to do when it's stuck.
func (s *Sync) card() []plugin.Block {
	tone := ""
	if s.status.Error != "" {
		tone = "warn"
	}
	out := []plugin.Block{{Type: plugin.BlockText, Text: s.statusLine() + ".", Tone: tone}}
	if s.status.Linked > 0 {
		out = append(out, plugin.Block{Type: plugin.BlockStat, Label: "Linked", Value: fmt.Sprint(s.status.Linked)})
	}
	b := []plugin.Button{{Label: "Sync now", Action: "sync.now"}}
	if s.status.Paused {
		b = append(b, plugin.Button{Label: "Resume sync", Action: "sync.resume"})
	}
	return append(out, plugin.Block{Type: plugin.BlockButtons, Buttons: b})
}

func ago(now, t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	}
	return fmt.Sprintf("%d days ago", int(d.Hours()/24))
}

func (s *Sync) noun() string {
	if s.cfg.Noun != "" {
		return s.cfg.Noun
	}
	return "item"
}

// count is "1 issue" or "3 issues".
func (s *Sync) count(n int) string {
	if n == 1 {
		return "1 " + s.noun()
	}
	return fmt.Sprintf("%d %ss", n, s.noun())
}

func (s *Sync) trayLine(n int, project string) string {
	verb := "are"
	if n == 1 {
		verb = "is"
	}
	line := fmt.Sprintf("%s from %s %s waiting", s.count(n), s.Name, verb)
	if project != "" {
		line += " on " + project
	}
	return line + "."
}

// tray is the held items, newest first, each with Accept and Skip.
func (s *Sync) tray() []plugin.Block {
	var held []*Link
	for _, l := range s.links {
		if l.State == Held {
			held = append(held, l)
		}
	}
	if len(held) == 0 {
		return nil
	}
	sort.Slice(held, func(i, j int) bool {
		if !held[i].Seen.Equal(held[j].Seen) {
			return held[i].Seen.After(held[j].Seen)
		}
		return held[i].ID < held[j].ID
	})
	out := []plugin.Block{{Type: plugin.BlockText, Text: "New in " + s.Name + ". Accept brings one in as work; Skip hides it until it changes."}}
	for i, l := range held {
		if i == trayShown {
			out = append(out, plugin.Block{Type: plugin.BlockText, Tone: "muted", Text: fmt.Sprintf("And %d more.", len(held)-trayShown)})
			break
		}
		var facts []string
		if l.Author != "" {
			facts = append(facts, l.Author)
		}
		facts = append(facts, ago(s.Req.Now, l.Seen))
		facts = append(facts, l.Labels...)
		one, _ := json.Marshal(map[string][]string{"ids": {l.ID}})
		b := plugin.Block{Type: plugin.BlockSection, Label: l.Ref, Text: l.Title, Sub: strings.Join(facts, " · "),
			Buttons: []plugin.Button{{Label: "Accept", Action: "sync.bring", Args: one}, {Label: "Skip", Action: "sync.pass", Args: one}}}
		if l.URL != "" {
			b.Menu = []plugin.Button{{Label: "Open in " + s.Name, URL: l.URL}}
		}
		out = append(out, b)
	}
	if len(held) > 1 {
		out = append(out, plugin.Block{Type: plugin.BlockButtons, Buttons: []plugin.Button{
			{Label: fmt.Sprintf("Accept all %d", len(held)), Action: "sync.bring"},
			{Label: "Skip all", Action: "sync.pass", Confirm: fmt.Sprintf("Skip all %d? Each comes back if it changes in %s.", len(held), s.Name)},
		}})
	}
	return out
}

// brief is a line on Today when items wait in a tray.
func (s *Sync) brief() {
	type proj struct {
		name, key string
		n         int
	}
	var waiting []proj
	total := 0
	for _, p := range s.Req.Projects {
		if !p.On {
			continue
		}
		var st Status
		if s.Req.State.GetProject(p.ID, keyStatus, &st) && st.Held > 0 && !st.Paused {
			waiting = append(waiting, proj{p.Name, p.Key, st.Held})
			total += st.Held
		}
	}
	if total == 0 {
		return
	}
	sort.Slice(waiting, func(i, j int) bool { return waiting[i].n > waiting[j].n })
	b := &plugin.Brief{}
	if len(waiting) == 1 {
		b.Text = s.trayLine(total, waiting[0].name)
	} else {
		b.Text = s.trayLine(total, fmt.Sprintf("%s and %d other projects", waiting[0].name, len(waiting)-1))
	}
	if k := strings.ToLower(waiting[0].key); k != "" {
		b.Button = &plugin.Button{Label: "Look", Href: "/p/" + k + "/work"}
	}
	s.Res.Brief = b
}
