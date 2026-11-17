package bundle

import (
	"bytes"
	"fmt"
	"path"
	"sort"
	"strings"
)

// A skill is one of two shapes:
//
//	skills/<name>.md          one file
//	skills/<name>/SKILL.md    a folder, the way Agent Skills are shared,
//	                          with any files the skill reads beside it
//
// Either describes itself in front matter instead of being listed in the
// manifest:
//
//	---
//	description: Write a status update someone can read in ten seconds.
//	applies: writing an update, a report or a summary
//	---
//	# When to use
//	...
//
// The skill's name is its file or folder name. A folder's SKILL.md may
// spell "applies when" as x-enplace-applies, so it stays a plain Agent
// Skill elsewhere. A folder skill that doesn't say is given to the roles it
// is on (AppliesByRole): skills shared on GitHub are written to be picked,
// not matched to every item.

// AppliesByRole is the "applies when" of a skill that goes, in full, only to
// agents playing a role it's on. Anyone else sees one line about it.
const AppliesByRole = "by role"

// SkillFile is the name of a folder skill's main file.
const SkillFile = "SKILL.md"

// SkillPath is where a skill's text is in a bundle's files: skills/<name>.md,
// or skills/<name>/SKILL.md for a folder skill. ok is false when there's
// neither.
func SkillPath(files map[string][]byte, name string) (string, bool) {
	for _, p := range []string{"skills/" + name + ".md", "skills/" + name + "/" + SkillFile} {
		if _, ok := files[p]; ok {
			return p, true
		}
	}
	return "", false
}

// SkillFiles are the files a folder skill keeps beside its SKILL.md, by
// their path inside the skill's folder (like references/tests.md).
func SkillFiles(files map[string][]byte, name string) map[string][]byte {
	out := map[string][]byte{}
	prefix := "skills/" + name + "/"
	for p, b := range files {
		if rel, ok := strings.CutPrefix(p, prefix); ok && rel != SkillFile {
			out[rel] = b
		}
	}
	return out
}

// skillsFromFiles fills in the manifest's skills from the front matter of
// its skill files, for those it doesn't list, and takes the front matter
// off each file so agents only see the skill itself.
func skillsFromFiles(m *Manifest, files map[string][]byte) error {
	listed := map[string]bool{}
	for _, s := range m.Contributes.Skills {
		listed[s.Name] = true
	}
	type found struct{ path, name string }
	var skills []found
	for p := range files {
		switch {
		case path.Dir(p) == "skills" && path.Ext(p) == ".md":
			skills = append(skills, found{p, strings.TrimSuffix(path.Base(p), ".md")})
		case path.Base(p) == SkillFile && path.Dir(path.Dir(p)) == "skills":
			skills = append(skills, found{p, path.Base(path.Dir(p))})
		}
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].path < skills[j].path })
	for _, f := range skills {
		folder := path.Base(f.path) == SkillFile
		meta, body, ok := frontMatter(files[f.path])
		if !ok {
			if folder {
				return fmt.Errorf("%s: it needs front matter with a description", f.path)
			}
			continue
		}
		files[f.path] = body
		if listed[f.name] {
			continue
		}
		s := Skill{Name: f.name, Description: meta["description"], Applies: meta["applies"]}
		if s.Applies == "" {
			s.Applies = meta["x-enplace-applies"]
		}
		if s.Applies == "" && folder {
			s.Applies = AppliesByRole
		}
		if s.Description == "" {
			return fmt.Errorf("%s: its front matter needs a description", f.path)
		}
		m.Contributes.Skills = append(m.Contributes.Skills, s)
	}
	return m.Contributes.checkSkills()
}

// frontMatter splits "---\nkey: value\n---\n" off the top of a file. It reads
// the top-level keys of the YAML people write there: plain, quoted and
// block (| or >) values. Nested keys, like an Agent Skill's metadata, are
// skipped.
func frontMatter(b []byte) (map[string]string, []byte, bool) {
	b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	rest, ok := bytes.CutPrefix(b, []byte("---\n"))
	if !ok {
		return nil, b, false
	}
	var head, body []byte
	if h, after, ok := bytes.Cut(rest, []byte("\n---\n")); ok {
		head, body = h, after
	} else if h, ok := bytes.CutSuffix(bytes.TrimRight(rest, "\n"), []byte("\n---")); ok {
		head = h
	} else {
		return nil, b, false
	}
	meta := map[string]string{}
	lines := strings.Split(string(head), "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '#' {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if style := strings.TrimRight(v, "+-"); style == "|" || style == ">" {
			var block []string
			for i+1 < len(lines) && (lines[i+1] == "" || lines[i+1][0] == ' ' || lines[i+1][0] == '\t') {
				i++
				block = append(block, strings.TrimSpace(lines[i]))
			}
			sep := "\n"
			if style == ">" {
				sep = " "
			}
			meta[k] = strings.TrimSpace(strings.Join(block, sep))
			continue
		}
		meta[k] = unquote(v)
	}
	return meta, bytes.TrimLeft(body, "\n"), true
}

// unquote takes the quotes off a YAML scalar.
func unquote(v string) string {
	if len(v) >= 2 {
		switch {
		case v[0] == '"' && v[len(v)-1] == '"':
			r := strings.NewReplacer(`\"`, `"`, `\\`, `\`, `\n`, "\n")
			return r.Replace(v[1 : len(v)-1])
		case v[0] == '\'' && v[len(v)-1] == '\'':
			return strings.ReplaceAll(v[1:len(v)-1], "''", "'")
		}
	}
	return v
}
