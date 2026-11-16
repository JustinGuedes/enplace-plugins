// Package worksync keeps a project's Enplace work in step with work that
// lives somewhere else: a Linear team, a repo's issues, a folder of notes.
//
// It's the logic half of the "work-sync" base plugin. A source plugin
// extends that base in its manifest ("extends": ["work-sync"]), which gives
// it the settings, the work panel, the status line and the permissions, and
// builds its logic around this package:
//
//	func main() {
//		plugin.Run(func(req *plugin.Request) (*plugin.Response, error) {
//			return worksync.Handle(req, worksync.Config{Name: "Linear", Source: &linear{}})
//		})
//	}
//
// The source is then mostly a client for its API: list the items, change
// one, and optionally create or close one. This package does the rest:
//
//   - The link table: which outside item is which work item, and what both
//     looked like when they last agreed. It's kept in the plugin's own
//     state, and each linked item carries the outside reference in the
//     plugin's "link" work field, so a person sees it on the card.
//   - Mapping: title, Markdown body, stage (by the outside status, with the
//     project's own mapping setting), priority, labels as tags, and due date.
//   - The sync rules: two-way, pull only or push only. When both sides
//     changed the same thing since they last agreed, the later change wins
//     and the other one is posted as a note on the item, so nothing is lost.
//   - Every change in Enplace is an op (work.import, work.create,
//     work.update, work.move, work.note, work.fields), so the person's rules
//     and Approvals apply. The first import is one batch: one approval.
//   - Deletes are never mirrored as deletes. Work removed outside moves to a
//     done stage with a note; work deleted in Enplace is closed outside only
//     when the source can close things.
package worksync

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

// Item is one piece of work as the outside source has it, already in
// Enplace's terms.
type Item struct {
	// ID is the source's own stable id for the item.
	ID string `json:"id"`
	// Ref is what people call it there, like "LIN-142" or "#12". It's
	// shown on the work card.
	Ref string `json:"ref"`
	URL string `json:"url,omitempty"`
	// Title and Body (Markdown).
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
	// Status is the source's own name for where it is, like "In Progress".
	// StatusKind is todo, doing, review or done when the source knows;
	// otherwise it's guessed from Status.
	Status     string `json:"status,omitempty"`
	StatusKind string `json:"status_kind,omitempty"`
	// Stage is a project stage key, for a source whose statuses are
	// coarser than the project's stages (GitHub issues are only open or
	// closed): it keeps the stage the work is in while the outside status
	// still fits it. It wins over Status when it names one of the
	// project's stages.
	Stage string `json:"stage,omitempty"`
	// Priority is 0 none, 1 low, 2 medium, 3 high, 4 urgent.
	Priority int      `json:"priority,omitempty"`
	Labels   []string `json:"labels,omitempty"`
	Due      string   `json:"due,omitempty"` // YYYY-MM-DD
	// Assignee is shown on the work panel, never synced.
	Assignee string `json:"assignee,omitempty"`
	// Author is who opened it, shown in the tray.
	Author    string    `json:"author,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
	// Deleted reports an item the source knows was removed.
	Deleted bool `json:"deleted,omitempty"`
	// Fields are the source plugin's own work fields to show on the item,
	// by key without the plugin id (like "points" for linear.points). They
	// only come in: a value changed in Enplace is never sent out, and the
	// next change outside replaces it. An empty value clears the field.
	Fields map[string]string `json:"fields,omitempty"`
}

// Change is what to change on an outside item. Nil fields stay as they are.
type Change struct {
	Title *string `json:"title,omitempty"`
	Body  *string `json:"body,omitempty"`
	// Status is the outside status to set, from the project's mapping when
	// it names one; otherwise empty, and StatusKind says where the work
	// is (todo, doing, review or done) for the source to pick its own.
	Status     *string   `json:"status,omitempty"`
	StatusKind *string   `json:"status_kind,omitempty"`
	Priority   *int      `json:"priority,omitempty"`
	Labels     *[]string `json:"labels,omitempty"`
	Due        *string   `json:"due,omitempty"`
}

// Empty reports whether the change changes nothing.
func (c Change) Empty() bool {
	return c.Title == nil && c.Body == nil && c.Status == nil && c.StatusKind == nil && c.Priority == nil && c.Labels == nil && c.Due == nil
}

// Source is a client for one outside system.
type Source interface {
	// List returns the items in the project's scope. full says the list
	// is everything there is, so a linked item missing from it was
	// removed; a source that only lists what changed returns false and
	// marks removed items Deleted.
	List(s *Sync) (items []Item, full bool, err error)
	// Update changes one item outside and returns it as it is now.
	Update(s *Sync, it Item, ch Change) (Item, error)
}

// Creator is a source that can add new items, for work created in
// Enplace (with the "create outside" setting on).
type Creator interface {
	Create(s *Sync, w plugin.Work, it Item) (Item, error)
}

// Closer is a source that can close an item, for work deleted in Enplace.
type Closer interface {
	Close(s *Sync, it Item) error
}

// Config is a source plugin's half of the sync.
type Config struct {
	// Name is the source as people know it: "Linear", "GitHub Issues".
	Name   string
	Source Source
	// Action handles the source's own actions; the sync's own are
	// "sync.now", "sync.pause", "sync.resume" and "sync.unlink".
	Action func(s *Sync, name string, args json.RawMessage) error
	// Render adds the source's own blocks for slots the sync doesn't
	// answer (like its settings panel).
	Render func(s *Sync, slot string) []plugin.Block
	// Skip names what the source doesn't have, so it's neither sent nor
	// read: "body", "priority", "labels", "due".
	Skip []string
	// Hold, when set, says which new outside items wait in the project's
	// tray (the "tray" project card and a line in the brief) for the
	// person to Accept or Skip, instead of coming straight in. Items it
	// says no to come in as before.
	Hold func(s *Sync, it Item) bool
	// Noun is what one outside item is called, like "issue", for the tray
	// and the brief. Default "item".
	Noun string
	// Mirror sends every open work item of the project out (a Creator
	// source only), the ones there before the sync started too, whatever
	// the "create outside" setting says: for a source that is a copy of
	// the project, like a folder of notes.
	Mirror bool
	// Done brings in outside items that are already done too, in a done
	// stage, for a source that is the person's whole record of the work
	// (like their own folder of notes). Otherwise finished items stay out.
	Done bool
}

func (s *Sync) skips(field string) bool { return slices.Contains(s.cfg.Skip, field) }

// AuthError is a source's error when its key or token was refused. The
// sync stops and asks the person to fix it.
type AuthError struct{ Msg string }

// Error is the message to show the person.
func (e *AuthError) Error() string { return e.Msg }

// ErrNotSet is a source's error when it isn't set up yet for the project
// (no team or repo picked). The sync waits quietly.
var ErrNotSet = errors.New("not set up yet")

// Directions, the "direction" setting.
const (
	TwoWay   = "Two-way"
	PullOnly = "Only bring changes in"
	PushOnly = "Only send changes out"
)

// Link states.
const (
	Filed   = "filed"   // asked Enplace to add it; may wait for approval
	Linked  = "linked"  // in step
	Skipped = "skipped" // the person refused it, or unlinked it
	Removed = "removed" // gone on one side
	Held    = "held"    // waits in the tray for the person
	Passed  = "passed"  // skipped from the tray; back if it changes
)

// Snap is what an item looked like when both sides last agreed, in
// Enplace's terms. The body is kept as a hash.
type Snap struct {
	Title    string   `json:"t"`
	Body     string   `json:"b"` // hash
	Stage    string   `json:"s"` // stage key
	Priority int      `json:"p"`
	Tags     []string `json:"g,omitempty"`
	Due      string   `json:"d,omitempty"`
}

// Link is one outside item and the work item it's linked to.
type Link struct {
	ID    string `json:"id"` // outside id
	Ref   string `json:"ref"`
	URL   string `json:"url,omitempty"`
	Work  string `json:"w,omitempty"` // work id once it exists
	State string `json:"st"`
	Base  Snap   `json:"base"`
	// Status is the outside status name last seen, for the panel.
	Status   string `json:"os,omitempty"`
	Assignee string `json:"as,omitempty"`
	// Title, Author and Labels are kept for held items, for the tray.
	Title  string    `json:"t,omitempty"`
	Author string    `json:"by,omitempty"`
	Labels []string  `json:"lb,omitempty"`
	Seen   time.Time `json:"seen"`   // outside updated time last seen
	Synced time.Time `json:"synced"` // when they last agreed
	Filed  time.Time `json:"filed,omitzero"`
}

// Status is the sync's state for one project.
type Status struct {
	Since  time.Time `json:"since,omitzero"` // first sync
	LastOK time.Time `json:"last_ok,omitzero"`
	Error  string    `json:"error,omitempty"`
	Auth   bool      `json:"auth,omitempty"` // the error needs the person
	Paused bool      `json:"paused,omitempty"`
	Linked int       `json:"linked"`
	Held   int       `json:"held,omitempty"` // waiting in the tray
}

// Storage keys. The link table is split in shards so each stays small.
const (
	keyStatus  = "sync.status"
	keyLinks   = "sync.links."
	keyPublic  = "public.links"
	shards     = 8
	maxOps     = 50
	importSize = 500
)

// Sync is one call's view of one project: what a source works with.
type Sync struct {
	Req       *plugin.Request
	Res       *plugin.Response
	ProjectID string
	Name      string
	// Fetch is plugin.Fetch; tests swap it.
	Fetch func(plugin.FetchRequest) (*plugin.FetchResponse, error)

	cfg     Config
	project *plugin.Project
	links   map[string]*Link // by outside id
	dirty   map[int]bool
	status  Status
	ops     int
	// asked marks the ops filed now as asked for by the person (Bring).
	asked bool
}

// Setting is one of the project's settings for the plugin, as text.
func (s *Sync) Setting(key string) string {
	switch v := s.Req.ProjectSettings[s.ProjectID][key].(type) {
	case string:
		return strings.TrimSpace(v)
	case bool:
		if v {
			return "true"
		}
		return ""
	case float64:
		return fmt.Sprint(v)
	}
	return ""
}

// Secret is one of the plugin's secrets for the project.
func (s *Sync) Secret(key string) string { return s.Req.Secrets[key] }

// Links are the project's links, by outside id.
func (s *Sync) Links() map[string]*Link { return s.links }

// Field is the work field that carries an item's outside reference.
func (s *Sync) Field() string { return s.Req.Plugin.ID + ".link" }

// Open is one project's sync, for a source that answers a hook the sync
// doesn't (like a webhook its own app calls): change what it needs, call
// Run, then Save.
func Open(req *plugin.Request, res *plugin.Response, cfg Config, projectID string) *Sync {
	return open(req, res, cfg, projectID)
}

// Save writes what the sync changed into the response.
func (s *Sync) Save() { s.save() }

// Relink moves a link to a new outside id, for an item whose id changed
// outside (a note renamed or moved). It reports whether there was a link
// to move; one already at the new id is replaced.
func (s *Sync) Relink(from, to string) bool {
	l := s.links[from]
	if l == nil || from == to {
		return false
	}
	delete(s.links, from)
	s.dirty[shard(from)] = true
	l.ID = to
	s.links[to] = l
	s.touch(l)
	return true
}

// Adopt links an outside item to work that already exists (a note that
// names its work item but has no link yet, like one restored from a
// backup). Where the two differ, the side changed last wins at the next
// Run: the outside item when it's newer than the work, else the work.
func (s *Sync) Adopt(it Item, workID string) bool {
	w := findWork(s.Req, workID)
	if w == nil || w.ProjectID != s.ProjectID || s.links[it.ID] != nil || s.linkFor(w.ID) != nil {
		return false
	}
	base := s.snapItem(it)
	if it.UpdatedAt.After(w.UpdatedAt) {
		base = s.snapWork(w)
	}
	l := &Link{ID: it.ID, Ref: it.Ref, URL: it.URL, Work: w.ID, State: Linked, Base: base, Status: it.Status, Seen: it.UpdatedAt.Add(-time.Second), Synced: s.Req.Now}
	s.links[it.ID] = l
	s.touch(l)
	if w.Fields[s.Field()] != it.Ref {
		s.op("work.fields", map[string]any{"id": w.ID, "fields": map[string]string{s.Field(): it.Ref}})
	}
	return true
}

// Handle answers every hook for a source plugin.
func Handle(req *plugin.Request, cfg Config) (*plugin.Response, error) {
	res := &plugin.Response{}
	switch req.Hook {
	case plugin.HookSchedule:
		if req.ProjectID != "" {
			s := open(req, res, cfg, req.ProjectID)
			s.Run()
			s.save()
		}
	case plugin.HookEvent:
		// A person changed linked work: send it out now, not at the next
		// check.
		if e := req.Event; e != nil && e.Type != "work.created" {
			s := open(req, res, cfg, e.Work.ProjectID)
			if l := s.linkFor(e.Work.ID); l != nil && l.State == Linked && s.direction() != PullOnly {
				s.Run()
				s.save()
			}
		}
	case plugin.HookAction:
		if req.Action == nil {
			break
		}
		pid := req.ProjectID
		var args struct {
			Work string   `json:"work"`
			IDs  []string `json:"ids"`
		}
		_ = json.Unmarshal(req.Action.Args, &args)
		if pid == "" && args.Work != "" {
			if w := findWork(req, args.Work); w != nil {
				pid = w.ProjectID
			}
		}
		s := open(req, res, cfg, pid)
		if err := s.action(req.Action.Name, args.Work, args.IDs); err != nil {
			return nil, err
		}
		s.save()
	case plugin.HookRender:
		pid := req.ProjectID
		if req.WorkID != "" {
			if w := findWork(req, req.WorkID); w != nil {
				pid = w.ProjectID
			}
		}
		s := open(req, res, cfg, pid)
		s.render()
	}
	return res, nil
}

func findWork(req *plugin.Request, id string) *plugin.Work {
	for i := range req.Work {
		if req.Work[i].ID == id || req.Work[i].Key == id {
			return &req.Work[i]
		}
	}
	return nil
}

func open(req *plugin.Request, res *plugin.Response, cfg Config, pid string) *Sync {
	s := &Sync{Req: req, Res: res, ProjectID: pid, Name: cmp.Or(cfg.Name, "the source"), Fetch: plugin.Fetch, cfg: cfg, links: map[string]*Link{}, dirty: map[int]bool{}}
	for i := range req.Projects {
		if req.Projects[i].ID == pid {
			s.project = &req.Projects[i]
		}
	}
	for n := range shards {
		var part map[string]*Link
		if req.State.GetProject(pid, fmt.Sprint(keyLinks, n), &part) {
			for id, l := range part {
				s.links[id] = l
			}
		}
	}
	req.State.GetProject(pid, keyStatus, &s.status)
	return s
}

func shard(id string) int {
	h := fnv.New32a()
	h.Write([]byte(id))
	return int(h.Sum32() % shards)
}

func (s *Sync) touch(l *Link) { s.dirty[shard(l.ID)] = true }

// save writes the shards that changed, the status and what's published.
func (s *Sync) save() {
	if s.ProjectID == "" {
		return
	}
	for n := range shards {
		if !s.dirty[n] {
			continue
		}
		part := map[string]*Link{}
		for id, l := range s.links {
			if shard(id) == n {
				part[id] = l
			}
		}
		if len(part) == 0 {
			s.Res.SetProject(s.ProjectID, fmt.Sprint(keyLinks, n), nil)
		} else {
			s.Res.SetProject(s.ProjectID, fmt.Sprint(keyLinks, n), part)
		}
	}
	s.status.Linked, s.status.Held = 0, 0
	pub := map[string]string{}
	for _, l := range s.links {
		if l.State == Held {
			s.status.Held++
		}
		if l.State == Linked && l.Work != "" {
			s.status.Linked++
			if len(pub) < 4000 {
				pub[l.Work] = l.Ref
			}
		}
	}
	s.Res.SetProject(s.ProjectID, keyStatus, s.status)
	if len(s.dirty) > 0 {
		// Other plugins can tell which work is linked where.
		s.Res.SetProject(s.ProjectID, keyPublic, map[string]any{"source": s.Name, "work": pub})
	}
	if s.Res.Status == nil {
		s.Res.Status = map[string]*plugin.ProjectStatus{}
	}
	s.Res.Status[s.ProjectID] = &plugin.ProjectStatus{Text: s.statusLine()}
}

func (s *Sync) statusLine() string {
	switch {
	case s.status.Paused:
		return s.Name + " sync paused"
	case s.status.Error != "":
		return s.Name + " sync stopped: " + s.status.Error
	case s.status.LastOK.IsZero():
		return "Not synced with " + s.Name + " yet"
	}
	line := "Synced with " + s.Name + " at " + s.clock(s.status.LastOK)
	if s.status.Held > 0 {
		line += fmt.Sprintf(" · %d waiting", s.status.Held)
	}
	return line
}

func (s *Sync) clock(t time.Time) string {
	loc, err := time.LoadLocation(s.Req.Timezone)
	if err != nil {
		loc = time.UTC
	}
	return t.In(loc).Format("15:04")
}

func (s *Sync) direction() string {
	switch d := s.Setting("direction"); d {
	case PullOnly, PushOnly:
		return d
	}
	return TwoWay
}

func (s *Sync) pulls() bool  { return s.direction() != PushOnly }
func (s *Sync) pushes() bool { return s.direction() != PullOnly }

func (s *Sync) linkFor(workID string) *Link {
	for _, l := range s.links {
		if l.Work == workID && l.State != Skipped {
			return l
		}
	}
	return nil
}

func (s *Sync) op(kind string, params any) bool {
	if s.ops >= maxOps {
		return false
	}
	s.ops++
	s.Res.Ops = append(s.Res.Ops, plugin.Op{Kind: kind, ProjectID: s.ProjectID, Params: params, Asked: s.asked})
	return true
}

// waiting reports whether a change this plugin filed for the work id, or
// carrying the outside reference, still waits for approval.
func (s *Sync) waiting(workID, ref string) bool {
	for _, op := range s.Req.Waiting {
		raw, _ := json.Marshal(op.Params)
		var p map[string]any
		if json.Unmarshal(raw, &p) != nil {
			continue
		}
		if workID != "" && p["id"] == workID {
			return true
		}
		if ref != "" && strings.Contains(string(raw), fmt.Sprintf("%q:%q", s.Field(), ref)) {
			return true
		}
	}
	return false
}

// Run syncs the project once.
func (s *Sync) Run() {
	if s.ProjectID == "" || s.status.Paused || s.cfg.Source == nil {
		return
	}
	now := s.Req.Now
	items, full, err := s.cfg.Source.List(s)
	if err != nil {
		s.fail(err)
		return
	}
	if s.status.Since.IsZero() {
		s.status.Since = now
	}
	byID := map[string]*Item{}
	for i := range items {
		byID[items[i].ID] = &items[i]
	}
	work := map[string]*plugin.Work{}
	byRef := map[string]*plugin.Work{}
	var mine []*plugin.Work
	for i := range s.Req.Work {
		w := &s.Req.Work[i]
		if w.ProjectID != s.ProjectID {
			continue
		}
		mine = append(mine, w)
		work[w.ID] = w
		if ref := w.Fields[s.Field()]; ref != "" {
			byRef[ref] = w
		}
	}

	// What was filed: now in Enplace, still waiting, or refused.
	for _, l := range s.sorted() {
		if l.State != Filed {
			continue
		}
		if w := byRef[l.Ref]; w != nil {
			l.Work, l.State, l.Synced = w.ID, Linked, now
			l.Base = s.snapWork(w)
			s.touch(l)
		} else if !s.waiting("", l.Ref) {
			l.State = Skipped
			s.touch(l)
		}
	}

	// New outside items come in, the first time as one batch.
	first := len(s.links) == 0
	var fresh []Item
	for _, it := range items {
		if l := s.links[it.ID]; l != nil && (l.State == Held || l.State == Passed) {
			// In the tray, or skipped from it.
			switch {
			case it.Deleted || s.kind(it) == "done":
				delete(s.links, it.ID)
				s.dirty[shard(it.ID)] = true
				continue
			case !s.holds(it):
				// No longer held (a label added, the tray switched off):
				// it comes in like any new item.
				delete(s.links, it.ID)
				s.dirty[shard(it.ID)] = true
			default:
				if l.State == Passed && it.UpdatedAt.After(l.Seen) {
					l.State = Held // changed since it was skipped
				}
				s.hold(l, it)
				continue
			}
		}
		if it.Deleted || s.links[it.ID] != nil {
			continue
		}
		if w := byRef[it.Ref]; w != nil {
			// Linked by hand (the field set some other way).
			l := &Link{ID: it.ID, Ref: it.Ref, URL: it.URL, Work: w.ID, State: Linked, Base: s.snapWork(w), Seen: it.UpdatedAt, Synced: now}
			s.links[it.ID] = l
			s.touch(l)
			continue
		}
		if s.kind(it) == "done" && !s.cfg.Done {
			continue // finished before it was ever linked
		}
		if s.pulls() && s.holds(it) {
			l := &Link{ID: it.ID, State: Held}
			s.links[it.ID] = l
			s.hold(l, it)
			continue
		}
		fresh = append(fresh, it)
	}
	if s.pulls() && len(fresh) > 0 {
		s.bringIn(fresh, first)
	}

	// Held items gone from a full list were removed outside.
	if full {
		for id, l := range s.links {
			if (l.State == Held || l.State == Passed) && byID[id] == nil {
				delete(s.links, id)
				s.dirty[shard(id)] = true
			}
		}
	}

	// Linked items: removals, then changes either way.
	for _, l := range s.sorted() {
		if l.State != Linked {
			continue
		}
		w := work[l.Work]
		it := byID[l.ID]
		if w == nil {
			// Deleted in Enplace: never mirrored as a delete; closed
			// outside only where the source can close things.
			if c, ok := s.cfg.Source.(Closer); ok && s.pushes() && it != nil && !it.Deleted {
				if err := c.Close(s, *it); err != nil {
					s.fail(err)
					return
				}
			}
			l.State = Removed
			s.touch(l)
			continue
		}
		if (it == nil && full) || (it != nil && it.Deleted) {
			l.State = Removed
			s.touch(l)
			if !w.Done() && s.pulls() {
				s.op("work.move", map[string]any{"id": w.ID, "stage": "done", "reason": "Removed in " + s.Name,
					"note": fmt.Sprintf("Removed in %s (%s), so it's marked done here. Nothing was deleted.", s.Name, l.Ref)})
			}
			continue
		}
		if it == nil || s.waiting(w.ID, "") {
			continue
		}
		if err := s.merge(l, w, it); err != nil {
			s.fail(err)
			return
		}
	}

	// New Enplace work goes out, when the person asked for that.
	if c, ok := s.cfg.Source.(Creator); ok && s.pushes() && (s.cfg.Mirror || s.Setting("create_outside") == "true") {
		for _, w := range mine {
			if w.Fields[s.Field()] != "" || s.linkFor(w.ID) != nil || (!s.cfg.Mirror && w.CreatedAt.Before(s.status.Since)) || w.Done() {
				continue
			}
			if s.ops >= maxOps {
				break // the rest go out next time
			}
			it, err := c.Create(s, *w, s.itemFor(w))
			if err != nil {
				s.fail(err)
				return
			}
			l := &Link{ID: it.ID, Ref: it.Ref, URL: it.URL, Work: w.ID, State: Linked, Base: s.snapWork(w), Status: it.Status, Seen: it.UpdatedAt, Synced: now}
			s.links[it.ID] = l
			s.touch(l)
			s.op("work.fields", map[string]any{"id": w.ID, "fields": map[string]string{s.Field(): it.Ref}})
		}
	}
	s.status.LastOK, s.status.Error, s.status.Auth = now, "", false
}

// holds reports whether a new item waits in the tray.
func (s *Sync) holds(it Item) bool { return s.cfg.Hold != nil && s.cfg.Hold(s, it) }

// hold keeps what the tray shows about a held item.
func (s *Sync) hold(l *Link, it Item) {
	l.Ref, l.URL, l.Title, l.Author, l.Labels = it.Ref, it.URL, cmp.Or(strings.TrimSpace(it.Title), it.Ref), it.Author, it.Labels
	l.Status, l.Assignee, l.Seen = it.Status, it.Assignee, it.UpdatedAt
	s.touch(l)
}

// Bring files held items (all of them when ids is empty): several at once
// as one import, so the person approves them together. It reads the
// source again, so what comes in is what's there now.
func (s *Sync) Bring(ids []string) (int, error) {
	want := s.trayIDs(ids, Held)
	if len(want) == 0 {
		return 0, nil
	}
	items, _, err := s.cfg.Source.List(s)
	if err != nil {
		s.fail(err)
		return 0, err
	}
	var in []Item
	for _, it := range items {
		if want[it.ID] && !it.Deleted && s.kind(it) != "done" {
			in = append(in, it)
		}
	}
	for id := range want {
		if !slices.ContainsFunc(in, func(it Item) bool { return it.ID == id }) {
			// Closed or gone meanwhile: off the tray.
			delete(s.links, id)
			s.dirty[shard(id)] = true
		}
	}
	if len(in) > 0 {
		// The person pressed Accept: that's their approval.
		s.asked = true
		s.bringIn(in, len(in) > 1)
		s.asked = false
	}
	return len(in), nil
}

// Skip takes held items off the tray (all of them when ids is empty).
// One comes back if it changes outside.
func (s *Sync) Skip(ids []string) int {
	n := 0
	for id := range s.trayIDs(ids, Held) {
		l := s.links[id]
		l.State = Passed
		s.touch(l)
		n++
	}
	return n
}

func (s *Sync) trayIDs(ids []string, state string) map[string]bool {
	out := map[string]bool{}
	for id, l := range s.links {
		if l.State == state && (len(ids) == 0 || slices.Contains(ids, id)) {
			out[id] = true
		}
	}
	return out
}

func (s *Sync) fail(err error) {
	if errors.Is(err, ErrNotSet) {
		s.status.Error, s.status.Auth = "", false
		return
	}
	var auth *AuthError
	s.status.Auth = errors.As(err, &auth)
	msg := err.Error()
	if len(msg) > 120 {
		msg = msg[:120]
	}
	s.status.Error = msg
}

// sorted is the links in a steady order, so runs are repeatable.
func (s *Sync) sorted() []*Link {
	out := make([]*Link, 0, len(s.links))
	for _, l := range s.links {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// bringIn files new outside items: several at once as one import, so a
// person approves them together.
func (s *Sync) bringIn(items []Item, first bool) {
	sort.SliceStable(items, func(i, j int) bool { return items[i].Ref < items[j].Ref })
	if len(items) > importSize {
		items = items[:importSize]
	}
	inputs := make([]map[string]any, 0, len(items))
	for _, it := range items {
		inputs = append(inputs, s.workInput(it))
	}
	var ok bool
	if len(items) == 1 && !first {
		ok = s.op("work.create", inputs[0])
	} else {
		ok = s.op("work.import", map[string]any{"items": inputs})
	}
	if !ok {
		return
	}
	now := s.Req.Now
	for _, it := range items {
		l := &Link{ID: it.ID, Ref: it.Ref, URL: it.URL, State: Filed, Base: s.snapItem(it), Status: it.Status, Assignee: it.Assignee, Seen: it.UpdatedAt, Filed: now}
		s.links[it.ID] = l
		s.touch(l)
	}
}

func (s *Sync) workInput(it Item) map[string]any {
	fields := map[string]string{s.Field(): it.Ref}
	for k, v := range it.Fields {
		if v != "" {
			fields[s.Req.Plugin.ID+"."+k] = v
		}
	}
	in := map[string]any{"title": cmp.Or(strings.TrimSpace(it.Title), it.Ref), "fields": fields}
	if it.Body != "" && !s.skips("body") {
		in["body"] = it.Body
	}
	if st := s.stageFor(it); st != "" {
		in["stage"] = st
	}
	if it.Priority > 0 && !s.skips("priority") {
		in["priority"] = it.Priority
	}
	if len(it.Labels) > 0 && !s.skips("labels") {
		in["tags"] = it.Labels
	}
	if it.Due != "" && !s.skips("due") {
		in["due"] = it.Due
	}
	return in
}

// merge brings one linked pair back in step, field by field.
func (s *Sync) merge(l *Link, w *plugin.Work, it *Item) error {
	e, o, b := s.snapWork(w), s.snapItem(*it), l.Base
	l.Status, l.Assignee, l.URL, l.Ref = it.Status, it.Assignee, cmp.Or(it.URL, l.URL), cmp.Or(it.Ref, l.Ref)
	outsideNewer := it.UpdatedAt.After(w.UpdatedAt)
	var in map[string]any // changes to the work item
	var out Change
	var lost []string
	next := b
	type field struct {
		key      string
		name     string
		ev, ov   string // as compared
		eShow    string // as shown in a note
		oShow    string
		pull     func()
		push     func()
		settle   func(fromOutside bool)
		noteable bool
	}
	set := func(k string, v any) {
		if in == nil {
			in = map[string]any{"id": w.ID}
		}
		in[k] = v
	}
	stage := ""
	fields := []field{
		{key: "title", name: "title", ev: e.Title, ov: o.Title, eShow: w.Title, oShow: it.Title, noteable: true,
			pull: func() { set("title", it.Title) }, push: func() { t := w.Title; out.Title = &t },
			settle: func(fo bool) { next.Title = pick(fo, o.Title, e.Title) }},
		{key: "body", name: "description", ev: e.Body, ov: o.Body, eShow: w.Body, oShow: it.Body, noteable: true,
			pull: func() { set("body", it.Body) }, push: func() { t := w.Body; out.Body = &t },
			settle: func(fo bool) { next.Body = pick(fo, o.Body, e.Body) }},
		{key: "stage", name: "stage", ev: e.Stage, ov: o.Stage, eShow: s.stageName(e.Stage), oShow: it.Status,
			pull: func() { stage = o.Stage }, push: func() {
				name, kind := s.statusFor(e.Stage)
				if name != "" {
					out.Status = &name
				}
				out.StatusKind = &kind
			},
			settle: func(fo bool) { next.Stage = pick(fo, o.Stage, e.Stage) }},
		{key: "priority", name: "priority", ev: fmt.Sprint(e.Priority), ov: fmt.Sprint(o.Priority), eShow: priorityName(e.Priority), oShow: priorityName(o.Priority),
			pull: func() { set("priority", it.Priority) }, push: func() { p := w.Priority; out.Priority = &p },
			settle: func(fo bool) { next.Priority = pick(fo, o.Priority, e.Priority) }},
		{key: "labels", name: "tags", ev: strings.Join(e.Tags, ","), ov: strings.Join(o.Tags, ","), eShow: strings.Join(e.Tags, ", "), oShow: strings.Join(o.Tags, ", "),
			pull: func() { set("tags", nonNil(it.Labels)) }, push: func() { t := nonNil(w.Tags); out.Labels = &t },
			settle: func(fo bool) { next.Tags = pick(fo, o.Tags, e.Tags) }},
		{key: "due", name: "due date", ev: e.Due, ov: o.Due, eShow: e.Due, oShow: o.Due,
			pull: func() { set("due", it.Due) }, push: func() { d := w.Due; out.Due = &d },
			settle: func(fo bool) { next.Due = pick(fo, o.Due, e.Due) }},
	}
	bv := map[string]string{"title": b.Title, "description": b.Body, "stage": b.Stage, "priority": fmt.Sprint(b.Priority), "tags": strings.Join(b.Tags, ","), "due date": b.Due}
	for _, f := range fields {
		if s.skips(f.key) {
			continue
		}
		eCh, oCh := f.ev != bv[f.name], f.ov != bv[f.name]
		switch {
		case !eCh && !oCh, f.ev == f.ov:
			f.settle(true)
		case oCh && !eCh:
			if s.pulls() {
				f.pull()
				f.settle(true)
			}
		case eCh && !oCh:
			if s.pushes() {
				f.push()
				f.settle(false)
			}
		default:
			// Both changed: the later change wins, unless the direction
			// says which side leads.
			fromOutside := outsideNewer
			if !s.pushes() {
				fromOutside = true
			} else if !s.pulls() {
				fromOutside = false
			}
			if fromOutside {
				f.pull()
				lost = append(lost, s.lostLine(f.name, "Enplace", f.eShow, f.noteable))
			} else {
				f.push()
				lost = append(lost, s.lostLine(f.name, s.Name, f.oShow, f.noteable))
			}
			f.settle(fromOutside)
		}
	}
	if !out.Empty() {
		got, err := s.cfg.Source.Update(s, *it, out)
		if err != nil {
			return err
		}
		l.Status, l.Seen = cmp.Or(got.Status, l.Status), got.UpdatedAt
	} else {
		l.Seen = it.UpdatedAt
	}
	if in != nil {
		s.op("work.update", in)
	}
	if extra := s.extraFields(w, it); len(extra) > 0 {
		s.op("work.fields", map[string]any{"id": w.ID, "fields": extra})
	}
	if stage != "" {
		s.op("work.move", map[string]any{"id": w.ID, "stage": stage, "reason": "Moved in " + s.Name})
	}
	if len(lost) > 0 {
		s.op("work.note", map[string]any{"id": w.ID, "body": fmt.Sprintf("%s and Enplace both changed this since the last sync. The later change was kept; here's what it replaced:\n\n%s", s.Name, strings.Join(lost, "\n"))})
	}
	if in != nil || stage != "" || !out.Empty() || !snapEqual(next, l.Base) {
		l.Base, l.Synced = next, s.Req.Now
		s.touch(l)
	}
	return nil
}

// extraFields are the source's own fields (Item.Fields) that differ from
// the work item's, when changes come in.
func (s *Sync) extraFields(w *plugin.Work, it *Item) map[string]string {
	if !s.pulls() || len(it.Fields) == 0 {
		return nil
	}
	var out map[string]string
	keys := make([]string, 0, len(it.Fields))
	for k := range it.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		key := s.Req.Plugin.ID + "." + k
		if v := it.Fields[k]; w.Fields[key] != v {
			if out == nil {
				out = map[string]string{}
			}
			out[key] = v
		}
	}
	return out
}

func (s *Sync) lostLine(field, side, value string, block bool) string {
	if value == "" {
		value = "(empty)"
	}
	if block && (strings.Contains(value, "\n") || len(value) > 80) {
		return fmt.Sprintf("- %s's %s:\n\n%s\n", side, field, quote(value))
	}
	return fmt.Sprintf("- %s's %s: %q", side, field, value)
}

func quote(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := range lines {
		lines[i] = "> " + lines[i]
	}
	return strings.Join(lines, "\n")
}

func pick[T any](fromOutside bool, o, e T) T {
	if fromOutside {
		return o
	}
	return e
}

func nonNil(x []string) []string {
	if x == nil {
		return []string{}
	}
	return x
}

func snapEqual(a, b Snap) bool {
	return a.Title == b.Title && a.Body == b.Body && a.Stage == b.Stage && a.Priority == b.Priority && slices.Equal(a.Tags, b.Tags) && a.Due == b.Due
}

func hash(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	if s == "" {
		return ""
	}
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:8])
}

func tags(x []string) []string {
	out := []string{}
	for _, t := range x {
		if t = strings.TrimSpace(strings.TrimPrefix(t, "#")); t != "" && !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s *Sync) snapWork(w *plugin.Work) Snap {
	return Snap{Title: strings.TrimSpace(w.Title), Body: hash(w.Body), Stage: w.Stage, Priority: w.Priority, Tags: tags(w.Tags), Due: w.Due}
}

func (s *Sync) snapItem(it Item) Snap {
	return Snap{Title: strings.TrimSpace(cmp.Or(it.Title, it.Ref)), Body: hash(it.Body), Stage: s.stageFor(it), Priority: it.Priority, Tags: tags(it.Labels), Due: it.Due}
}

// itemFor is a work item as a new outside item.
func (s *Sync) itemFor(w *plugin.Work) Item {
	name, kind := s.statusFor(w.Stage)
	return Item{Title: w.Title, Body: w.Body, Status: name, StatusKind: kind, Priority: w.Priority, Labels: w.Tags, Due: w.Due}
}

var priorityNames = []string{"none", "low", "medium", "high", "urgent"}

func priorityName(p int) string {
	if p >= 0 && p < len(priorityNames) {
		return priorityNames[p]
	}
	return fmt.Sprint(p)
}
