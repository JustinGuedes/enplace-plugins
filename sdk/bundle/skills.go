package bundle

import (
	"bytes"
	"fmt"
	"path"
	"sort"
	"strings"
)

// Skill files can describe themselves in front matter, the way Agent
// Skills do, instead of being listed in the manifest:
//
//	---
//	description: Write a status update someone can read in ten seconds.
//	applies: writing an update, a report or a summary
//	---
//	# When to use
//	...
//
// The skill's name is its file name.

// skillsFromFiles fills in the manifest's skills from the front matter of
// its skills/<name>.md files, for those it doesn't list, and takes the
// front matter off each file so agents only see the skill itself.
func skillsFromFiles(m *Manifest, files map[string][]byte) error {
	listed := map[string]bool{}
	for _, s := range m.Contributes.Skills {
		listed[s.Name] = true
	}
	var names []string
	for p := range files {
		if strings.HasPrefix(p, "skills/") && path.Ext(p) == ".md" && path.Dir(p) == "skills" {
			names = append(names, p)
		}
	}
	sort.Strings(names)
	for _, p := range names {
		meta, body, ok := frontMatter(files[p])
		if !ok {
			continue
		}
		files[p] = body
		name := strings.TrimSuffix(path.Base(p), ".md")
		if listed[name] {
			continue
		}
		s := Skill{Name: name, Description: meta["description"], Applies: meta["applies"]}
		if s.Description == "" {
			return fmt.Errorf("%s: its front matter needs a description", p)
		}
		m.Contributes.Skills = append(m.Contributes.Skills, s)
	}
	return m.Contributes.checkSkills()
}

// frontMatter splits "---\nkey: value\n---\n" off the top of a file.
func frontMatter(b []byte) (map[string]string, []byte, bool) {
	rest, ok := bytes.CutPrefix(b, []byte("---\n"))
	if !ok {
		return nil, b, false
	}
	head, body, ok := bytes.Cut(rest, []byte("\n---\n"))
	if !ok {
		return nil, b, false
	}
	meta := map[string]string{}
	for _, line := range strings.Split(string(head), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if ok {
			meta[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return meta, bytes.TrimLeft(body, "\n"), true
}
