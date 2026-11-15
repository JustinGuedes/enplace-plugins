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

func TestNoKindStillNeedsNoScope(t *testing.T) {
	cases := map[string]string{
		`"levels":["workspace"]`:              "workspace",
		`"levels":["workspace","project"]`:    "both",
		`"levels":["project","product"]`:      "project",
		`"permissions":{"read":["work"]}`:     "project",
		`"scope":"both","levels":["product"]`: "both",
	}
	for body, scope := range cases {
		m, err := parseDemo(t, body)
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if m.Scope != scope {
			t.Errorf("%s: scope %s, want %s", body, m.Scope, scope)
		}
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
