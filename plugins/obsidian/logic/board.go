package main

// A board is an Obsidian Bases kanban view the Enplace plugin for Obsidian
// found in the vault: the property it groups notes by and its lanes, in
// order, with their colours. The project uses one (the person picks it, or
// the one over their folder), so Enplace can show the work the way the
// vault does: each note's lane is kept on its work item ("lane"), the
// "board" work view lays the lanes out, and a lane's name says which stage
// its notes go to unless Statuses to stages says otherwise.

import (
	"cmp"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin/worksync"
)

const (
	keyBoards = "vault.boards" // the boards the app last sent
	keyBoard  = "vault.board"  // the one the person picked, by path
	keyLanes  = "vault.lanes"  // the stage picked for each lane, by lane name
	// laneField is the work field each note's lane is kept in.
	laneField = "lane"
	viewBoard = "board"
	maxBoards = 50
	maxLanes  = 40
)

// board is one board as the app sends it.
type board struct {
	// Path is the .base file, from the vault's top.
	Path string `json:"path"`
	Name string `json:"name"`
	// Property is the note property it groups by, like "status".
	Property string `json:"property"`
	// Folder is the folder its notes come from, when its filter names one.
	Folder string `json:"folder,omitempty"`
	Lanes  []lane `json:"lanes"`
}

type lane struct {
	Name   string `json:"name"`
	Colour string `json:"colour,omitempty"`
}

// takeBoards keeps the boards the app sent, cleaned up.
func takeBoards(res *plugin.Response, pid string, in []board) {
	out := []board{}
	for _, b := range in {
		if len(out) >= maxBoards {
			break
		}
		b.Path, b.Name = clip(strings.TrimSpace(b.Path), 240), clip(strings.TrimSpace(b.Name), 80)
		b.Property = strings.TrimPrefix(strings.ToLower(clip(strings.TrimSpace(b.Property), 60)), "note.")
		if f, ok := cleanFolder(b.Folder); ok {
			b.Folder = f
		} else {
			b.Folder = ""
		}
		var lanes []lane
		for _, l := range b.Lanes {
			l.Name = clip(strings.TrimSpace(l.Name), 60)
			if l.Name == "" || len(lanes) >= maxLanes || slices.ContainsFunc(lanes, func(x lane) bool { return strings.EqualFold(x.Name, l.Name) }) {
				continue
			}
			l.Colour = colourOf(l.Colour)
			lanes = append(lanes, l)
		}
		if b.Path == "" || b.Property == "" || len(lanes) == 0 {
			continue
		}
		b.Lanes = lanes
		b.Name = cmp.Or(b.Name, stem(strings.TrimSuffix(b.Path, ".base")+".md"))
		out = append(out, b)
	}
	res.SetProject(pid, keyBoards, out)
}

// cleanFolder is a folder path from the app, from the vault's top.
func cleanFolder(raw string) (string, bool) {
	p := strings.Trim(strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/"), "/")
	if p == "" || len(p) > 200 {
		return "", false
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == ".." || strings.HasPrefix(part, ".") {
			return "", false
		}
	}
	return p, true
}

var hexColour = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// colourOf turns an Obsidian colour name into #rrggbb.
func colourOf(c string) string {
	c = strings.ToLower(strings.TrimSpace(c))
	if hexColour.MatchString(c) {
		return c
	}
	return map[string]string{
		"red": "#ff5d6c", "orange": "#ffa94d", "yellow": "#f2c94c", "green": "#5fd38d", "cyan": "#4fd1e8",
		"blue": "#5b9cff", "purple": "#b38cff", "pink": "#ff7ac6", "gray": "#9aa0a6", "grey": "#9aa0a6",
	}[c]
}

// boards are the boards the app last sent for the project.
func boards(req *plugin.Request, pid string) []board {
	var out []board
	req.State.GetProject(pid, keyBoards, &out)
	return out
}

// chosenBoard is the board the project uses: the one the person picked,
// or else the one whose notes come from the project's folder.
func chosenBoard(req *plugin.Request, pid, dir string) *board {
	all := boards(req, pid)
	var pick string
	req.State.GetProject(pid, keyBoard, &pick)
	for i := range all {
		if pick != "" && all[i].Path == pick {
			return &all[i]
		}
	}
	if dir == "" {
		return nil
	}
	for i := range all {
		if strings.EqualFold(all[i].Folder, dir) && (all[i].Property == "status" || all[i].Property == "stage") {
			return &all[i]
		}
	}
	for i := range all {
		if strings.EqualFold(all[i].Folder, dir) {
			return &all[i]
		}
	}
	return nil
}

// lanes are the project's lanes: its board's, or else the statuses its
// notes have, in the order of their stages.
func (v *vault) lanes(req *plugin.Request) []lane {
	if v.dir == "" {
		return nil
	}
	if b := chosenBoard(req, v.pid, v.dir); b != nil {
		return b.Lanes
	}
	type seen struct {
		name string
		rank int
	}
	var found []seen
	rank := map[string]int{"todo": 0, "doing": 1, "review": 2, "done": 3}
	// Statuses to stages, when set, gives the order.
	typed, _ := req.ProjectSettings[v.pid]["stages"].(string)
	var order []string
	for _, pair := range strings.FieldsFunc(typed, func(r rune) bool { return r == ';' || r == '\n' || r == ',' }) {
		if a, _, ok := strings.Cut(pair, "="); ok {
			order = append(order, strings.ToLower(strings.TrimSpace(a)))
		}
	}
	for _, c := range v.notes {
		if c.Gone {
			continue
		}
		n := parseNote(c.Text)
		raw := n.value(v.stageProp(n))
		if raw == "" || slices.ContainsFunc(found, func(x seen) bool { return strings.EqualFold(x.name, raw) }) {
			continue
		}
		r := rank[worksync.GuessKind(raw)] + 100
		if i := slices.Index(order, strings.ToLower(raw)); i >= 0 {
			r = i
		}
		found = append(found, seen{raw, r})
	}
	slices.SortStableFunc(found, func(a, b seen) int { return cmp.Or(a.rank-b.rank, strings.Compare(a.name, b.name)) })
	out := make([]lane, 0, len(found))
	for _, f := range found {
		out = append(out, lane{Name: f.name})
	}
	return out
}

// laneOf is the lane a note is in, by its board's name for it.
func laneOf(lanes []lane, raw string) string {
	for _, l := range lanes {
		if strings.EqualFold(l.Name, raw) {
			return l.Name
		}
	}
	return raw
}

// reconcileLanes takes lanes changed in Enplace (a card dropped in another
// lane of the board view, or the field changed on the item) into the
// notes. Each note remembers the lane both sides last agreed on, and when:
// when the work's lane moved off it since, and the note's didn't, Enplace
// changed it.
func (v *vault) reconcileLanes(s *worksync.Sync) {
	if v.dir == "" {
		return
	}
	key := s.Req.Plugin.ID + "." + laneField
	for p, c := range v.notes {
		if c.Gone {
			continue
		}
		n := parseNote(c.Text)
		prop := v.stageProp(n)
		note := n.value(prop)
		l := s.Links()[p]
		var w *plugin.Work
		if l != nil && l.State == worksync.Linked {
			w = workByID(s.Req, l.Work)
		}
		want := ""
		if w != nil {
			want = w.Fields[key]
		}
		if want != "" && !strings.EqualFold(want, c.Lane) && strings.EqualFold(note, c.Lane) && !strings.EqualFold(want, note) && w.UpdatedAt.After(c.LaneAt) {
			n.set(prop, want, false)
			if n.find("updated") != nil {
				n.set("updated", v.today(s), false)
			}
			c.Text, c.Mod, c.Lane, c.LaneAt = n.String(), s.Req.Now, want, s.Req.Now
			v.bump(c)
			continue
		}
		if c.Lane != note {
			c.Lane, c.LaneAt = note, s.Req.Now
			v.put(c)
		}
	}
}

// view is the "board" work view: the board's lanes, in order, with the
// project's work in the lane its note is in.
func boardView(req *plugin.Request) *plugin.View {
	view := &plugin.View{Groups: []plugin.Group{}, Columns: true}
	v := loadVault(req, &plugin.Response{}, req.ProjectID)
	if v.dir == "" {
		view.Groups = append(view.Groups, plugin.Group{Key: "_none", Title: "Your Obsidian board shows here", Muted: true, Work: []string{},
			Empty: "Set \"Use an existing folder\" in this project's Obsidian settings to the folder your board's notes are in."})
		return view
	}
	lanes := v.lanes(req)
	key := req.Plugin.ID + "." + laneField
	by := map[string][]string{}
	var other []string
	for _, w := range req.Work {
		if w.ProjectID != req.ProjectID {
			continue
		}
		ln := w.Fields[key]
		if ln == "" || !slices.ContainsFunc(lanes, func(l lane) bool { return strings.EqualFold(l.Name, ln) }) {
			if w.Fields[req.Plugin.ID+".link"] != "" || ln != "" {
				other = append(other, w.ID)
			}
			continue
		}
		ln = laneOf(lanes, ln)
		by[ln] = append(by[ln], w.ID)
	}
	b := chosenBoard(req, v.pid, v.dir)
	for _, l := range lanes {
		g := plugin.Group{Key: "lane:" + l.Name, Title: l.Name, Colour: l.Colour, Work: nonNilIDs(by[l.Name]), Set: map[string]string{key: l.Name},
			Empty: "Nothing in " + l.Name + ". Drop a card here to move its note."}
		if b == nil {
			g.Sub = "from your notes"
		}
		view.Groups = append(view.Groups, g)
	}
	if len(other) > 0 {
		view.Groups = append(view.Groups, plugin.Group{Key: "_other", Title: "Not on the board", Sub: "Their notes are in a lane the board doesn't have", Muted: true, Work: other})
	}
	return view
}

func nonNilIDs(x []string) []string {
	if x == nil {
		return []string{}
	}
	return x
}

// laneFieldState is the lane field's choices: the project's lanes.
func laneFieldState(req *plugin.Request, pid string) plugin.FieldState {
	v := loadVault(req, &plugin.Response{}, pid)
	if v.dir == "" {
		return plugin.FieldState{Hidden: true}
	}
	opts := []plugin.Option{}
	for _, l := range v.lanes(req) {
		opts = append(opts, plugin.Option{Value: l.Name, Label: l.Name, Short: l.Name, Colour: l.Colour})
	}
	return plugin.FieldState{Options: opts}
}

// boardPanel is the settings panel: which board the project follows, and
// where each of its lanes goes.
func boardPanel(s *worksync.Sync, v *vault) []plugin.Block {
	if s.ProjectID == "" {
		return []plugin.Block{{Type: plugin.BlockText, Tone: "muted", Text: "Pick a board in each project's Obsidian settings."}}
	}
	all := boards(s.Req, s.ProjectID)
	if len(all) == 0 {
		text := "Once Obsidian is connected, the boards in your vault (Bases files with a Kanban view) show here, so you can bring one in with its lanes."
		var last seen
		if s.Req.State.GetProject(s.ProjectID, keySeen, &last) && !last.At.IsZero() {
			text = "No boards found in your vault yet. Update the Enplace plugin in Obsidian to 1.2 or later so it can send them."
		}
		return []plugin.Block{{Type: plugin.BlockHeading, Text: "Board"}, {Type: plugin.BlockText, Tone: "muted", Text: text}}
	}
	b := chosenBoard(s.Req, s.ProjectID, v.dir)
	names := make([]string, 0, len(all))
	cur := ""
	for _, x := range all {
		names = append(names, boardLabel(x))
		if b != nil && x.Path == b.Path {
			cur = boardLabel(x)
		}
	}
	if cur == "" {
		names = append([]string{""}, names...)
	}
	out := []plugin.Block{{Type: plugin.BlockHeading, Text: "Board", Sub: "Its lanes, their order and colours come in. Nothing in the vault changes."},
		{Type: plugin.BlockForm, Text: fmt.Sprintf("%d %s found in your vault.", len(all), plural(len(all), "board")),
			Fields:  []plugin.Field{{Key: "board", Label: "Board", Type: "select", Options: names, Value: cur, Required: true}},
			Buttons: []plugin.Button{{Label: "Use this board", Action: "obsidian.board"}}}}
	if b == nil {
		return out
	}
	if v.dir == "" {
		dir := cmp.Or(b.Folder, "its folder")
		out = append(out, plugin.Block{Type: plugin.BlockText, Tone: "warn", Text: "Set \"Use an existing folder\" below to " + dir + " so its notes come in."})
	}
	stages := make([]string, 0, 6)
	for _, st := range s.Stages() {
		stages = append(stages, st.Name)
	}
	fields := make([]plugin.Field, 0, len(b.Lanes))
	for _, l := range b.Lanes {
		fields = append(fields, plugin.Field{Key: lanePrefix + l.Name, Label: l.Name, Type: "select", Options: stages,
			Value: s.StageName(s.StageFor(worksync.Item{Status: l.Name})), Required: true})
	}
	return append(out, plugin.Block{Type: plugin.BlockHeading, Text: "Lanes to stages", Sub: "Where a note in each lane sits in Enplace. Lanes in the same stage stay apart on the Obsidian board."},
		plugin.Block{Type: plugin.BlockForm, Fields: fields, Buttons: []plugin.Button{{Label: "Save lanes", Action: "obsidian.lanes"}}})
}

// lanePrefix starts a lane's field in the panel's form.
const lanePrefix = "lane:"

// lanePicks are the stages picked for lanes in the panel, lane to stage key.
func lanePicks(s *worksync.Sync) map[string]string {
	var picks map[string]string
	s.Req.State.GetProject(s.ProjectID, keyLanes, &picks)
	return picks
}

// pickLanes is the panel's "Save lanes": each lane of the board goes to
// the stage picked for it.
func pickLanes(s *worksync.Sync, v *vault, raw json.RawMessage) {
	var args map[string]any
	_ = json.Unmarshal(raw, &args)
	b := chosenBoard(s.Req, s.ProjectID, v.dir)
	if b == nil {
		s.Res.Toast = "Pick a board first"
		return
	}
	picks := map[string]string{}
	for _, l := range b.Lanes {
		name, _ := args[lanePrefix+l.Name].(string)
		for _, st := range s.Stages() {
			if strings.EqualFold(st.Name, name) || st.Key == name {
				picks[l.Name] = st.Key
			}
		}
	}
	s.Res.SetProject(s.ProjectID, keyLanes, picks)
	// Work already in a lane follows it.
	moved := 0
	for p, c := range v.notes {
		l := s.Links()[p]
		if c.Gone || l == nil || l.State != worksync.Linked || picks[c.Lane] == "" {
			continue
		}
		if w := workByID(s.Req, l.Work); w != nil && w.Stage != picks[c.Lane] && s.Move(w.ID, picks[c.Lane], "Its Obsidian lane, "+c.Lane+", now goes to "+s.StageName(picks[c.Lane])) {
			moved++
		}
	}
	s.Res.Toast = "Lanes saved"
	if moved > 0 {
		s.Res.Toast = fmt.Sprintf("Lanes saved, %d %s moved to match", moved, plural(moved, "item"))
	}
}

func boardLabel(b board) string {
	if b.Folder != "" {
		return b.Name + " · " + b.Folder
	}
	return b.Name
}

// pickBoard is the panel's "Use this board".
func pickBoard(s *worksync.Sync, raw json.RawMessage) {
	var args struct {
		Board string `json:"board"`
	}
	_ = json.Unmarshal(raw, &args)
	for _, b := range boards(s.Req, s.ProjectID) {
		if boardLabel(b) == args.Board || b.Path == args.Board {
			s.Res.SetProject(s.ProjectID, keyBoard, b.Path)
			s.Res.Toast = "Using " + b.Name + " from your vault"
			return
		}
	}
	s.Res.Toast = "Pick one of the boards found in your vault"
}

func plural(n int, w string) string {
	if n == 1 {
		return w
	}
	return w + "s"
}
