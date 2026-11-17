package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"
)

// The Versions plugin works out the version; this plugin hands it the
// files it asks for and opens the pull requests it asks for. Versions
// publishes on the project, or on each of its products:
//
//	"public.want": {"patterns":["project.yml","*.xcconfig",…],"skip":["node_modules",…],
//	                "max_depth":4,"read":3,"paths":["Version.xcconfig"],"grep":["MARKETING_VERSION",…]}
//	"public.bump": {"id":"…","title":"…","body":"…","branch":"enplace/version-1.5",
//	                "edits":[{"path":"Version.xcconfig","old":"MARKETING_VERSION = 1.4","new":"MARKETING_VERSION = 1.5","all":true}]}
//
// and this plugin answers, on the project:
//
//	"public.files": {"sha":"…","matched":["Version.xcconfig",…],"files":[{"path":"Version.xcconfig","text":"…"}]}
//	"public.bumps": {"<bump id>":{"state":"opened","number":12,"url":"…"}}

const (
	keyFiles     = "files"
	keyBumps     = "bumps"
	keyFilesOut  = "public.files"
	keyBumpsOut  = "public.bumps"
	keyWant      = "public.want"
	keyBumpAsk   = "public.bump"
	keyVersions  = "public.versions"
	maxShared    = 32 << 10
	maxMatched   = 50
	maxReadFiles = 8
	keepBumps    = 20
)

type want struct {
	Patterns []string `json:"patterns"`
	Skip     []string `json:"skip"`
	MaxDepth int      `json:"max_depth"`
	Read     int      `json:"read"`
	Paths    []string `json:"paths,omitempty"`
	Grep     []string `json:"grep"`
}

type sharedFile struct {
	Path    string `json:"path"`
	Text    string `json:"text,omitempty"`
	Partial bool   `json:"partial,omitempty"`
	Missing bool   `json:"missing,omitempty"`
}

// Shared is what this plugin read for Versions at commit SHA.
type Shared struct {
	SHA     string       `json:"sha"`
	Matched []string     `json:"matched"`
	Files   []sharedFile `json:"files"`
	At      time.Time    `json:"at"`
	// Want is what was asked for, so a new ask is read at once.
	Want string `json:"want"`
}

type edit struct {
	Path string `json:"path"`
	Old  string `json:"old"`
	New  string `json:"new"`
	All  bool   `json:"all,omitempty"`
}

type bumpAsk struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Branch string `json:"branch"`
	Edits  []edit `json:"edits"`
}

// Bumped is how a bump Versions asked for went.
type Bumped struct {
	State  string    `json:"state"` // "opened" or "failed"
	URL    string    `json:"url,omitempty"`
	Number int       `json:"number,omitempty"`
	Error  string    `json:"error,omitempty"`
	At     time.Time `json:"at"`
}

// fromVersions is what Versions published for this project and its
// products, by key.
func (r *run) fromVersions(key string, each func(raw json.RawMessage)) {
	ids := make([]string, 0, len(r.req.Peers))
	for id := range r.req.Peers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		st := r.req.Peers[id]
		scopes := make([]string, 0, len(st.Projects))
		for s := range st.Projects {
			if s == r.pid || strings.HasPrefix(s, r.pid+"/") {
				scopes = append(scopes, s)
			}
		}
		sort.Strings(scopes)
		for _, s := range scopes {
			if raw, ok := st.Projects[s][key]; ok && string(raw) != "null" {
				each(raw)
			}
		}
	}
}

// wanted is every ask for files put together, or nil.
func (r *run) wanted() *want {
	var all *want
	r.fromVersions(keyWant, func(raw json.RawMessage) {
		var w want
		if json.Unmarshal(raw, &w) != nil {
			return
		}
		if all == nil {
			all = &w
			return
		}
		all.Read = max(all.Read, w.Read)
		all.MaxDepth = max(all.MaxDepth, w.MaxDepth)
		all.Paths = union(all.Paths, w.Paths)
		all.Grep = union(all.Grep, w.Grep)
		all.Skip = union(all.Skip, w.Skip)
	})
	if all != nil {
		all.Read = min(all.Read, maxReadFiles)
	}
	return all
}

func union(a, b []string) []string {
	for _, s := range b {
		if !contains(a, s) {
			a = append(a, s)
		}
	}
	return a
}

// matches are the paths whose names match the patterns, best first: the
// pattern they match, then how near the root they are.
func (w *want) matches(paths []string) []string {
	type m struct {
		p     string
		score int
	}
	var out []m
	for _, p := range paths {
		parts := strings.Split(p, "/")
		if w.MaxDepth > 0 && len(parts) > w.MaxDepth || w.skipped(parts[:len(parts)-1]) {
			continue
		}
		b := strings.ToLower(parts[len(parts)-1])
		for i, pat := range w.Patterns {
			if ok, _ := path.Match(strings.ToLower(pat), b); ok {
				out = append(out, m{p, i + 4*(len(parts)-1)})
				break
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score < out[j].score
		}
		return out[i].p < out[j].p
	})
	ps := make([]string, 0, len(out))
	for i := range out {
		if i < maxMatched {
			ps = append(ps, out[i].p)
		}
	}
	return ps
}

func (w *want) skipped(dirs []string) bool {
	for _, d := range dirs {
		if strings.HasPrefix(d, ".") {
			return true
		}
		for _, s := range w.Skip {
			if strings.EqualFold(d, s) {
				return true
			}
		}
	}
	return false
}

// shared is what was read for Versions last time.
func (r *run) shared() *Shared {
	var s *Shared
	r.req.State.GetProject(r.pid, keyFiles, &s)
	return s
}

// share reads the files Versions asks for at the head commit, when the
// branch moved or the ask changed.
func (r *run) share(gh *client, c *Code, force bool) error {
	w := r.wanted()
	if w == nil {
		r.res.SetProject(r.pid, keyFiles, nil)
		r.res.SetProject(r.pid, keyFilesOut, nil)
		return nil
	}
	raw, _ := json.Marshal(w)
	old := r.shared()
	if !force && old != nil && old.SHA == c.SHA && old.Want == string(raw) {
		return nil
	}
	paths, err := gh.files(c.SHA)
	if err != nil {
		return err
	}
	s := &Shared{SHA: c.SHA, Matched: w.matches(paths), At: r.req.Now, Want: string(raw)}
	read := append([]string{}, w.Paths...)
	for _, p := range s.Matched {
		if len(read) >= w.Read+len(w.Paths) {
			break
		}
		if !contains(read, p) {
			read = append(read, p)
		}
	}
	for _, p := range read {
		data, err := gh.raw(p, c.SHA)
		switch {
		case notFound(err):
			s.Files = append(s.Files, sharedFile{Path: p, Missing: true})
		case err != nil:
			return err
		default:
			s.Files = append(s.Files, excerpt(p, data, w.Grep))
		}
	}
	r.res.SetProject(r.pid, keyFiles, s)
	r.res.SetProject(r.pid, keyFilesOut, s)
	return nil
}

// excerpt is a file as shared: whole, or for a big one just the lines
// that say one of grep.
func excerpt(p string, data []byte, grep []string) sharedFile {
	if len(data) <= maxShared {
		return sharedFile{Path: p, Text: string(data)}
	}
	var keep []string
	for _, line := range strings.Split(string(data), "\n") {
		l := strings.ToLower(line)
		for _, g := range grep {
			if strings.Contains(l, strings.ToLower(g)) {
				keep = append(keep, line)
				break
			}
		}
	}
	text := strings.Join(keep, "\n")
	if len(text) > maxShared {
		text = text[:maxShared]
	}
	return sharedFile{Path: p, Text: text, Partial: true}
}

// raw reads one file at a commit, as GitHub has it.
func (c *client) raw(p, ref string) ([]byte, error) {
	return c.get("/repos/"+c.repo+"/contents/"+escapePath(p)+"?ref="+url.QueryEscape(ref), "application/vnd.github.raw+json")
}

func escapePath(p string) string {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

// ----- Bumps -----

func (r *run) bumpsDone() map[string]Bumped {
	done := map[string]Bumped{}
	r.req.State.GetProject(r.pid, keyBumps, &done)
	return done
}

// bump opens the pull requests Versions asked for and hasn't had yet.
func (r *run) bump(gh *client, c *Code) {
	done := r.bumpsDone()
	changed := false
	r.fromVersions(keyBumpAsk, func(raw json.RawMessage) {
		var b bumpAsk
		if json.Unmarshal(raw, &b) != nil || b.ID == "" {
			return
		}
		if _, ok := done[b.ID]; ok {
			return
		}
		res := Bumped{At: r.req.Now}
		if n, link, err := r.openBump(gh, c, b); err != nil {
			res.State, res.Error = "failed", sentence(err.Error())
		} else {
			res.State, res.Number, res.URL = "opened", n, link
		}
		done[b.ID] = res
		changed = true
	})
	if len(done) > keepBumps {
		ids := make([]string, 0, len(done))
		for id := range done {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return done[ids[i]].At.After(done[ids[j]].At) })
		for _, id := range ids[keepBumps:] {
			delete(done, id)
		}
	}
	if changed {
		r.res.SetProject(r.pid, keyBumps, done)
	}
	r.res.SetProject(r.pid, keyBumpsOut, done)
}

var errNoWrite = errors.New("the GitHub token can't write to this repo. Give it Contents and Pull requests: read and write")

// openBump makes a branch off the head commit, edits the files on it and
// opens a pull request into the project's branch.
func (r *run) openBump(gh *client, c *Code, b bumpAsk) (int, string, error) {
	if gh.token == "" {
		return 0, "", errors.New("add a GitHub token with Contents and Pull requests: read and write to bump the version")
	}
	if c.SHA == "" || len(b.Edits) == 0 {
		return 0, "", errors.New("there was nothing to change")
	}
	branch := b.Branch
	_, err := gh.send("POST", "/repos/"+gh.repo+"/git/refs", map[string]string{"ref": "refs/heads/" + branch, "sha": c.SHA})
	if status(err) == 422 { // the branch is there from before
		branch = b.Branch + "-" + b.ID
		_, err = gh.send("POST", "/repos/"+gh.repo+"/git/refs", map[string]string{"ref": "refs/heads/" + branch, "sha": c.SHA})
	}
	if err != nil {
		return 0, "", writeErr(err)
	}
	var files []string
	for _, e := range b.Edits {
		if !contains(files, e.Path) {
			files = append(files, e.Path)
		}
	}
	for _, p := range files {
		var cur struct {
			SHA     string `json:"sha"`
			Content string `json:"content"`
		}
		if err := gh.getJSON("/repos/"+gh.repo+"/contents/"+escapePath(p)+"?ref="+url.QueryEscape(branch), &cur); err != nil {
			return 0, "", err
		}
		data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(cur.Content, "\n", ""))
		if err != nil {
			return 0, "", fmt.Errorf("couldn't read %s", p)
		}
		text, ok := applyEdits(string(data), p, b.Edits)
		if !ok {
			return 0, "", fmt.Errorf("%s changed since Versions read it. Bump again to try with what it says now", p)
		}
		_, err = gh.send("PUT", "/repos/"+gh.repo+"/contents/"+escapePath(p), map[string]string{
			"message": b.Title, "content": base64.StdEncoding.EncodeToString([]byte(text)), "sha": cur.SHA, "branch": branch,
		})
		if err != nil {
			return 0, "", writeErr(err)
		}
	}
	out, err := gh.send("POST", "/repos/"+gh.repo+"/pulls", map[string]string{"title": b.Title, "body": b.Body, "head": branch, "base": c.Branch})
	if err != nil {
		return 0, "", writeErr(err)
	}
	var pr struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
	}
	_ = json.Unmarshal(out, &pr)
	return pr.Number, pr.HTMLURL, nil
}

// applyEdits makes a file's edits; false when one no longer fits.
func applyEdits(text, p string, edits []edit) (string, bool) {
	for _, e := range edits {
		if e.Path != p {
			continue
		}
		if !strings.Contains(text, e.Old) {
			return text, false
		}
		if e.All {
			text = strings.ReplaceAll(text, e.Old, e.New)
		} else {
			text = strings.Replace(text, e.Old, e.New, 1)
		}
	}
	return text, true
}

func status(err error) int {
	var ae *apiError
	if errors.As(err, &ae) {
		return ae.Status
	}
	return 0
}

func writeErr(err error) error {
	if s := status(err); s == 403 || s == 404 {
		return errNoWrite
	}
	return err
}

func sentence(s string) string {
	s = upper(s)
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}

// version is what Versions read from the code, for this project's card.
type versionInfo struct {
	Version string `json:"version"`
	Build   string `json:"build"`
	File    string `json:"file"`
}

func (r *run) version() *versionInfo {
	var v *versionInfo
	r.fromVersions(keyVersions, func(raw json.RawMessage) {
		var x versionInfo
		if v == nil && json.Unmarshal(raw, &x) == nil && x.Version != "" {
			v = &x
		}
	})
	return v
}
