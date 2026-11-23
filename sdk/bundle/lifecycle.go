package bundle

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// A product's lifecycle: the stages its work goes through, from planning
// to watching it live, and the types of plugin that do each job in them.
//
// Every lifecycle plugin does one job, its type (Manifest.Type), like
// Builds or Beta testing. A plugin asks for other jobs by type
// (Manifest.Needs), not by naming a plugin, so any plugin of that type can
// fill the need: TestFlight needs Builds, which Xcode Cloud or GitHub
// Actions can be.
//
// Stages and types are data. Enplace knows the ones below, and a plugin can
// bring more with contributes.stages and contributes.types, so a new kind
// of plugin or product needs no new Enplace.

// FormatVersion is the manifest format this SDK reads. A manifest whose
// format is higher uses fields this SDK doesn't know: ParseManifest turns
// it away with ErrNewerFormat, so a server can keep the version it has
// instead of failing on the new one.
//
//   - 1: everything before lifecycle types.
//   - 2: type, needs, format, contributes.types and contributes.stages.
//   - 3: tracks: a setting's per, and contributes.purposes.
const FormatVersion = 3

// ErrNewerFormat is returned for a manifest written for a newer Enplace.
var ErrNewerFormat = errors.New("this plugin needs a newer Enplace")

// Stage is one column of a product's lifecycle, like Build.
type Stage struct {
	// Key is how the stage is named in code, like "build".
	Key string `json:"key"`
	// Name is how it reads as a column, like "Build".
	Name string `json:"name"`
	// Page is how it reads in the sidebar, like "Builds". Empty uses Name.
	Page string `json:"page,omitempty"`
	// Icon names a symbol from Enplace's shared set.
	Icon string `json:"icon,omitempty"`
	// Order places it among the other stages: lower comes first. The
	// built-in stages are 10 apart, so a new one can go between them.
	Order int `json:"order"`
	// About says what happens in it, in a few words.
	About string `json:"about,omitempty"`
}

// Type is a job a plugin can do, like Builds.
type Type struct {
	// Key is how the type is named in manifests, like "ci".
	Key string `json:"key"`
	// Name is how it reads, like "Builds".
	Name string `json:"name"`
	// About says what a plugin of this type does, in one sentence.
	About string `json:"about,omitempty"`
	// Stage is the key of the stage it sits in.
	Stage string `json:"stage"`
	// Contract is the major version of what plugins of this type share.
	// A change that would break a plugin reading it is a new major.
	Contract int `json:"contract"`
	// Publishes are the public state keys a plugin of this type shares,
	// like "public.ci".
	Publishes []string `json:"publishes,omitempty"`
	// Answers are the public state keys it acts on when another plugin
	// publishes them, like "public.ci_request".
	Answers []string `json:"answers,omitempty"`
	// View is how Enplace draws the type: one of Views, or "plugin" when
	// the plugin that brings the type draws it itself.
	View string `json:"view,omitempty"`
	// Many says a product may have several plugins of this type at once
	// (two builders), not just one.
	Many bool `json:"many,omitempty"`
	// Manual are stand-ins for when no plugin fills a need for this type,
	// like "You, in Xcode" for Builds: a need is never stuck.
	Manual []Manual `json:"manual,omitempty"`
	// ProductTypes are the product types it makes sense for, like "app".
	// Empty means any.
	ProductTypes []string `json:"product_types,omitempty"`
}

// Manual is a way a person (or their own agent) does a type's job without
// a plugin.
type Manual struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	About string `json:"about,omitempty"`
}

// Need is a job a plugin needs another plugin to do (Manifest.Needs).
type Need struct {
	// Type is the key of the type needed, like "ci".
	Type string `json:"type"`
	// Contract is the range of the type's contract versions this plugin
	// reads, like "^1". Empty takes any.
	Contract string `json:"contract,omitempty"`
	// For says why, shown to people: "to get builds".
	For string `json:"for,omitempty"`
	// Optional: the plugin works without it, and does more with it.
	Optional bool `json:"optional,omitempty"`
	// Many takes every plugin of the type, not just one (a health view
	// that shows them all).
	Many bool `json:"many,omitempty"`
	// Scope is where the need is filled from: "track" (the default: the
	// plugin of that type in the same track of the product), "product" or
	// "project". It's always looked for upwards too: product, then
	// project, then workspace.
	Scope string `json:"scope,omitempty"`
}

// Views are the ways Enplace can draw a type by itself.
var Views = []string{"runs", "builds", "store", "repo", "health", "list", "plugin"}

// NeedScopes are the places a need can be filled from.
var NeedScopes = []string{"track", "product", "project"}

// Stages are the stages Enplace knows, in order.
var Stages = []Stage{
	{Key: "plan", Name: "Plan", Page: "Plan", Icon: "list", Order: 10, About: "Where the work comes from and is kept."},
	{Key: "code", Name: "Code", Page: "Code", Icon: "code", Order: 20, About: "The code, its branches and pull requests."},
	{Key: "build", Name: "Build", Page: "Builds", Icon: "hammer", Order: 30, About: "Building it, and checking every build."},
	{Key: "test", Name: "Test", Page: "Testing", Icon: "phone", Order: 40, About: "Getting builds to you and your testers."},
	{Key: "ship", Name: "Ship", Page: "Store", Icon: "rocket", Order: 50, About: "Releasing it to people."},
	{Key: "watch", Name: "Watch", Page: "Health", Icon: "pulse", Order: 60, About: "How it's doing once it's out."},
}

// Types are the types Enplace knows.
var Types = []Type{
	{Key: "work", Name: "Work source", Stage: "plan", Contract: 1, View: "list",
		About:     "Keeps a project's work in step with another tool.",
		Publishes: []string{"public.work_sync"}},
	{Key: "vcs", Name: "Code", Stage: "code", Contract: 1, View: "repo",
		About:     "Holds the code: its repository, branches and pull requests.",
		Publishes: []string{"public.repo"}},
	{Key: "ci", Name: "Builds", Stage: "build", Contract: 1, View: "runs", Many: true,
		About:     "Builds the code and checks every build.",
		Publishes: []string{"public.ci", "public.builder"}, Answers: []string{"public.ci_request", "public.build_request"},
		Manual: []Manual{
			{Key: "agent", Name: "An agent on your Mac", About: "Your own agent builds and uploads it."},
			{Key: "you", Name: "You", About: "You build and upload it yourself, like from Xcode."},
		}},
	{Key: "beta", Name: "Beta testing", Stage: "test", Contract: 1, View: "builds",
		About:        "Gets builds onto your phone and your testers'.",
		Publishes:    []string{"public.builds"},
		ProductTypes: []string{"app"}},
	{Key: "store", Name: "Store", Stage: "ship", Contract: 1, View: "store", Many: true,
		About:        "Follows the product in a store: its listing, review and numbers.",
		Publishes:    []string{"public.app"},
		ProductTypes: []string{"app"}},
	{Key: "release", Name: "Release", Stage: "ship", Contract: 1, View: "list",
		About:     "Plans releases and what goes in each.",
		Publishes: []string{"public.releases"}},
	{Key: "version", Name: "Versioning", Stage: "ship", Contract: 1, View: "list",
		About:     "Where the version lives, how it's numbered and bumped.",
		Publishes: []string{"public.version"}},
	{Key: "deploy", Name: "Hosting", Stage: "ship", Contract: 1, View: "runs",
		About:        "Puts a site or service live, and keeps each deploy.",
		Publishes:    []string{"public.deploy"},
		ProductTypes: []string{"website"}},
	{Key: "health", Name: "Health", Stage: "watch", Contract: 1, View: "health", Many: true,
		About:     "Watches it once it's out: up or down, speed, errors and crashes.",
		Publishes: []string{"public.health"}},
}

// StageByKey finds a built-in stage.
func StageByKey(key string) (Stage, bool) {
	i := slices.IndexFunc(Stages, func(s Stage) bool { return s.Key == key })
	if i < 0 {
		return Stage{}, false
	}
	return Stages[i], true
}

// TypeByKey finds a built-in type.
func TypeByKey(key string) (Type, bool) {
	i := slices.IndexFunc(Types, func(t Type) bool { return t.Key == key })
	if i < 0 {
		return Type{}, false
	}
	return Types[i], true
}

// typeKinds are the kinds that can have a type. Agents, roles and skill
// packs work across the lifecycle, so they have none.
var typeKinds = []string{KindSource, KindService, KindExtension}

// checkLifecycle checks the manifest's format, type, needs and the stages
// and types it brings.
func (m *Manifest) checkLifecycle() error {
	if m.Format < 0 || m.Format > FormatVersion {
		return ErrNewerFormat
	}
	if m.Kind == KindSource && m.Type == "" {
		m.Type = "work"
	}
	if m.Type != "" {
		if !keyRe.MatchString(m.Type) {
			return fmt.Errorf("type %q is a key like ci", m.Type)
		}
		if !slices.Contains(typeKinds, m.Kind) {
			return fmt.Errorf("a %s plugin has no type: types are for %s plugins", m.Kind, strings.Join(typeKinds, ", "))
		}
	}
	if len(m.Needs) > 10 {
		return errors.New("a plugin needs at most 10 types")
	}
	seen := map[string]bool{}
	for _, n := range m.Needs {
		if !keyRe.MatchString(n.Type) {
			return fmt.Errorf("needs: type %q is a key like ci", n.Type)
		}
		if seen[n.Type] {
			return fmt.Errorf("needs: %s is listed twice", n.Type)
		}
		seen[n.Type] = true
		if _, err := ParseRange(n.Contract); err != nil {
			return fmt.Errorf("needs %s: contract: %w", n.Type, err)
		}
		if n.Scope != "" && !slices.Contains(NeedScopes, n.Scope) {
			return fmt.Errorf("needs %s: scope is one of %s", n.Type, strings.Join(NeedScopes, ", "))
		}
		if len(n.For) > 80 {
			return fmt.Errorf("needs %s: for is a few words", n.Type)
		}
	}
	stages := map[string]bool{}
	for _, s := range Stages {
		stages[s.Key] = true
	}
	for _, s := range m.Contributes.Stages {
		if !keyRe.MatchString(s.Key) || stages[s.Key] {
			return fmt.Errorf("stages: bad or repeated stage %q", s.Key)
		}
		if s.Name == "" || len(s.Name) > 24 || len(s.Page) > 24 || len(s.About) > 120 {
			return fmt.Errorf("stage %s: name and page are up to 24 characters", s.Key)
		}
		stages[s.Key] = true
	}
	types := map[string]bool{}
	for i := range m.Contributes.Types {
		t := &m.Contributes.Types[i]
		if _, builtIn := TypeByKey(t.Key); !keyRe.MatchString(t.Key) || builtIn || types[t.Key] {
			return fmt.Errorf("types: bad or repeated type %q", t.Key)
		}
		types[t.Key] = true
		if t.Name == "" || len(t.Name) > 30 || len(t.About) > 200 {
			return fmt.Errorf("type %s: name is up to 30 characters", t.Key)
		}
		if !stages[t.Stage] {
			return fmt.Errorf("type %s: stage %q is neither Enplace's nor this plugin's", t.Key, t.Stage)
		}
		if t.Contract < 1 {
			return fmt.Errorf("type %s: contract starts at 1", t.Key)
		}
		if t.View == "" {
			t.View = "plugin"
		}
		if !slices.Contains(Views, t.View) {
			return fmt.Errorf("type %s: view is one of %s", t.Key, strings.Join(Views, ", "))
		}
		for _, k := range append(slices.Clone(t.Publishes), t.Answers...) {
			if !strings.HasPrefix(k, "public.") {
				return fmt.Errorf("type %s: %q is a public state key, like public.%s", t.Key, k, t.Key)
			}
		}
		for _, x := range t.Manual {
			if !keyRe.MatchString(x.Key) || x.Name == "" {
				return fmt.Errorf("type %s: each manual way has a key and a name", t.Key)
			}
		}
	}
	for _, p := range m.Contributes.Purposes {
		if _, builtIn := PurposeByKey(p.Key); !keyRe.MatchString(p.Key) || builtIn || p.Name == "" || len(p.Name) > 24 || len(p.About) > 120 {
			return fmt.Errorf("purposes: %q needs a new key and a name of up to 24 characters", p.Key)
		}
	}
	return nil
}

// Tracks (ENP-350). A track is one path a product's work takes: Main
// builds, Internal beta, Release. Each has a purpose, which features look
// tracks up by (Autopilot's test build takes the internal one, Releases
// ships through production), and a trigger. A setting with "per": "track"
// holds a value for each track (plugin.Request.Tracks).

// Purpose is what a track is for.
type Purpose struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	// Makes says the track's builds are made for someone to use (a
	// TestFlight build, a store build, a deploy). A check track only
	// proves the code builds and passes, so a beta or store step can't
	// take builds from it.
	Makes bool   `json:"makes"`
	About string `json:"about,omitempty"`
}

// Purposes are the purposes Enplace knows, in the order they're offered.
var Purposes = []Purpose{
	{Key: "check", Name: "Checks", About: "Builds and tests the code, like every push to main. Makes nothing to install."},
	{Key: "internal", Name: "Internal", Makes: true, About: "Builds for your own phone and your team."},
	{Key: "external", Name: "Beta testers", Makes: true, About: "Builds for testers outside the team."},
	{Key: "production", Name: "Release", Makes: true, About: "The build that goes to the store, or the live site."},
	{Key: "preview", Name: "Preview", Makes: true, About: "A build for each pull request, to try before merging."},
}

// PurposeByKey finds one of Enplace's purposes.
func PurposeByKey(key string) (Purpose, bool) {
	for _, p := range Purposes {
		if p.Key == key {
			return p, true
		}
	}
	return Purpose{}, false
}

// Triggers are what can start a track: a push to matching branches, a tag,
// a pull request, a schedule, a press of a button, or a release being cut.
var Triggers = []string{"push", "tag", "pull_request", "schedule", "manual", "release"}
