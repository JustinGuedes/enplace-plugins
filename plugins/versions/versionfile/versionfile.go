// Package versionfile finds the file a codebase keeps its version in,
// reads the version and build number out of it, and works out the edits
// that set new ones. It knows the usual places: an Xcode project
// (XcodeGen's project.yml, .xcconfig files, project.pbxproj, Info.plist),
// package.json, Gradle, pubspec.yaml, Cargo.toml, pyproject.toml, Expo's
// app.json and a plain VERSION file. Any other file works too when it says
// "version" next to a number.
//
// It knows nothing about where the files come from: a code plugin (GitHub
// today, GitLab or Bitbucket later) hands them over.
package versionfile

import (
	"encoding/json"
	"errors"
	"path"
	"regexp"
	"sort"
	"strings"
)

// known are the files that hold a version, best first, as patterns for a
// file's name (case doesn't matter).
var known = []struct {
	pattern, name string
}{
	{"project.yml", "XcodeGen project"},
	// An .xcconfig named for the version is the likeliest; the rest
	// (Debug, Release, Shared) only now and then set it.
	{"*version*.xcconfig", "Xcode config"},
	{"package.json", "package.json"},
	{"pubspec.yaml", "Flutter pubspec"},
	{"app.json", "Expo app.json"},
	{"build.gradle.kts", "Gradle"},
	{"build.gradle", "Gradle"},
	{"cargo.toml", "Cargo.toml"},
	{"pyproject.toml", "pyproject.toml"},
	{"project.pbxproj", "Xcode project"},
	{"*.xcconfig", "Xcode config"},
	{"info.plist", "Info.plist"},
	{"version", "VERSION file"},
	{"version.txt", "VERSION file"},
}

// rank is where a file's kind comes in known, or -1.
func rank(p string) int {
	b := strings.ToLower(path.Base(p))
	for i, k := range known {
		if ok, _ := path.Match(k.pattern, b); ok {
			return i
		}
	}
	return -1
}

// Patterns are the names of the files worth reading, best first, for a
// code plugin to look for (case doesn't matter).
func Patterns() []string {
	out := make([]string, len(known))
	for i, k := range known {
		out[i] = k.pattern
	}
	return out
}

// Skip are folders that hold other people's code or build output.
var Skip = []string{"node_modules", "Pods", "vendor", "Carthage", ".build", "build", "dist", "DerivedData", "third_party", "testdata", "fixtures", "examples", "example", ".git"}

// MaxDepth is how deep in a repo a version file is looked for.
const MaxDepth = 4

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
		if len(parts) > MaxDepth || skipped(parts[:len(parts)-1]) {
			continue
		}
		if i := rank(p); i >= 0 {
			out = append(out, cand{p, i, len(parts)})
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
		for _, s := range Skip {
			if strings.EqualFold(d, s) {
				return true
			}
		}
	}
	return false
}

// Kind names a file's kind for people, like "XcodeGen project".
func Kind(p string) string {
	if i := rank(p); i >= 0 {
		return known[i].name
	}
	return "file"
}

// ErrNone means the file has no version Enplace can read.
var ErrNone = errors.New("no version in this file")

var (
	reMarketing = xcode("MARKETING_VERSION")
	reBuild     = xcode("CURRENT_PROJECT_VERSION")
	rePlist     = regexp.MustCompile(`<key>CFBundleShortVersionString</key>\s*<string>([^<]*)</string>`)
	reGradle    = regexp.MustCompile(`(?m)^\s*versionName\s*(?:=\s*)?["']([^"']+)["']`)
	reTOMLSect  = regexp.MustCompile(`(?m)^\s*\[([^\]]+)\]\s*$`)
	reTOMLVer   = regexp.MustCompile(`(?m)^\s*version\s*=\s*["']([^"']+)["']`)
	reYAMLVer   = regexp.MustCompile(`(?m)^version:\s*["']?([^"'\s#]+)`)
	reAny       = regexp.MustCompile(`(?i)\bversion["']?\s*[:=]\s*["']?v?([0-9]+(?:\.[0-9]+){0,3}(?:[-+][0-9A-Za-z.\-]+)?)`)
	reBare      = regexp.MustCompile(`^v?([0-9]+(?:\.[0-9]+){0,3}(?:[-+][0-9A-Za-z.\-]+)?)$`)
)

// xcode matches an Xcode build setting set to a number, as project.yml
// (KEY: 1.4), project.pbxproj (KEY = 1.4;) and .xcconfig files
// (KEY[config=Release] = 1.4) write it, but not in a // comment.
func xcode(key string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)(?:^|[{,])[ \t]*"?` + key + `(?:\[[^\]\n]*\])?"?[ \t]*[:=][ \t]*["']?([0-9][0-9A-Za-z.\-+]*)`)
}

func xcodeFile(p string) bool {
	b := strings.ToLower(path.Base(p))
	return b == "project.yml" || b == "project.pbxproj" || path.Ext(b) == ".xcconfig"
}

// Read reads the version from a file's contents. The path picks how.
func Read(p string, data []byte) (string, error) {
	s := string(data)
	var v string
	switch b := strings.ToLower(path.Base(p)); {
	case xcodeFile(p):
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
	case b == "cargo.toml":
		v = tomlVersion(s, "package", "workspace.package")
	case b == "pyproject.toml":
		v = tomlVersion(s, "project", "tool.poetry")
	case b == "info.plist":
		v = first(rePlist, s)
	case b == "version" || b == "version.txt":
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

// Build reads the build number beside the version, like 42 from an Xcode
// project's CURRENT_PROJECT_VERSION or pubspec.yaml's 1.4.0+42, or "" when
// the file has none.
func Build(p string, data []byte) string {
	var b string
	switch {
	case xcodeFile(p):
		b = first(reBuild, string(data))
	case strings.ToLower(path.Base(p)) == "pubspec.yaml":
		_, b, _ = strings.Cut(first(reYAMLVer, string(data)), "+")
	}
	if !startsWithDigit(b) {
		return ""
	}
	return b
}

func first(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

// tomlVersion is version = "..." in the first of the sections named.
func tomlVersion(s string, sections ...string) string {
	from, to := tomlSection(s, sections...)
	return first(reTOMLVer, s[from:to])
}

// tomlSection is where the first of the sections named that sets a
// version starts and ends, or 0, 0.
func tomlSection(s string, sections ...string) (int, int) {
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
			if first(reTOMLVer, s[m[1]:end]) != "" {
				return m[1], end
			}
		}
	}
	return 0, 0
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
