package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/JustinGuedes/enplace-plugins/sdk/bundle"
)

// UpstreamFile sits beside a skill pack's manifest and says which of its
// upstream repo's skills to copy in. It stays out of the bundle.
const UpstreamFile = "upstream.json"

// upstreamRules is upstream.json.
type upstreamRules struct {
	// Skills are the repo's skill folders to take, as path patterns
	// ("skills/engineering/*"); "." is a repo that is one skill.
	Skills []string `json:"skills"`
	// Except are skill names to leave out.
	Except []string `json:"except,omitempty"`
}

// Licences whose skills may be copied into a pack and shared: they only
// ask that the licence and the author's name go with them.
var shareableLicences = []string{"MIT", "Apache-2.0", "BSD-2-Clause", "BSD-3-Clause", "ISC", "0BSD", "CC0-1.0", "CC-BY-4.0", "Unlicense"}

// Files in a skill's folder that are about the repo, not the skill.
var notSkillFiles = []string{"README.md", "CHANGELOG.md", "CONTRIBUTING.md", "AGENTS.md", "CLAUDE.md", "package.json", "package-lock.json"}

// Folders in a skill's folder that other tools use, not agents.
var notSkillDirs = []string{"agents", "evals", "tests", "node_modules"}

var licenceFiles = []string{"LICENSE", "LICENSE.md", "LICENSE.txt", "COPYING"}

// vendorCmd copies a skill pack's skills in from its upstream repo, at the
// commit the manifest pins or the repo's newest, with the repo's licence.
// Only words come over: scripts and other files agents could run are left
// out, and so said.
func vendorCmd(args []string) error {
	fset := flag.NewFlagSet("vendor", flag.ContinueOnError)
	latest := fset.Bool("latest", false, "take the repo's newest commit instead of the pinned one")
	bump := fset.Bool("bump", false, "bump the patch version when the skills change")
	from := fset.String("from", "", "read the repo from this checkout instead of fetching it")
	summary := fset.String("summary", "", "write what changed, as Markdown, to this file")
	var dir string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		dir, args = args[0], args[1:]
	}
	if err := fset.Parse(args); err != nil {
		return err
	}
	if dir == "" {
		return errors.New("usage: enplace-plugin vendor DIR [--latest] [--bump] [--summary FILE]")
	}
	res, err := vendor(dir, vendorOptions{Latest: *latest, Bump: *bump, From: *from})
	if err != nil {
		return err
	}
	fmt.Print(res.String())
	if *summary != "" && res.Changed() {
		return os.WriteFile(*summary, []byte(res.Markdown()), 0o644)
	}
	return nil
}

type vendorOptions struct {
	Latest, Bump bool
	From         string // a checkout to read instead of fetching
}

// vendorResult is what a vendor run changed.
type vendorResult struct {
	ID, Name, Repo      string
	From, To            string // commits
	Version             string // after any bump
	Added, Edited, Gone []string
	Dropped             []string // files left out, by repo path
	LicenceChanged      bool
}

func (r vendorResult) Changed() bool {
	return len(r.Added)+len(r.Edited)+len(r.Gone) > 0 || r.LicenceChanged
}

func (r vendorResult) String() string {
	if !r.Changed() {
		return fmt.Sprintf("%s: no change at %s\n", r.ID, short(r.To))
	}
	return fmt.Sprintf("%s: %s → %s, %d added, %d changed, %d gone, version %s\n",
		r.ID, short(r.From), short(r.To), len(r.Added), len(r.Edited), len(r.Gone), r.Version)
}

// Markdown says what changed for a pull request: what a person reads before
// these words reach anyone's agents.
func (r vendorResult) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s changed upstream: [%s…%s](%s/compare/%s...%s). Read the diff before merging: these skills are instructions agents follow.\n\n",
		r.Name, short(r.From), short(r.To), r.Repo, r.From, r.To)
	list := func(title string, names []string) {
		if len(names) > 0 {
			fmt.Fprintf(&b, "- %s: %s\n", title, strings.Join(names, ", "))
		}
	}
	list("New skills", r.Added)
	list("Changed", r.Edited)
	list("Gone", r.Gone)
	if r.LicenceChanged {
		b.WriteString("- **The licence changed.** Check it still lets us share these skills.\n")
	}
	list("Left out (not words)", r.Dropped)
	fmt.Fprintf(&b, "\nVersion %s.\n", r.Version)
	return b.String()
}

func short(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}

func vendor(dir string, opt vendorOptions) (vendorResult, error) {
	var res vendorResult
	rawManifest, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return res, err
	}
	m, err := bundle.ParseManifest(rawManifest)
	if err != nil {
		return res, err
	}
	if m.Upstream == nil {
		return res, fmt.Errorf("%s: the manifest names no upstream repo", dir)
	}
	if !slices.Contains(shareableLicences, m.License) {
		return res, fmt.Errorf("%s: %q isn't a licence that lets us share copies (%s)", dir, m.License, strings.Join(shareableLicences, ", "))
	}
	rawRules, err := os.ReadFile(filepath.Join(dir, UpstreamFile))
	if err != nil {
		return res, err
	}
	var rules upstreamRules
	dec := json.NewDecoder(bytes.NewReader(rawRules))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rules); err != nil || len(rules.Skills) == 0 {
		return res, fmt.Errorf("%s: name the skills to take, like {\"skills\": [\"skills/*\"]}", filepath.Join(dir, UpstreamFile))
	}
	res.ID, res.Name, res.Repo, res.From, res.Version = m.ID, m.Name, m.Upstream.Repo, m.Upstream.Commit, m.Version

	repo, commit := opt.From, ""
	if repo == "" {
		ref := m.Upstream.Commit
		if opt.Latest {
			ref = "HEAD"
		}
		tmp, err := os.MkdirTemp("", "enplace-vendor-")
		if err != nil {
			return res, err
		}
		defer os.RemoveAll(tmp)
		if err := fetch(tmp, m.Upstream.Repo, ref); err != nil {
			return res, err
		}
		repo = tmp
	}
	if commit, err = git(repo, "rev-parse", "HEAD"); err != nil {
		return res, err
	}
	res.To = commit

	skills, dropped, err := readUpstream(repo, rules)
	if err != nil {
		return res, err
	}
	res.Dropped = dropped
	licence, err := readLicence(repo)
	if err != nil {
		return res, err
	}

	old, err := readTree(filepath.Join(dir, "skills"))
	if err != nil {
		return res, err
	}
	res.Added, res.Edited, res.Gone = diffSkills(old, skills)
	if was, _ := os.ReadFile(filepath.Join(dir, "LICENSE")); !bytes.Equal(was, licence) {
		res.LicenceChanged = true
	}
	if !res.Changed() && commit == m.Upstream.Commit {
		return res, nil
	}

	// Check it packs before touching the folder.
	files := map[string][]byte{"manifest.json": rawManifest, "LICENSE": licence}
	for name, fs := range skills {
		for p, b := range fs {
			files["skills/"+name+"/"+p] = b
		}
	}
	if _, err := bundle.Pack(files, nil); err != nil {
		return res, fmt.Errorf("the copied skills don't make a pack: %w", err)
	}

	if err := os.RemoveAll(filepath.Join(dir, "skills")); err != nil {
		return res, err
	}
	for p, b := range files {
		if p == "manifest.json" {
			continue
		}
		out := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return res, err
		}
		if err := os.WriteFile(out, b, 0o644); err != nil {
			return res, err
		}
	}
	next := commitRe.ReplaceAll(rawManifest, []byte(`"commit": "`+commit+`"`))
	if opt.Bump && res.Changed() {
		v, err := bundle.ParseVersion(m.Version)
		if err != nil {
			return res, err
		}
		res.Version = fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2]+1)
		next = versionRe.ReplaceAll(next, []byte(`"version": "`+res.Version+`"`))
	}
	return res, os.WriteFile(filepath.Join(dir, "manifest.json"), next, 0o644)
}

var (
	commitRe  = regexp.MustCompile(`"commit":\s*"[0-9a-f]*"`)
	versionRe = regexp.MustCompile(`"version":\s*"[^"]*"`)
)

// readUpstream finds the skills the rules name in a checkout: each skill's
// files by their path in its folder, and the files left out.
func readUpstream(repo string, rules upstreamRules) (map[string]map[string][]byte, []string, error) {
	var folders []string
	for _, pat := range rules.Skills {
		matches, err := filepath.Glob(filepath.Join(repo, filepath.FromSlash(pat)))
		if err != nil {
			return nil, nil, fmt.Errorf("%q: %w", pat, err)
		}
		if len(matches) == 0 {
			return nil, nil, fmt.Errorf("%q matches nothing in the repo", pat)
		}
		for _, m := range matches {
			if _, err := os.Stat(filepath.Join(m, bundle.SkillFile)); err == nil {
				folders = append(folders, m)
			}
		}
	}
	skills := map[string]map[string][]byte{}
	var dropped []string
	for _, folder := range folders {
		name := filepath.Base(folder)
		if rel, _ := filepath.Rel(repo, folder); rel == "." {
			raw, _ := os.ReadFile(filepath.Join(folder, bundle.SkillFile))
			name = frontMatterName(raw)
		}
		if slices.Contains(rules.Except, name) {
			continue
		}
		if _, dup := skills[name]; dup {
			return nil, nil, fmt.Errorf("two skills are called %q", name)
		}
		files := map[string][]byte{}
		err := filepath.WalkDir(folder, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(folder, p)
			rel = filepath.ToSlash(rel)
			if d.IsDir() {
				if rel != "." && (strings.HasPrefix(d.Name(), ".") || slices.Contains(notSkillDirs, d.Name()) && !strings.Contains(rel, "/")) {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.Contains(rel, "/") && (slices.Contains(notSkillFiles, rel) || slices.Contains(licenceFiles, rel)) || strings.HasPrefix(d.Name(), ".") {
				return nil
			}
			if !slices.Contains(bundle.SkillFileTypes, strings.ToLower(path.Ext(rel))) {
				full, _ := filepath.Rel(repo, p)
				dropped = append(dropped, filepath.ToSlash(full))
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			files[rel] = b
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
		skills[name] = files
	}
	if len(skills) == 0 {
		return nil, nil, errors.New("no skills found: each needs a folder with a SKILL.md")
	}
	sort.Strings(dropped)
	return skills, dropped, nil
}

// frontMatterName is the name a SKILL.md gives itself.
func frontMatterName(raw []byte) string {
	for _, line := range strings.Split(string(raw), "\n")[1:] {
		if line == "---" {
			break
		}
		if v, ok := strings.CutPrefix(line, "name:"); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}

func readLicence(repo string) ([]byte, error) {
	for _, n := range licenceFiles {
		if b, err := os.ReadFile(filepath.Join(repo, n)); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("the repo has no LICENSE file, so its skills can't be shared")
}

// readTree reads a pack's skills folder: skill name → its files.
func readTree(root string) (map[string]map[string][]byte, error) {
	out := map[string]map[string][]byte{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return filepath.SkipAll
		}
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		name, file, ok := strings.Cut(filepath.ToSlash(rel), "/")
		if !ok {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if out[name] == nil {
			out[name] = map[string][]byte{}
		}
		out[name][file] = b
		return nil
	})
	return out, err
}

func diffSkills(old, cur map[string]map[string][]byte) (added, changed, gone []string) {
	for name, files := range cur {
		was, ok := old[name]
		switch {
		case !ok:
			added = append(added, name)
		case len(was) != len(files):
			changed = append(changed, name)
		default:
			for p, b := range files {
				if !bytes.Equal(was[p], b) {
					changed = append(changed, name)
					break
				}
			}
		}
	}
	for name := range old {
		if _, ok := cur[name]; !ok {
			gone = append(gone, name)
		}
	}
	sort.Strings(added)
	sort.Strings(changed)
	sort.Strings(gone)
	return added, changed, gone
}

// fetch checks out one commit (or HEAD) of a public repo, shallowly.
func fetch(dir, repo, ref string) error {
	if _, err := git(dir, "init", "-q"); err != nil {
		return err
	}
	if _, err := git(dir, "fetch", "-q", "--depth", "1", repo+".git", ref); err != nil {
		return err
	}
	_, err := git(dir, "checkout", "-q", "FETCH_HEAD")
	return err
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(errOut.String()))
	}
	return strings.TrimSpace(out.String()), nil
}
