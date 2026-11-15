// Package versionfile finds the file a codebase keeps its version in, and
// reads the version out of it. It knows the usual places: an Xcode project
// (XcodeGen's project.yml, project.pbxproj, Info.plist), package.json,
// Gradle, pubspec.yaml, Cargo.toml, pyproject.toml, Expo's app.json and a
// plain VERSION file. Any other file works too when it says "version" next
// to a number.
//
// It knows nothing about where the files come from, so any code host
// plugin (GitHub today, GitLab or Bitbucket later) can use it.
package versionfile

import (
	"encoding/json"
	"errors"
	"path"
	"regexp"
	"sort"
	"strings"
)

// known are the files that hold a version, best first.
var known = []struct {
	match func(p string) bool
	name  string
}{
	{base("project.yml"), "XcodeGen project"},
	{base("package.json"), "package.json"},
	{base("pubspec.yaml"), "Flutter pubspec"},
	{base("app.json"), "Expo app.json"},
	{base("build.gradle.kts"), "Gradle"},
	{base("build.gradle"), "Gradle"},
	{base("Cargo.toml"), "Cargo.toml"},
	{base("pyproject.toml"), "pyproject.toml"},
	{base("project.pbxproj"), "Xcode project"},
	{base("Info.plist"), "Info.plist"},
	{func(p string) bool {
		b := strings.ToLower(path.Base(p))
		return b == "version" || b == "version.txt"
	}, "VERSION file"},
}

func base(name string) func(string) bool {
	return func(p string) bool { return path.Base(p) == name }
}

// skip are folders that hold other people's code or build output.
var skip = []string{"node_modules", "Pods", "vendor", "Carthage", ".build", "build", "dist", "DerivedData", "third_party", "testdata", "fixtures", "examples", "example", ".git"}

// Candidates picks the files in a repo (paths relative to its root) that
// may hold its version, best first: the kind of file, then how near the
// root it is. Only the first few are worth reading.
func Candidates(paths []string) []string {
	type cand struct {
		p           string
		rank, depth int
	}
	var out []cand
	for _, p := range paths {
		p = strings.TrimPrefix(p, "/")
		parts := strings.Split(p, "/")
		if len(parts) > 4 || skipped(parts[:len(parts)-1]) {
			continue
		}
		for i, k := range known {
			if k.match(p) {
				out = append(out, cand{p, i, len(parts)})
				break
			}
		}
	}
	// The kind of file counts, and so does being near the root: a strong
	// kind one level down (an app in its own folder) beats a weak kind at
	// the root.
	score := func(c cand) int { return c.rank + 4*(c.depth-1) }
	sort.SliceStable(out, func(i, j int) bool {
		if si, sj := score(out[i]), score(out[j]); si != sj {
			return si < sj
		}
		return out[i].p < out[j].p
	})
	ps := make([]string, len(out))
	for i, c := range out {
		ps[i] = c.p
	}
	return ps
}

func skipped(dirs []string) bool {
	for _, d := range dirs {
		if strings.HasPrefix(d, ".") {
			return true
		}
		for _, s := range skip {
			if strings.EqualFold(d, s) {
				return true
			}
		}
	}
	return false
}

// Kind names a file's kind for people, like "XcodeGen project".
func Kind(p string) string {
	for _, k := range known {
		if k.match(p) {
			return k.name
		}
	}
	return "file"
}

// ErrNone means the file has no version Enplace can read.
var ErrNone = errors.New("no version in this file")

var (
	reMarketing = regexp.MustCompile(`(?m)\bMARKETING_VERSION\s*[:=]\s*["']?([0-9][0-9A-Za-z.\-+]*)`)
	rePlist     = regexp.MustCompile(`<key>CFBundleShortVersionString</key>\s*<string>([^<]*)</string>`)
	reGradle    = regexp.MustCompile(`(?m)^\s*versionName\s*(?:=\s*)?["']([^"']+)["']`)
	reTOMLSect  = regexp.MustCompile(`(?m)^\s*\[([^\]]+)\]\s*$`)
	reTOMLVer   = regexp.MustCompile(`(?m)^\s*version\s*=\s*["']([^"']+)["']`)
	reYAMLVer   = regexp.MustCompile(`(?m)^version:\s*["']?([^"'\s#]+)`)
	reAny       = regexp.MustCompile(`(?i)\bversion["']?\s*[:=]\s*["']?v?([0-9]+(?:\.[0-9]+){0,3}(?:[-+][0-9A-Za-z.\-]+)?)`)
	reBare      = regexp.MustCompile(`^v?([0-9]+(?:\.[0-9]+){0,3}(?:[-+][0-9A-Za-z.\-]+)?)$`)
)

// Read reads the version from a file's contents. The path picks how.
func Read(p string, data []byte) (string, error) {
	s := string(data)
	var v string
	switch b := path.Base(p); {
	case b == "project.yml" || b == "project.pbxproj":
		v = first(reMarketing, s)
	case b == "package.json":
		var pkg struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(data, &pkg) == nil {
			v = pkg.Version
		}
	case b == "app.json":
		var app struct {
			Version string `json:"version"`
			Expo    struct {
				Version string `json:"version"`
			} `json:"expo"`
		}
		if json.Unmarshal(data, &app) == nil {
			v = app.Expo.Version
			if v == "" {
				v = app.Version
			}
		}
	case b == "pubspec.yaml":
		// 1.4.0+12: the part after + is the build number.
		v, _, _ = strings.Cut(first(reYAMLVer, s), "+")
	case strings.HasPrefix(b, "build.gradle"):
		v = first(reGradle, s)
	case b == "Cargo.toml":
		v = tomlVersion(s, "package", "workspace.package")
	case b == "pyproject.toml":
		v = tomlVersion(s, "project", "tool.poetry")
	case b == "Info.plist":
		v = first(rePlist, s)
	case strings.EqualFold(b, "VERSION") || strings.EqualFold(b, "version.txt"):
		v = first(reBare, strings.TrimSpace(s))
	default:
		v = first(reAny, s)
	}
	v = strings.TrimSpace(v)
	// Info.plist often says $(MARKETING_VERSION): the number is elsewhere.
	if v == "" || strings.ContainsAny(v, "$({}") || !startsWithDigit(strings.TrimPrefix(v, "v")) {
		return "", ErrNone
	}
	return v, nil
}

func first(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

// tomlVersion is version = "..." in the first of the sections named.
func tomlVersion(s string, sections ...string) string {
	idx := reTOMLSect.FindAllStringSubmatchIndex(s, -1)
	for _, want := range sections {
		for i, m := range idx {
			if strings.TrimSpace(s[m[2]:m[3]]) != want {
				continue
			}
			end := len(s)
			if i+1 < len(idx) {
				end = idx[i+1][0]
			}
			if v := first(reTOMLVer, s[m[1]:end]); v != "" {
				return v
			}
		}
	}
	return ""
}

func startsWithDigit(s string) bool { return s != "" && s[0] >= '0' && s[0] <= '9' }

// Same says whether two versions are the same release: "v1.4", "1.4" and
// "1.4.0" are; "1.4" and "1.4.1" aren't.
func Same(a, b string) bool {
	na, nb := numbers(a), numbers(b)
	if na == nil || nb == nil {
		return strings.TrimPrefix(a, "v") == strings.TrimPrefix(b, "v")
	}
	for len(na) < len(nb) {
		na = append(na, "0")
	}
	for len(nb) < len(na) {
		nb = append(nb, "0")
	}
	for i := range na {
		if na[i] != nb[i] {
			return false
		}
	}
	return true
}

// Newer says whether a is a later version than b. Versions it can't read
// are never newer.
func Newer(a, b string) bool {
	na, nb := numbers(a), numbers(b)
	if na == nil {
		return false
	}
	if nb == nil {
		return true
	}
	for i := 0; i < len(na) || i < len(nb); i++ {
		x, y := 0, 0
		if i < len(na) {
			x = atoi(na[i])
		}
		if i < len(nb) {
			y = atoi(nb[i])
		}
		if x != y {
			return x > y
		}
	}
	return false
}

// numbers splits "v1.4.0-beta+3" into ["1","4","0"], or nil.
func numbers(v string) []string {
	v = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(v), "release-"), "v")
	if i := strings.IndexAny(v, "-+ "); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	for _, p := range parts {
		if p == "" || strings.Trim(p, "0123456789") != "" || len(p) > 9 {
			return nil
		}
	}
	for len(parts) > 1 && parts[len(parts)-1] == "0" {
		parts = parts[:len(parts)-1]
	}
	for i, p := range parts {
		parts[i] = strings.TrimLeft(p, "0")
		if parts[i] == "" {
			parts[i] = "0"
		}
	}
	return parts
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

// FromTag reads a version from a tag name like "v1.4", "1.4.0" or
// "release-1.4", or "" when it isn't one.
func FromTag(tag string) string {
	t := strings.TrimPrefix(tag, "release-")
	t = strings.TrimPrefix(strings.TrimPrefix(t, "release/"), "v")
	if numbers(t) == nil {
		return ""
	}
	return t
}
