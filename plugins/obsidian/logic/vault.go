package main

// The vault as Enplace knows it: one stored copy of each work note, by
// its path inside the project's Work folder. Obsidian sends the notes
// that changed; the sync compares them with the work; what changed here
// is written into the copy with a new revision number, and Obsidian takes
// every copy newer than the last one it wrote.

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin/worksync"
)

const (
	keyNote = "note." // + a hash of the path
	keyRev  = "vault.rev"
	keySeen = "vault.seen"
	// A note bigger than this stays out of the sync.
	maxNote = 200 << 10
)

// copyOf is one note as Enplace last knows it.
type copyOf struct {
	Path string `json:"p"`
	Text string `json:"x"`
	// Rev is the revision Enplace last wrote it at, for Obsidian to take.
	Rev int `json:"r"`
	// Mod is when the vault last changed it.
	Mod time.Time `json:"m"`
	// Gone says it was deleted in the vault.
	Gone bool `json:"g,omitempty"`
}

// seen is the last exchange with Obsidian.
type seen struct {
	At     time.Time `json:"at"`
	Client string    `json:"client,omitempty"`
	Since  int       `json:"since"`
}

// vault is one project's notes, loaded from state, and the Source the sync
// works with.
type vault struct {
	pid   string
	res   *plugin.Response
	notes map[string]*copyOf // by path
	rev   int
	dirty map[string]bool
	// since is the revision the calling app already has (writes it has
	// taken, so the full list it sends can tell deletes from new notes).
	since int
	// dir is the person's own folder of notes, when the project uses one
	// ("use an existing folder"); empty when Enplace keeps the notes in
	// its own folder. In their folder Enplace never adds a note or a key,
	// and the note's own property names and values are kept.
	dir string
	tz  string
}

func loadVault(req *plugin.Request, res *plugin.Response, pid string) *vault {
	v := &vault{pid: pid, res: res, notes: map[string]*copyOf{}, dirty: map[string]bool{}, dir: ownFolder(req, pid), tz: req.Timezone}
	for k, raw := range req.State.Projects[pid] {
		if !strings.HasPrefix(k, keyNote) {
			continue
		}
		var c copyOf
		if req.State.GetProject(pid, k, &c) && c.Path != "" {
			v.notes[c.Path] = &c
		}
		_ = raw
	}
	req.State.GetProject(pid, keyRev, &v.rev)
	return v
}

func noteKey(p string) string {
	h := sha256.Sum256([]byte(p))
	return keyNote + hex.EncodeToString(h[:10])
}

func (v *vault) put(c *copyOf) {
	v.notes[c.Path] = c
	v.dirty[c.Path] = true
}

func (v *vault) drop(p string) {
	delete(v.notes, p)
	v.dirty[p] = true
}

// bump gives a note Enplace changed a new revision, for Obsidian to take.
func (v *vault) bump(c *copyOf) {
	v.rev++
	c.Rev = v.rev
	v.put(c)
}

func (v *vault) save() {
	for p := range v.dirty {
		if c := v.notes[p]; c != nil {
			v.res.SetProject(v.pid, noteKey(p), c)
		} else {
			v.res.SetProject(v.pid, noteKey(p), nil)
		}
	}
	if len(v.dirty) > 0 {
		v.res.SetProject(v.pid, keyRev, v.rev)
	}
}

func stem(p string) string { return strings.TrimSuffix(path.Base(p), ".md") }

// ownFolder is the project's "folder" setting as a clean vault path, or
// "" when it has none (or one that isn't a plain folder in the vault).
func ownFolder(req *plugin.Request, pid string) string {
	raw, _ := req.ProjectSettings[pid]["folder"].(string)
	p := strings.Trim(strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/"), "/")
	if p == "" || len(p) > 200 || strings.ContainsRune(p, 0) {
		return ""
	}
	c := path.Clean(p)
	for _, part := range strings.Split(c, "/") {
		if part == ".." || strings.HasPrefix(part, ".") {
			return ""
		}
	}
	return c
}

// stageProp is the property a note keeps its stage in: "stage", or
// "status" as many vaults call it.
func (v *vault) stageProp(n *note) string {
	switch {
	case n.find("stage") != nil:
		return "stage"
	case n.find("status") != nil || v.dir != "":
		return "status"
	}
	return "stage"
}

// ref is what a note is called: its key, its own id, or its file name.
func (v *vault) ref(n *note, p string) string {
	if v.dir != "" {
		return cmp.Or(n.value("id"), n.value("enplace_id"), stem(p))
	}
	return stem(p)
}

// item is a note in Enplace's terms.
func (v *vault) item(s *worksync.Sync, c *copyOf) worksync.Item {
	n := parseNote(c.Text)
	it := worksync.Item{ID: c.Path, Ref: v.ref(n, c.Path), Title: cmp.Or(n.heading, stem(c.Path)), Body: n.body,
		Priority: n.priority(), Labels: n.list("tags"), Due: n.due(), UpdatedAt: c.Mod, Deleted: c.Gone}
	if l := s.Links()[c.Path]; l != nil && l.Ref != "" {
		it.Ref = l.Ref
	}
	raw := n.value(v.stageProp(n))
	it.Status = raw
	if st := stageKey(s, raw); st != "" && !s.Mapped(raw) {
		it.Stage, it.StatusKind = st, s.StageKind(st)
	}
	return it
}

// stageKey is the project's stage a note names, by key or name.
func stageKey(s *worksync.Sync, raw string) string {
	if raw == "" {
		return ""
	}
	for _, p := range s.Req.Projects {
		if p.ID != s.ProjectID {
			continue
		}
		for _, st := range p.Stages {
			if strings.EqualFold(st.Key, raw) || strings.EqualFold(st.Name, raw) {
				return st.Key
			}
		}
	}
	return ""
}

func workByID(req *plugin.Request, id string) *plugin.Work {
	for i := range req.Work {
		if req.Work[i].ID == id {
			return &req.Work[i]
		}
	}
	return nil
}

func workByKey(req *plugin.Request, pid, key string) *plugin.Work {
	for i := range req.Work {
		if req.Work[i].ProjectID == pid && strings.EqualFold(req.Work[i].Key, key) {
			return &req.Work[i]
		}
	}
	return nil
}

// List is every note, after writing each linked one's key into it when
// it doesn't have it yet (a new note, once its work item exists).
func (v *vault) List(s *worksync.Sync) ([]worksync.Item, bool, error) {
	paths := make([]string, 0, len(v.notes))
	for p := range v.notes {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	v.stamp(s)
	items := make([]worksync.Item, 0, len(paths))
	for _, p := range paths {
		items = append(items, v.item(s, v.notes[p]))
	}
	// Not a full list: deletes come marked, from the app.
	return items, false, nil
}

// stamp writes each linked note's key into it when it doesn't have it
// yet: a new note, once its work item exists.
func (v *vault) stamp(s *worksync.Sync) {
	if v.dir != "" {
		return // the person's own notes keep their own ids
	}
	for p, c := range v.notes {
		l := s.Links()[p]
		if l == nil || l.State != worksync.Linked || c.Gone {
			continue
		}
		if w := workByID(s.Req, l.Work); w != nil {
			n := parseNote(c.Text)
			if !strings.EqualFold(n.value("enplace_id"), w.Key) || n.find("enplace_id") == nil {
				n.set("enplace_id", w.Key, false)
				c.Text = n.String()
				v.bump(c)
			}
		}
	}
}

// Update writes what changed in Enplace into the note.
func (v *vault) Update(s *worksync.Sync, it worksync.Item, ch worksync.Change) (worksync.Item, error) {
	c := v.notes[it.ID]
	if c == nil || c.Gone {
		return it, nil
	}
	n := parseNote(c.Text)
	if ch.Title != nil {
		n.heading, n.hasHeading = strings.TrimSpace(*ch.Title), true
	}
	if ch.Body != nil {
		n.body = strings.Trim(strings.ReplaceAll(*ch.Body, "\r\n", "\n"), "\n")
	}
	keep := v.dir != "" // the person's notes keep their keys, empty
	if ch.StatusKind != nil || ch.Status != nil {
		if st := v.stageFor(s, it, ch); st != "" {
			if v.dir != "" {
				// Their own status: the mapped one, or the stage's name.
				st = s.StageName(st)
				if ch.Status != nil && *ch.Status != "" {
					st = *ch.Status
				}
			}
			n.set(v.stageProp(n), st, false)
		}
	}
	if ch.Priority != nil {
		n.set("priority", n.priorityValue(*ch.Priority), keep)
	}
	if ch.Labels != nil {
		var tags any
		if len(*ch.Labels) > 0 || keep {
			tags = *ch.Labels
		}
		n.set("tags", tags, keep)
	}
	if ch.Due != nil {
		var due any
		if *ch.Due != "" {
			due = *ch.Due
		}
		n.set("due", due, keep)
	}
	if text := n.String(); text != c.Text {
		if v.dir != "" && n.find("updated") != nil {
			n.set("updated", v.today(s), false)
			text = n.String()
		}
		c.Text = text
		v.bump(c)
	}
	return v.item(s, c), nil
}

// today is the date in the person's time zone.
func (v *vault) today(s *worksync.Sync) string {
	loc, err := time.LoadLocation(v.tz)
	if err != nil {
		loc = time.UTC
	}
	return s.Req.Now.In(loc).Format("2006-01-02")
}

// stageFor is the stage key to write: the linked work's own stage.
func (v *vault) stageFor(s *worksync.Sync, it worksync.Item, ch worksync.Change) string {
	if l := s.Links()[it.ID]; l != nil {
		if w := workByID(s.Req, l.Work); w != nil {
			return w.Stage
		}
	}
	if ch.Status != nil && stageKey(s, *ch.Status) != "" {
		return stageKey(s, *ch.Status)
	}
	if ch.StatusKind != nil {
		for _, p := range s.Req.Projects {
			if p.ID == s.ProjectID {
				for _, st := range p.Stages {
					if st.Kind == *ch.StatusKind {
						return st.Key
					}
				}
			}
		}
	}
	return ""
}

// Create writes a note for work that has none.
func (v *vault) Create(s *worksync.Sync, w plugin.Work, _ worksync.Item) (worksync.Item, error) {
	n := &note{hasFront: true, heading: strings.TrimSpace(cmp.Or(w.Title, w.Key)), hasHeading: true,
		body: strings.Trim(strings.ReplaceAll(w.Body, "\r\n", "\n"), "\n")}
	n.set("enplace_id", w.Key, false)
	n.set("stage", w.Stage, false)
	if w.Priority > 0 {
		n.set("priority", w.Priority, false)
	}
	if len(w.Tags) > 0 {
		n.set("tags", w.Tags, false)
	}
	if w.Due != "" {
		n.set("due", w.Due, false)
	}
	p := v.freePath(fileName(w.Key, w.Title))
	c := &copyOf{Path: p, Text: n.String(), Mod: s.Req.Now}
	v.bump(c)
	it := v.item(s, c)
	it.Ref = w.Key
	return it, nil
}

var unsafeName = regexp.MustCompile(`[\\/:*?"<>|#^\[\]\x00-\x1f]+`)

// fileName is "ENP-42 Short title.md", safe on every system.
func fileName(key, title string) string {
	t := strings.Join(strings.Fields(unsafeName.ReplaceAllString(title, " ")), " ")
	t = strings.Trim(t, ". ")
	for len(t) > 60 {
		_, size := utf8.DecodeLastRuneInString(t)
		t = t[:len(t)-size]
	}
	t = strings.TrimSpace(t)
	if t == "" {
		return key + ".md"
	}
	return key + " " + t + ".md"
}

func (v *vault) freePath(p string) string {
	if _, taken := v.notes[p]; !taken {
		return p
	}
	base := strings.TrimSuffix(p, ".md")
	for i := 2; ; i++ {
		q := base + " " + itoa(i) + ".md"
		if _, taken := v.notes[q]; !taken {
			return q
		}
	}
}

func itoa(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return itoa(i/10) + string(rune('0'+i%10))
}

// cleanPath checks a path the app sent: inside the folder, a Markdown file.
func cleanPath(p string) (string, bool) {
	p = strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")
	if p == "" || len(p) > 240 || !strings.HasSuffix(strings.ToLower(p), ".md") || strings.HasPrefix(p, "/") || strings.ContainsRune(p, 0) {
		return "", false
	}
	c := path.Clean(p)
	if c != p || c == ".." || strings.HasPrefix(c, "../") || strings.HasPrefix(c, ".") {
		return "", false
	}
	return c, true
}
