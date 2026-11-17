package versionfile

import (
	"path"
	"regexp"
	"strings"
)

// An Edit changes one piece of a file: Old, exactly as the file has it,
// becomes New. All changes every place Old is, not just the first (an
// Xcode project sets its version once for each build configuration).
type Edit struct {
	Path string `json:"path"`
	Old  string `json:"old"`
	New  string `json:"new"`
	All  bool   `json:"all,omitempty"`
}

var reJSONVer = regexp.MustCompile(`"version"\s*:\s*"([^"]*)"`)

// Edits are the changes that set a file's version to version, and its build
// number to build when build isn't "" and the file keeps one. ErrNone means
// the file has no version Enplace can change.
func Edits(p string, data []byte, version, build string) ([]Edit, error) {
	if _, err := Read(p, data); err != nil {
		return nil, err
	}
	s := string(data)
	var out []Edit
	add := func(re *regexp.Regexp, text, v string, all bool) {
		for _, m := range re.FindAllStringSubmatchIndex(text, -1) {
			old := text[m[0]:m[1]]
			e := Edit{Path: p, Old: old, New: old[:m[2]-m[0]] + v + old[m[3]-m[0]:], All: all}
			if e.Old != e.New && !hasEdit(out, e) {
				out = append(out, e)
			}
			if !all {
				return
			}
		}
	}
	switch b := strings.ToLower(path.Base(p)); {
	case xcodeFile(p):
		add(reMarketing, s, version, true)
		if build != "" {
			add(reBuild, s, build, true)
		}
	case b == "package.json" || b == "app.json":
		// app.json keeps it under "expo"; either way it's the first.
		add(reJSONVer, s, version, false)
	case b == "pubspec.yaml":
		v := version
		if _, old, ok := strings.Cut(first(reYAMLVer, s), "+"); ok || build != "" {
			if build == "" {
				build = old
			}
			v += "+" + build
		}
		add(reYAMLVer, s, v, false)
	case strings.HasPrefix(b, "build.gradle"):
		add(reGradle, s, version, false)
	case b == "cargo.toml" || b == "pyproject.toml":
		sections := []string{"package", "workspace.package"}
		if b == "pyproject.toml" {
			sections = []string{"project", "tool.poetry"}
		}
		if from, to := tomlSection(s, sections...); to > from {
			add(reTOMLVer, s[from:to], version, false)
		}
	case b == "info.plist":
		add(rePlist, s, version, false)
	case b == "version" || b == "version.txt":
		old := strings.TrimSpace(s)
		v := version
		if strings.HasPrefix(old, "v") {
			v = "v" + v
		}
		if old != v {
			out = append(out, Edit{Path: p, Old: old, New: v})
		}
	default:
		add(reAny, s, version, false)
	}
	return out, nil
}

func hasEdit(list []Edit, e Edit) bool {
	for _, x := range list {
		if x.Old == e.Old {
			return true
		}
	}
	return false
}

// Apply makes the edits to a file's text. It says false when one of them
// no longer fits, because the file changed since they were worked out.
func Apply(text string, edits []Edit) (string, bool) {
	for _, e := range edits {
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
