// Obsidian vault keeps a project's work in step with a folder of Markdown
// notes in an Obsidian vault, one note per work item.
//
// It extends Work sync. The vault is on the person's own devices, so
// Enplace can't reach it: the Enplace plugin for Obsidian (clients/obsidian
// in this repo) calls the project's webhook address instead, signed with
// its secret, every few seconds while Obsidian is open. Each call sends
// the notes that changed and takes back the notes Enplace changed. The
// webhook answers (contributes.webhook.answers), so one call does both.
//
// Everything about the notes happens here: reading and writing the front
// matter, which note is which work item, new notes, renames and deletes.
// The Obsidian plugin only reads and writes files.
package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin/worksync"
)

func main() { plugin.Run(handle) }

const (
	name = "Obsidian"
	// Obsidian is "away" when it hasn't called for this long.
	away = 2 * time.Minute
	// Calls older or newer than this are turned away (a replayed call).
	skew = 5 * time.Minute
	// How often the app should call while nothing changes, in seconds.
	poll = 10
	// Writes sent in one answer, at most; the rest come next call.
	maxWrites = 200
	// version 2 apps know the "path" answer (an existing folder); a
	// version 1 app is still served Enplace's own folder.
	version = 2
)

func config(req *plugin.Request, v *vault) worksync.Config {
	render := func(s *worksync.Sync, slot string) []plugin.Block {
		if slot == plugin.SlotSettingsPanel {
			return boardPanel(s, v)
		}
		return nil
	}
	act := func(s *worksync.Sync, name string, args json.RawMessage) error {
		switch name {
		case "obsidian.board":
			pickBoard(s, args)
		case "obsidian.lanes":
			pickLanes(s, v, args)
		}
		return nil
	}
	if v.dir != "" {
		// The person's own folder: Enplace never adds a note to it.
		// Their folder is their whole record, so finished notes come in too.
		return worksync.Config{Name: name, Noun: "note", Source: ownNotes{v}, Done: true, Render: render, Action: act, Stages: lanePicks}
	}
	// Every open work item gets a note unless the person said not to.
	mirror := req.ProjectSettings[v.pid]["create_outside"] != false
	return worksync.Config{Name: name, Noun: "note", Source: v, Mirror: mirror, Render: render, Action: act}
}

// ownNotes is the vault without Create, for a folder of the person's
// own notes.
type ownNotes struct{ v *vault }

func (o ownNotes) List(s *worksync.Sync) ([]worksync.Item, bool, error) { return o.v.List(s) }
func (o ownNotes) Update(s *worksync.Sync, it worksync.Item, ch worksync.Change) (worksync.Item, error) {
	return o.v.Update(s, it, ch)
}

func handle(req *plugin.Request) (*plugin.Response, error) {
	if req.Hook == plugin.HookWebhook {
		return exchange(req), nil
	}
	if req.Hook == plugin.HookView {
		res := &plugin.Response{}
		if req.View == viewBoard && req.ProjectID != "" {
			res.View = boardView(req)
		}
		return res, nil
	}
	pid := req.ProjectID
	if req.WorkID != "" {
		if w := workByID(req, req.WorkID); w != nil {
			pid = w.ProjectID
		}
	}
	if req.Event != nil {
		pid = req.Event.Work.ProjectID
	}
	if a := req.Action; a != nil && pid == "" {
		var args struct {
			Work string `json:"work"`
		}
		_ = json.Unmarshal(a.Args, &args)
		for i := range req.Work {
			if req.Work[i].ID == args.Work || req.Work[i].Key == args.Work {
				pid = req.Work[i].ProjectID
			}
		}
	}
	res := &plugin.Response{}
	v := loadVault(req, res, pid)
	out, err := worksync.Handle(req, config(req, v))
	if err != nil {
		return nil, err
	}
	// The vault's copies changed by the sync go into the same answer.
	if out.State != nil || len(v.dirty) > 0 {
		v.res = out
		v.save()
	}
	if req.Hook == plugin.HookRender && slices.Contains(req.Slots, plugin.SlotWorkFields) && pid != "" {
		out.Fields = map[string]plugin.FieldState{laneField: laneFieldState(req, pid)}
	}
	waitingLine(req, out, pid)
	folderLine(out, pid, v.dir)
	return out, nil
}

// folderLine names the person's own folder on the status line, so it's
// plain which folder of the vault syncs.
func folderLine(res *plugin.Response, pid, dir string) {
	if st := res.Status[pid]; st != nil && dir != "" && !strings.Contains(st.Text, "stopped") {
		st.Text += " · " + dir + " folder"
	}
}

// waitingLine says when Obsidian isn't calling, instead of "Synced".
func waitingLine(req *plugin.Request, res *plugin.Response, pid string) {
	st := res.Status[pid]
	if st == nil || strings.Contains(st.Text, "paused") || strings.Contains(st.Text, "stopped") {
		return
	}
	var last seen
	req.State.GetProject(pid, keySeen, &last)
	if req.Hook == plugin.HookWebhook || req.Now.Sub(last.At) < away {
		return
	}
	if last.At.IsZero() {
		st.Text = "Waiting for Obsidian: connect the vault from the Enplace plugin there"
		return
	}
	st.Text = "Waiting for Obsidian · last synced " + clock(req, last.At)
}

func clock(req *plugin.Request, t time.Time) string {
	loc, err := time.LoadLocation(req.Timezone)
	if err != nil {
		loc = time.UTC
	}
	if t.In(loc).Format("2006-01-02") != req.Now.In(loc).Format("2006-01-02") {
		return t.In(loc).Format("Jan 2, 15:04")
	}
	return t.In(loc).Format("15:04")
}

// call is what the Obsidian plugin sends.
type call struct {
	V int `json:"v"`
	// At is when it was sent, so an old call can't be sent again.
	At time.Time `json:"at"`
	// Since is the highest revision the app has written to the vault.
	Since int `json:"since"`
	// Changes are the notes changed in the vault since the last call.
	Changes []change `json:"changes"`
	// All, when sent, is every note's path in the folder, so a note
	// deleted while Obsidian was closed is found.
	All    []string `json:"all,omitempty"`
	Client string   `json:"client,omitempty"`
	// Boards, when sent, are the vault's boards (Bases kanban views):
	// sent when they change.
	Boards []board `json:"boards,omitempty"`
}

type change struct {
	Path    string    `json:"path"`
	OldPath string    `json:"old_path,omitempty"`
	Text    string    `json:"text,omitempty"`
	Deleted bool      `json:"deleted,omitempty"`
	Mod     time.Time `json:"mtime,omitzero"`
}

// answer is what goes back.
type answer struct {
	V       int     `json:"v"`
	Project project `json:"project"`
	// Folder is the project's folder name in the vault, under the app's
	// root, with the notes in its Work folder.
	Folder string `json:"folder"`
	// Path, when set, is the person's own folder of notes, from the
	// vault's top, used as it is: the app only changes notes already
	// there, and never adds one.
	Path string `json:"path,omitempty"`
	Rev  int    `json:"rev"`
	// Writes are the notes to write (or rename) in the vault.
	Writes []write  `json:"writes"`
	More   bool     `json:"more,omitempty"`
	Stages []stage  `json:"stages"`
	Status string   `json:"status"`
	Poll   int      `json:"poll"`
	Error  string   `json:"error,omitempty"`
	Skip   []string `json:"skipped,omitempty"`
}

type project struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

type stage struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

type write struct {
	Path string `json:"path"`
	Text string `json:"text"`
	Rev  int    `json:"rev"`
}

// exchange answers one call from the Obsidian plugin: take the vault's
// changes, sync, and send back what to write.
func exchange(req *plugin.Request) *plugin.Response {
	res := &plugin.Response{}
	pid := req.ProjectID
	a := answer{V: version, Poll: poll, Writes: []write{}, Stages: []stage{}}
	for _, p := range req.Projects {
		if p.ID == pid {
			a.Project = project{Key: p.Key, Name: p.Name}
			a.Folder = folderName(p.Name, p.Key)
			for _, st := range p.Stages {
				a.Stages = append(a.Stages, stage{Key: st.Key, Name: st.Name})
			}
		}
	}
	var in call
	if req.Webhook == nil || req.Webhook.Delivery == nil || json.Unmarshal(req.Webhook.Delivery.Body, &in) != nil {
		a.Error = "Enplace couldn't read that. Update the Enplace plugin in Obsidian."
		res.Data = a
		return res
	}
	a.Path = ownFolder(req, pid)
	if in.V < 1 || in.V > version || (a.Path != "" && in.V < 2) {
		a.Error = "Update the Enplace plugin in Obsidian to keep syncing."
		res.Data = a
		return res
	}
	if d := req.Now.Sub(in.At); d > skew || d < -skew {
		a.Error = "This device's clock is more than 5 minutes off, so Enplace turned the call away. Set the clock to update itself."
		res.Data = a
		return res
	}

	if in.Boards != nil {
		takeBoards(res, pid, in.Boards)
	}
	v := loadVault(req, res, pid)
	v.since = in.Since
	s := worksync.Open(req, res, config(req, v), pid)
	a.Skip = v.take(s, in)
	s.Run()
	v.stamp(s)
	s.Save()

	// What Obsidian hasn't written yet, oldest first.
	var due []*copyOf
	for _, c := range v.notes {
		if c.Rev > in.Since && !c.Gone {
			due = append(due, c)
		}
	}
	slices.SortFunc(due, func(x, y *copyOf) int { return x.Rev - y.Rev })
	a.Rev = v.rev
	if len(due) > maxWrites {
		due, a.More = due[:maxWrites], true
		a.Rev = due[len(due)-1].Rev
	}
	for _, c := range due {
		a.Writes = append(a.Writes, write{Path: c.Path, Text: c.Text, Rev: c.Rev})
	}
	// Notes deleted in the vault whose link is settled are forgotten.
	for p, c := range v.notes {
		if l := s.Links()[p]; c.Gone && (l == nil || l.State != worksync.Linked) {
			v.drop(p)
		}
	}
	v.save()
	res.SetProject(pid, keySeen, seen{At: req.Now, Client: clip(in.Client, 80), Since: in.Since})
	folderLine(res, pid, v.dir)
	if st := res.Status[pid]; st != nil {
		a.Status = st.Text
	}
	res.Data = a
	return res
}

// take stores the vault's changes, following renames, and links notes
// that name their work item. It returns the paths it couldn't take.
func (v *vault) take(s *worksync.Sync, in call) []string {
	var skipped []string
	now := s.Req.Now
	// Deletes first, so a note moved by another app (gone here, new
	// there, in one call) keeps its link.
	slices.SortStableFunc(in.Changes, func(x, y change) int {
		switch {
		case x.Deleted && !y.Deleted:
			return -1
		case y.Deleted && !x.Deleted:
			return 1
		}
		return 0
	})
	if in.All != nil {
		// Deleted while Obsidian was closed: in the copy (and already
		// written to the vault) but no longer in the folder.
		all := map[string]bool{}
		for _, p := range in.All {
			if c, ok := cleanPath(p); ok {
				all[c] = true
			}
		}
		for p, c := range v.notes {
			if !all[p] && !c.Gone && c.Rev <= in.Since {
				c.Gone, c.Mod = true, now
				v.put(c)
			}
		}
	}
	for _, ch := range in.Changes {
		p, ok := cleanPath(ch.Path)
		if !ok {
			skipped = append(skipped, clip(ch.Path, 240))
			continue
		}
		mod := ch.Mod
		if mod.IsZero() || mod.After(now) {
			mod = now
		}
		if ch.Deleted {
			if c := v.notes[p]; c != nil && !c.Gone {
				c.Gone, c.Mod = true, mod
				v.put(c)
			}
			continue
		}
		if len(ch.Text) > maxNote {
			skipped = append(skipped, p)
			continue
		}
		if old, ok := cleanPath(ch.OldPath); ok && old != p {
			// Renamed or moved in the vault: the same note.
			if c := v.notes[old]; c != nil {
				v.drop(old)
				c.Path = p
				v.put(c)
			}
			s.Relink(old, p)
		}
		c := v.notes[p]
		if c == nil {
			c = &copyOf{Path: p}
		}
		if c.Text == ch.Text && !c.Gone {
			continue
		}
		c.Text, c.Mod, c.Gone = ch.Text, mod, false
		v.put(c)
		if s.Links()[p] == nil {
			v.claim(s, c)
		}
	}
	return skipped
}

// claim links a new note that names its work item (enplace_id): to the
// link of a note of that item that's gone (moved while Obsidian was
// closed), or to the item itself (a restored or exported note). Any other
// new note becomes new work.
func (v *vault) claim(s *worksync.Sync, c *copyOf) {
	if v.dir != "" {
		v.claimOwn(s, c)
		return
	}
	key := parseNote(c.Text).value("enplace_id")
	if key == "" {
		return
	}
	w := workByKey(s.Req, s.ProjectID, key)
	if w == nil {
		return
	}
	for id, l := range s.Links() {
		if l.Work != w.ID {
			continue
		}
		// Its note is gone, or Obsidian never had it yet.
		if old := v.notes[id]; old == nil || old.Gone || old.Rev > v.since {
			v.drop(id)
			s.Relink(id, c.Path)
		}
		// Otherwise it's a copy of a note that's still there: new work.
		return
	}
	it := v.item(s, c)
	it.Ref = w.Key
	s.Adopt(it, w.ID)
}

// claimOwn links a new note in the person's own folder to the link of a
// gone note with the same id: the same note, moved while Obsidian was
// closed. Any other new note becomes new work.
func (v *vault) claimOwn(s *worksync.Sync, c *copyOf) {
	ref := v.ref(parseNote(c.Text), c.Path)
	for id, l := range s.Links() {
		if l.State != worksync.Linked || !strings.EqualFold(l.Ref, ref) {
			continue
		}
		if old := v.notes[id]; old == nil || old.Gone {
			v.drop(id)
			s.Relink(id, c.Path)
		}
		return
	}
}

// folderName is the project's folder in the vault: "Enplace (ENP)".
func folderName(name, key string) string {
	n := strings.Join(strings.Fields(unsafeName.ReplaceAllString(name, " ")), " ")
	n = strings.Trim(n, ". ")
	if n == "" {
		return key
	}
	return fmt.Sprintf("%s (%s)", n, key)
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
