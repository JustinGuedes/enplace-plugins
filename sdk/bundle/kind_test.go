package bundle

import (
	"slices"
	"strings"
	"testing"
)

func parseDemo(t *testing.T, body string) (*Manifest, error) {
	t.Helper()
	return ParseManifest([]byte(`{"id":"demo","name":"Demo","version":"1.0.0",` + body + `}`))
}

func TestKindFillsInWhereAPluginGoes(t *testing.T) {
	cases := []struct {
		kind, body string
		scope      string
		levels     []string
		flows      string
		extends    []string
	}{
		{KindSkills, ``, "both", []string{"workspace", "project", "product"}, InheritDown, nil},
		{KindAgent, `"contributes":{"runner":{"agent":"Claude","where":"computer","pays":"plan","kind":"prompt","prompt":"Do {key}"}}`, "workspace", []string{"workspace"}, InheritNone, nil},
		{KindSource, ``, "project", []string{"project", "product"}, InheritOffer, []string{"work-sync"}},
		{KindService, ``, "project", []string{"product"}, InheritNone, nil},
	}
	for _, c := range cases {
		t.Run(c.kind, func(t *testing.T) {
			body := `"kind":"` + c.kind + `"`
			if c.body != "" {
				body += "," + c.body
			}
			m, err := parseDemo(t, body)
			if err != nil {
				t.Fatal(err)
			}
			if m.Scope != c.scope || !slices.Equal(m.OnLevels(), c.levels) || m.Flows() != c.flows || !slices.Equal(m.Extends, c.extends) {
				t.Errorf("scope %s, levels %v, flows %s, extends %v", m.Scope, m.OnLevels(), m.Flows(), m.Extends)
			}
		})
	}
}

func TestWhatAManifestSaysWinsOverItsKind(t *testing.T) {
	m, err := parseDemo(t, `"kind":"service","levels":["project","product"],"inherit":"offer"`)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(m.Levels, []string{"project", "product"}) || m.Inherit != InheritOffer {
		t.Errorf("levels %v, inherit %s", m.Levels, m.Inherit)
	}
}

func TestKindKeepsAPluginToItsPieces(t *testing.T) {
	cases := map[string]string{
		"skills plugin with a page":    `"kind":"skills","contributes":{"pages":[{"key":"p","name":"P","where":"workspace"}]}`,
		"agent without a runner":       `"kind":"agent"`,
		"service that brings a runner": `"kind":"service","contributes":{"runner":{"agent":"Claude","where":"computer","pays":"plan","kind":"prompt","prompt":"Do {key}"}}`,
		"unknown kind":                 `"kind":"theme"`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseDemo(t, body); err == nil {
				t.Error("accepted")
			}
		})
	}
}

func TestAnExtensionSaysWhereItGoes(t *testing.T) {
	cases := map[string]string{
		`"levels":["workspace"]`:              "workspace",
		`"levels":["workspace","project"]`:    "both",
		`"levels":["project","product"]`:      "project",
		`"permissions":{"read":["work"]}`:     "project",
		`"scope":"both","levels":["product"]`: "both",
	}
	for body, scope := range cases {
		m, err := parseDemo(t, `"kind":"extension",`+body)
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if m.Scope != scope {
			t.Errorf("%s: scope %s, want %s", body, m.Scope, scope)
		}
	}
}

func TestABundleFromBeforeKindsIsAnExtension(t *testing.T) {
	m, err := parseDemo(t, `"levels":["project"],"contributes":{"pages":[{"key":"p","name":"P","where":"project"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if m.Kind != KindExtension || m.Scope != "project" {
		t.Errorf("kind %q, scope %q", m.Kind, m.Scope)
	}
}

func TestSkillsDescribeThemselves(t *testing.T) {
	files := map[string][]byte{
		"manifest.json":   []byte(`{"id":"pack","name":"Pack","version":"1.0.0","kind":"skills"}`),
		"skills/tidy.md":  []byte("---\ndescription: Keep it tidy.\napplies: \"writing anything\"\n---\n\n# Tidy\n"),
		"skills/plain.md": []byte("---\ndescription: Say it plainly.\n---\n# Plain\n"),
	}
	data, err := Pack(files, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(files["skills/tidy.md"]), "---") {
		t.Error("packing changed the caller's files")
	}
	b, err := Read(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []Skill{{Name: "plain", Description: "Say it plainly."}, {Name: "tidy", Description: "Keep it tidy.", Applies: "writing anything"}}
	if !slices.Equal(b.Manifest.Contributes.Skills, want) {
		t.Errorf("skills = %+v", b.Manifest.Contributes.Skills)
	}
	if got := string(b.Files["skills/tidy.md"]); got != "# Tidy\n" {
		t.Errorf("agents see %q: the front matter should come off", got)
	}
}

func TestSkillFilesMustCheckOut(t *testing.T) {
	cases := map[string]map[string][]byte{
		"front matter without a description": {
			"manifest.json":  []byte(`{"id":"pack","name":"Pack","version":"1.0.0","kind":"skills"}`),
			"skills/tidy.md": []byte("---\napplies: anything\n---\n# Tidy\n"),
		},
		"listed skill without its file": {
			"manifest.json": []byte(`{"id":"pack","name":"Pack","version":"1.0.0","kind":"skills","contributes":{"skills":[{"name":"tidy","description":"Tidy."}]}}`),
		},
		"skills plugin with logic": {
			"manifest.json": []byte(`{"id":"pack","name":"Pack","version":"1.0.0","kind":"skills"}`),
			"logic.wasm":    []byte("\x00asm"),
		},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Pack(files, nil); err == nil {
				t.Error("packed")
			}
		})
	}
}

func TestFolderSkills(t *testing.T) {
	files := map[string][]byte{
		"manifest.json":          []byte(`{"id":"pack","name":"Pack","version":"1.0.0","kind":"skills"}`),
		"skills/tdd/SKILL.md":    []byte("---\nname: tdd\ndescription: |\n  Test first.\n  Then the code.\nmetadata:\n  version: \"2\"\n---\n# TDD\nSee tests.md.\n"),
		"skills/tdd/tests.md":    []byte("# Good tests\n"),
		"skills/tdd/refs/a.md":   []byte("# A\n"),
		"skills/review/SKILL.md": []byte("---\ndescription: 'Review it, don''t rush.'\nx-enplace-applies: reviewing a pull request\n---\n# Review\n"),
		"skills/plain.md":        []byte("---\ndescription: Say it plainly.\n---\n# Plain\n"),
	}
	data, err := Pack(files, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Read(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []Skill{
		{Name: "plain", Description: "Say it plainly."},
		{Name: "review", Description: "Review it, don't rush.", Applies: "reviewing a pull request"},
		{Name: "tdd", Description: "Test first.\nThen the code.", Applies: AppliesByRole},
	}
	if !slices.Equal(b.Manifest.Contributes.Skills, want) {
		t.Errorf("skills = %+v", b.Manifest.Contributes.Skills)
	}
	if p, ok := SkillPath(b.Files, "tdd"); !ok || p != "skills/tdd/SKILL.md" || string(b.Files[p]) != "# TDD\nSee tests.md.\n" {
		t.Errorf("tdd is at %q: %q", p, b.Files[p])
	}
	if p, _ := SkillPath(b.Files, "plain"); p != "skills/plain.md" {
		t.Errorf("plain is at %q", p)
	}
	extra := SkillFiles(b.Files, "tdd")
	if len(extra) != 2 || string(extra["tests.md"]) != "# Good tests\n" || extra["refs/a.md"] == nil {
		t.Errorf("tdd's own files = %v", extra)
	}
	if len(SkillFiles(b.Files, "plain")) != 0 {
		t.Error("a one-file skill has no files of its own")
	}
}

func TestSkillPacksAreWordsOnly(t *testing.T) {
	files := map[string][]byte{
		"manifest.json":              []byte(`{"id":"pack","name":"Pack","version":"1.0.0","kind":"skills"}`),
		"skills/bugs/SKILL.md":       []byte("---\ndescription: Find bugs.\n---\n# Bugs\n"),
		"skills/bugs/scripts/run.sh": []byte("rm -rf /\n"),
	}
	if _, err := Pack(files, nil); err == nil || !strings.Contains(err.Error(), "run.sh") {
		t.Errorf("a script in a skill pack: %v", err)
	}
	files = map[string][]byte{
		"manifest.json":        []byte(`{"id":"pack","name":"Pack","version":"1.0.0","kind":"skills"}`),
		"skills/bugs/SKILL.md": []byte("# Bugs, no front matter\n"),
	}
	if _, err := Pack(files, nil); err == nil {
		t.Error("a folder skill needs front matter")
	}
}

func TestUpstreamCredits(t *testing.T) {
	ok := `{"id":"pack","name":"Pack","version":"1.0.0","kind":"skills","license":"MIT",
		"upstream":{"repo":"https://github.com/someone/skills","commit":"5863806dd4e15c63174bee4e7515a4256792ecfa"}}`
	if _, err := ParseManifest([]byte(ok)); err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string]string{
		"no licence":   `{"id":"pack","name":"Pack","version":"1.0.0","kind":"skills","upstream":{"repo":"https://github.com/someone/skills","commit":"5863806dd4e15c63174bee4e7515a4256792ecfa"}}`,
		"not github":   `{"id":"pack","name":"Pack","version":"1.0.0","kind":"skills","license":"MIT","upstream":{"repo":"https://example.com/skills","commit":"5863806dd4e15c63174bee4e7515a4256792ecfa"}}`,
		"short commit": `{"id":"pack","name":"Pack","version":"1.0.0","kind":"skills","license":"MIT","upstream":{"repo":"https://github.com/someone/skills","commit":"5863806"}}`,
		"bad licence":  `{"id":"pack","name":"Pack","version":"1.0.0","kind":"skills","license":"MIT; rm"}`,
	} {
		if _, err := ParseManifest([]byte(raw)); err == nil {
			t.Errorf("%s: should be refused", name)
		}
	}
}

// A service can make test builds, as long as it says how and where they land.
func TestServiceMakesTestBuilds(t *testing.T) {
	m, err := parseDemo(t, `"kind":"service","contributes":{"test_builds":{"action":"test_build","where":"TestFlight"}}`)
	if err != nil || m.Contributes.TestBuilds.Where != "TestFlight" {
		t.Fatalf("%v %+v", err, m)
	}
	if _, err := parseDemo(t, `"kind":"service","contributes":{"test_builds":{"where":"TestFlight"}}`); err == nil || !strings.Contains(err.Error(), "test_builds") {
		t.Fatalf("no action: %v", err)
	}
}
