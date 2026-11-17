// Command logic is the Versions plugin: where a project's version lives,
// how its releases are numbered, and bumping the version in the code. It is
// built for WASI and runs in Enplace's sandbox.
//
// It reads the code through a linked code plugin (GitHub), which hands
// over the files it asks for and opens the pull requests it asks for (see
// contract.go). Release cycle numbers releases by the rules it publishes.
package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/JustinGuedes/enplace-plugins/plugins/versions/versionfile"
	"github.com/JustinGuedes/enplace-plugins/sdk/plugin"
)

func main() { plugin.Run(handle) }

const (
	keyFound  = "found"
	keyBump   = "bump"
	keyBumped = "bumped"
	keyOwn    = "own"

	keyWant      = "public.want"
	keyPublished = "public.versions"
	keyBumpOut   = "public.bump"
	keyFiles     = "public.files"
	keyBumpsIn   = "public.bumps"
	keyCode      = "public.code"
	keyReleases  = "public.releases"
	keyNumbering = "public.numbering"

	// readFiles is how many of the files found a code plugin reads.
	readFiles = 3

	cardSlot = "project_card:version"

	bumpOnCut   = "When a release is cut"
	bumpOnPress = "When I press Bump"
	buildUp     = "Goes up by one"
)

var styles = map[string]string{"Short (1.4)": versionfile.Short, "Semantic (1.4.0)": versionfile.Semantic, "Calendar (2026.11)": versionfile.Calendar, "Number (14)": versionfile.Number}
var bumps = map[string]string{"Major": versionfile.Major, "Minor": versionfile.Minor, "Patch": versionfile.Patch}

// Found is the version the code says, as last read.
type Found struct {
	Version string `json:"version,omitempty"`
	Build   string `json:"build,omitempty"`
	File    string `json:"file,omitempty"`
	Auto    bool   `json:"auto,omitempty"` // the file was found, not named
	SHA     string `json:"sha,omitempty"`
	// Note says why there's no version.
	Note string `json:"note,omitempty"`
}

type run struct {
	req *plugin.Request
	res *plugin.Response
}

func handle(req *plugin.Request) (*plugin.Response, error) {
	r := &run{req: req, res: &plugin.Response{}}
	switch req.Hook {
	case plugin.HookSchedule:
		for _, p := range req.Projects {
			if p.On {
				r.refresh(p.ID, true)
			}
		}
	case plugin.HookAction:
		if req.Action == nil || req.ProjectID == "" {
			break
		}
		switch req.Action.Name {
		case "bump":
			if !req.ReadOnly {
				r.bumpNow(req.ProjectID)
			}
		case "tool_version":
			r.res.Data = r.tool(req.ProjectID)
		}
	case plugin.HookRender:
		for _, slot := range req.Slots {
			switch slot {
			case cardSlot:
				r.card(req.ProjectID)
			case plugin.SlotSettingsPanel:
				r.setCard(slot, r.panel(req.ProjectID))
			}
		}
	}
	return r.res, nil
}

// ----- Settings -----

// rules is how a project's releases are numbered and its code bumped.
type rules struct {
	Style, Bump, Next string
	BuildUp           bool
	BumpCode          string
	File              string
	// FileFrom says where File came from when it wasn't set here.
	FileFrom string
}

func (r *run) setting(scope, key string) string {
	v, _ := r.req.ProjectSettings[scope][key].(string)
	return strings.TrimSpace(v)
}

func (r *run) rules(scope string) rules {
	ru := rules{
		Style: styles[r.setting(scope, "style")], Bump: bumps[r.setting(scope, "bump")],
		Next:    r.setting(scope, "next_version"),
		BuildUp: r.setting(scope, "build") != "Stays as it is", BumpCode: r.setting(scope, "bump_code"),
		File: strings.Trim(r.setting(scope, "file"), "/ "),
	}
	if ru.Style == "" {
		ru.Style = versionfile.Short
	}
	if ru.Bump == "" {
		ru.Bump = versionfile.Minor
	}
	if ru.BumpCode == "" {
		ru.BumpCode = bumpOnCut
	}
	// Until the numbering is set here, Release cycle's own carries on.
	var own bool
	r.req.State.GetProject(scope, keyOwn, &own)
	if !own && ru.Style == versionfile.Short && ru.Bump == versionfile.Minor && ru.Next == "" {
		var n numbering
		if r.peer(scope, keyNumbering, &n) {
			if n.Style != "" {
				ru.Style = n.Style
			}
			if n.Bump != "" {
				ru.Bump = n.Bump
			}
			ru.Next = n.NextVersion
		}
	}
	if ru.File == "" {
		var c codeInfo
		if r.peer(scope, keyCode, &c) && c.VersionFile != "" {
			ru.File, ru.FileFrom = strings.Trim(c.VersionFile, "/ "), "GitHub's old Version file setting"
		}
	}
	return ru
}

// ownNumbering says whether the numbering was set here, so Release cycle's
// old settings no longer count.
func (r *run) ownNumbering(scope string) bool {
	return (r.setting(scope, "style") != "" && r.setting(scope, "style") != "Short (1.4)") ||
		(r.setting(scope, "bump") != "" && r.setting(scope, "bump") != "Minor") || r.setting(scope, "next_version") != ""
}

// ----- Linked plugins -----

// peer reads what a linked plugin published for the scope, or for its
// project when the scope is a product that has none of its own.
func (r *run) peer(scope, key string, v any) bool {
	scopes := []string{scope}
	if parent, _, ok := strings.Cut(scope, "/"); ok {
		scopes = append(scopes, parent)
	}
	ids := make([]string, 0, len(r.req.Peers))
	for id := range r.req.Peers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, s := range scopes {
		for _, id := range ids {
			if r.req.Peers[id].GetProject(s, key, v) {
				return true
			}
		}
	}
	return false
}

func (r *run) files(scope string) *Files {
	var f *Files
	if !r.peer(scope, keyFiles, &f) {
		return nil
	}
	return f
}

func (f *Files) file(p string) *File {
	if f == nil {
		return nil
	}
	for i := range f.Files {
		if f.Files[i].Path == p {
			return &f.Files[i]
		}
	}
	return nil
}

// nextRelease is the first release still to ship, from Release cycle.
func (r *run) nextRelease(scope string) *release {
	var rels []release
	if !r.peer(scope, keyReleases, &rels) {
		return nil
	}
	var open []release
	for _, x := range rels {
		if x.State != "live" && x.State != "skipped" && !x.Deploy && x.Version != "" {
			open = append(open, x)
		}
	}
	if len(open) == 0 {
		return nil
	}
	sort.SliceStable(open, func(i, j int) bool { return open[i].ShipDate < open[j].ShipDate })
	return &open[0]
}

// cut says whether a release is past its cut-off (or already building).
func (x *release) cut(today string) bool {
	return x.State != "planned" || (x.Cutoff != "" && x.Cutoff <= today)
}

// ----- Reading the version -----

// read finds the version in the files the code plugin shared.
func read(ru rules, fs *Files, hasCode bool) Found {
	switch {
	case fs == nil && hasCode:
		return Found{Note: "Waiting for the code plugin to read the code."}
	case fs == nil:
		return Found{Note: "Switch on GitHub and link the repo, and Versions reads the version from the code."}
	}
	if ru.File != "" {
		f := fs.file(ru.File)
		switch {
		case f == nil:
			return Found{File: ru.File, SHA: fs.SHA, Note: "Waiting for the code plugin to read " + ru.File + "."}
		case f.Missing:
			return Found{File: ru.File, SHA: fs.SHA, Note: "There's no " + ru.File + " in the repo."}
		}
		v, err := versionfile.Read(f.Path, []byte(f.Text))
		if err != nil {
			return Found{File: ru.File, SHA: fs.SHA, Note: "There's no version in " + ru.File + " that Enplace can read."}
		}
		return Found{Version: v, Build: versionfile.Build(f.Path, []byte(f.Text)), File: f.Path, SHA: fs.SHA}
	}
	for _, p := range versionfile.Candidates(fs.Matched) {
		f := fs.file(p)
		if f == nil || f.Missing {
			continue
		}
		if v, err := versionfile.Read(p, []byte(f.Text)); err == nil {
			return Found{Version: v, Build: versionfile.Build(p, []byte(f.Text)), File: p, Auto: true, SHA: fs.SHA}
		}
	}
	return Found{Auto: true, SHA: fs.SHA, Note: "Enplace didn't find a version file. Name it in the settings, like Version.xcconfig."}
}

// refresh reads the version again for a project or product, publishes it
// and what to read next, and bumps the code when a release is cut (auto).
func (r *run) refresh(scope string, auto bool) (Found, rules) {
	ru := r.rules(scope)
	if r.ownNumbering(scope) {
		r.res.SetProject(scope, keyOwn, true)
	}
	want := Want{Patterns: versionfile.Patterns(), Skip: versionfile.Skip, MaxDepth: versionfile.MaxDepth, Read: readFiles,
		Grep: []string{"MARKETING_VERSION", "CURRENT_PROJECT_VERSION", "version"}}
	if ru.File != "" {
		want.Paths = []string{ru.File}
	}
	var c codeInfo
	found := read(ru, r.files(scope), r.peer(scope, keyCode, &c))
	r.res.SetProject(scope, keyWant, want)
	r.res.SetProject(scope, keyFound, found)
	r.res.SetProject(scope, keyPublished, Published{Version: found.Version, Build: found.Build, File: found.File, Style: ru.Style, Bump: ru.Bump, NextVersion: ru.Next})

	b := r.pending(scope)
	// Once the code says the new version, the pull request was merged.
	if b != nil && found.Version != "" && versionfile.Same(found.Version, b.Version) {
		b = nil
	}
	if auto && b == nil && ru.BumpCode == bumpOnCut && found.Version != "" {
		if rel := r.nextRelease(scope); rel != nil && rel.cut(r.req.Date) && versionfile.Newer(rel.Version, found.Version) && !r.bumpedFor(scope, rel.ID) {
			if nb, err := r.newBump(scope, found, ru, rel.Version, rel.ID); err == nil {
				b = nb
				r.res.SetProject(scope, keyBumped, append(r.bumpedList(scope), rel.ID))
			}
		}
	}
	r.res.SetProject(scope, keyBump, b)
	r.res.SetProject(scope, keyBumpOut, b)
	return found, ru
}

// found is what the code says now, from the files the code plugin shared
// last, so cards don't wait for the next schedule.
func (r *run) found(scope string) Found {
	var c codeInfo
	return read(r.rules(scope), r.files(scope), r.peer(scope, keyCode, &c))
}

// ----- Bumping the code -----

func (r *run) pending(scope string) *Bump {
	var b *Bump
	r.req.State.GetProject(scope, keyBump, &b)
	return b
}

// result is how the code plugin got on with a bump, or nil while it hasn't.
func (r *run) result(scope string, b *Bump) *Bumped {
	if b == nil {
		return nil
	}
	var all map[string]Bumped
	if !r.peer(scope, keyBumpsIn, &all) {
		return nil
	}
	if x, ok := all[b.ID]; ok {
		return &x
	}
	return nil
}

func (r *run) bumpedList(scope string) []string {
	var ids []string
	r.req.State.GetProject(scope, keyBumped, &ids)
	if len(ids) > 20 {
		ids = ids[len(ids)-20:]
	}
	return ids
}

func (r *run) bumpedFor(scope, release string) bool {
	for _, id := range r.bumpedList(scope) {
		if id == release {
			return true
		}
	}
	return false
}

// target is what Bump would set: the next release's version when Release
// cycle plans one past the code, else the next by the numbering rules.
func (r *run) target(scope string, f Found, ru rules) (version, build, rel string) {
	version = versionfile.Next(f.Version, ru.Style, ru.Bump, r.req.Date)
	if x := r.nextRelease(scope); x != nil && versionfile.Newer(x.Version, f.Version) {
		version, rel = x.Version, x.ID
	}
	if f.Build != "" && ru.BuildUp {
		build = versionfile.NextBuild(f.Build)
	}
	return version, build, rel
}

func (r *run) newBump(scope string, f Found, ru rules, version, rel string) (*Bump, error) {
	build := ""
	if f.Build != "" && ru.BuildUp {
		build = versionfile.NextBuild(f.Build)
	}
	file := r.files(scope).file(f.File)
	if file == nil || file.Missing {
		return nil, fmt.Errorf("the code plugin hasn't shared %s yet", f.File)
	}
	edits, err := versionfile.Edits(file.Path, []byte(file.Text), version, build)
	if err != nil || len(edits) == 0 {
		return nil, fmt.Errorf("enplace can't change the version in %s", f.File)
	}
	body := fmt.Sprintf("Sets the version to %s in `%s`.", label(version, build), f.File)
	if rel != "" {
		body += " Its release is past its cut-off."
	}
	return &Bump{
		ID: "v" + strconv.FormatInt(r.req.Now.UnixNano(), 36), Title: "Bump the version to " + label(version, build),
		Body: body + "\n\nOpened by Enplace's Versions plugin.", Branch: "enplace/version-" + branchSafe(strings.TrimSuffix(version+"-"+build, "-")),
		Edits: edits, Version: version, Build: build, Release: rel, At: r.req.Now,
	}, nil
}

// bumpNow is the Bump button.
func (r *run) bumpNow(scope string) {
	f, ru := r.refresh(scope, false)
	b := r.pending(scope)
	if res := r.result(scope, b); b != nil && (res == nil || res.State == "opened") && !versionfile.Same(f.Version, b.Version) {
		if res != nil && res.URL != "" {
			r.res.Toast = "Its pull request is open: merge it to bump the version."
		} else {
			r.res.Toast = "The bump to " + label(b.Version, b.Build) + " is on its way."
		}
		return
	}
	if f.Version == "" {
		r.res.Toast = "Enplace can't bump the version until it can read it."
		return
	}
	version, _, rel := r.target(scope, f, ru)
	nb, err := r.newBump(scope, f, ru, version, rel)
	if err != nil {
		r.res.Toast = upper(err.Error()) + "."
		return
	}
	r.res.SetProject(scope, keyBump, nb)
	r.res.SetProject(scope, keyBumpOut, nb)
	r.res.Log = "Asked for a pull request bumping the version to " + label(nb.Version, nb.Build)
	r.res.Toast = "GitHub opens the pull request for " + label(nb.Version, nb.Build) + " on its next check."
}

func label(version, build string) string {
	if build == "" {
		return version
	}
	return version + " (" + build + ")"
}

func branchSafe(s string) string {
	var b strings.Builder
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '.', c == '-':
			b.WriteRune(c)
		}
	}
	return strings.Trim(b.String(), "-")
}

func upper(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// ----- Drawing -----

func (r *run) setCard(slot string, blocks []plugin.Block) {
	if r.res.Cards == nil {
		r.res.Cards = map[string][]plugin.Block{}
	}
	r.res.Cards[slot] = blocks
}

// bumpLine says where a bump is up to, or nil when there's none.
func (r *run) bumpLine(scope string, b *Bump) (*plugin.Block, *plugin.Button) {
	if b == nil {
		return nil, nil
	}
	res := r.result(scope, b)
	switch {
	case res == nil:
		return &plugin.Block{Type: plugin.BlockText, Text: "Bumping to " + label(b.Version, b.Build) + ": GitHub opens the pull request on its next check.", Tone: "muted"}, nil
	case res.State == "failed":
		return &plugin.Block{Type: plugin.BlockText, Text: "Couldn't bump to " + label(b.Version, b.Build) + ": " + res.Error, Tone: "warn"}, nil
	default:
		t := "Bump to " + label(b.Version, b.Build) + " is waiting for you to merge"
		if res.Number > 0 {
			t += fmt.Sprintf(" #%d", res.Number)
		}
		return &plugin.Block{Type: plugin.BlockText, Text: t + ".", Tone: "good"}, &plugin.Button{Label: "Open pull request", URL: res.URL}
	}
}

func (r *run) card(scope string) {
	f, ru := r.found(scope), r.rules(scope)
	stat := plugin.Block{Type: plugin.BlockStat, Label: "In the code", Value: f.Version, Sub: where(f)}
	if f.Version == "" {
		stat.Value, stat.Tone, stat.Sub = "No version", "muted", f.Note
		if stat.Sub == "" {
			stat.Sub = "Versions reads it within a few minutes."
		}
	}
	blocks := []plugin.Block{stat}
	var btns []plugin.Button
	b := r.pending(scope)
	if line, open := r.bumpLine(scope, b); line != nil {
		blocks = append(blocks, *line)
		if open != nil {
			btns = append(btns, *open)
		}
	}
	if f.Version != "" {
		version, build, rel := r.target(scope, f, ru)
		if rel != "" && (b == nil || !versionfile.Same(b.Version, version)) {
			blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: "Next release: " + label(version, build), Tone: "muted"})
		}
		res := r.result(scope, b)
		if ru.BumpCode != "Never" && (b == nil || (res != nil && res.State == "failed")) {
			btns = append(btns, plugin.Button{Label: "Bump to " + label(version, build), Action: "bump",
				Confirm: "Open a pull request that sets the version to " + label(version, build) + "?"})
		}
	}
	if len(btns) > 0 {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockButtons, Buttons: btns})
	}
	r.setCard(cardSlot, blocks)
	if f.Version != "" {
		r.res.Say(cardSlot, "The code says "+label(f.Version, f.Build)+".", f.Version)
	}
	level := plugin.LevelQuiet
	if res := r.result(scope, b); res != nil && res.State == "failed" {
		level = plugin.LevelNormal
	}
	r.res.Signal(cardSlot, level, "")
}

func where(f Found) string {
	var parts []string
	if f.Build != "" {
		parts = append(parts, "build "+f.Build)
	}
	if f.File != "" {
		parts = append(parts, f.File)
	}
	return strings.Join(parts, " · ")
}

func (r *run) panel(scope string) []plugin.Block {
	f, ru := r.found(scope), r.rules(scope)
	var blocks []plugin.Block
	switch {
	case f.Version != "" && f.Auto:
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: fmt.Sprintf("Reading %s from %s (%s), found on its own. Name another file below to use that instead.", label(f.Version, f.Build), f.File, versionfile.Kind(f.File))})
	case f.Version != "":
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: fmt.Sprintf("Reading %s from %s.", label(f.Version, f.Build), f.File)})
	case f.Note != "":
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: f.Note, Tone: "muted"})
	}
	if ru.FileFrom != "" {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: "The file comes from " + ru.FileFrom + ". Set it here to change it.", Tone: "muted"})
	}
	var own bool
	r.req.State.GetProject(scope, keyOwn, &own)
	var n numbering
	if !own && !r.ownNumbering(scope) && r.peer(scope, keyNumbering, &n) {
		blocks = append(blocks, plugin.Block{Type: plugin.BlockText, Text: "Releases are numbered the way Release cycle numbered them until you set the numbering here.", Tone: "muted"})
	}
	return blocks
}

// ----- Agents -----

func (r *run) tool(scope string) map[string]any {
	f, ru := r.found(scope), r.rules(scope)
	out := map[string]any{"version": f.Version, "build": f.Build, "file": f.File, "file_found_automatically": f.Auto,
		"numbering": map[string]any{"style": ru.Style, "bump": ru.Bump, "next_version": ru.Next, "build_goes_up": ru.BuildUp},
		"bump_code": ru.BumpCode}
	if f.Note != "" {
		out["note"] = f.Note
	}
	if f.Version != "" {
		v, b, rel := r.target(scope, f, ru)
		out["next"] = map[string]any{"version": v, "build": b, "from_release": rel != ""}
	}
	if b := r.pending(scope); b != nil {
		pr := map[string]any{"version": b.Version, "build": b.Build, "asked_at": b.At}
		if res := r.result(scope, b); res != nil {
			raw, _ := json.Marshal(res)
			var m map[string]any
			_ = json.Unmarshal(raw, &m)
			pr["result"] = m
		}
		out["bump"] = pr
	}
	return out
}
