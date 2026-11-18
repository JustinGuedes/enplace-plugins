// Package plugins_test checks every plugin folder the way a server sees it
// once packed: a valid manifest, its skills, and a bundle that reads back.
package plugins_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/JustinGuedes/enplace-plugins/sdk/bundle"
)

func folders(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	if len(out) == 0 {
		t.Fatal("no plugins found")
	}
	return out
}

func TestEveryPluginPacks(t *testing.T) {
	for _, id := range folders(t) {
		t.Run(id, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(id, "manifest.json"))
			if err != nil {
				t.Fatal(err)
			}
			m, err := bundle.ParseManifest(raw)
			if err != nil {
				t.Fatal(err)
			}
			if m.ID != id {
				t.Errorf("manifest id %q, folder %q: they must match", m.ID, id)
			}
			if !strings.Contains(string(raw), `"kind"`) {
				t.Error("name the plugin's kind in its manifest")
			}
			if m.Summary == "" {
				t.Error("a summary is shown in the store: add one")
			}

			files := map[string][]byte{"manifest.json": raw}
			for name, body := range iconFiles(t, id) {
				files[name] = body
			}
			skills := skillFiles(t, id)
			for name, body := range skills {
				files[name] = body
			}
			if b, err := os.ReadFile(filepath.Join(id, "LICENSE")); err == nil {
				files["LICENSE"] = b
			}
			if _, err := os.Stat(filepath.Join(id, "logic")); err == nil {
				// The real logic is built for WASI in CI; any bytes stand in here.
				files["logic.wasm"] = []byte("\x00asm\x01\x00\x00\x00")
			}

			data, err := bundle.Pack(files, nil)
			if err != nil {
				t.Fatalf("packing: %v", err)
			}
			b, err := bundle.Read(data, nil)
			if err != nil {
				t.Fatalf("reading the bundle back: %v", err)
			}
			if b.Manifest.ID != id || b.Manifest.Version != m.Version {
				t.Errorf("bundle reads back as %s %s", b.Manifest.ID, b.Manifest.Version)
			}
			if len(b.Manifest.Glyph) == 0 && len(m.Extends) == 0 {
				t.Error("draw the plugin's icon in icon.svg")
			}
			// Every skill file is one the server installs: listed in the
			// manifest, or describing itself in its front matter.
			var listed []string
			for _, sk := range b.Manifest.Contributes.Skills {
				listed = append(listed, sk.Name)
			}
			for p := range skills {
				name, ok := skillName(p)
				if ok && !slices.Contains(listed, name) {
					t.Errorf("%s is neither listed nor has front matter with a description", p)
				}
			}
			if m.Upstream != nil {
				if _, err := os.Stat(filepath.Join(id, "LICENSE")); err != nil {
					t.Error("a pack copied from someone's repo keeps their LICENSE beside the manifest")
				}
				if _, err := os.Stat(filepath.Join(id, "upstream.json")); err != nil {
					t.Error("say which of the repo's skills to take in upstream.json")
				}
			}
		})
	}
}

// iconFiles are the plugin's icon.svg and icons/<page>.svg.
func iconFiles(t *testing.T, id string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	if b, err := os.ReadFile(filepath.Join(id, "icon.svg")); err == nil {
		out["icon.svg"] = b
	}
	pages, _ := os.ReadDir(filepath.Join(id, "icons"))
	for _, f := range pages {
		b, err := os.ReadFile(filepath.Join(id, "icons", f.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out["icons/"+f.Name()] = b
	}
	return out
}

// skillFiles are every file under the plugin's skills/, by its path in the
// bundle.
func skillFiles(t *testing.T, id string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	err := filepath.WalkDir(filepath.Join(id, "skills"), func(p string, d fs.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return fs.SkipDir
		}
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(b)) == "" {
			t.Errorf("%s is empty", p)
		}
		rel, _ := filepath.Rel(id, p)
		out[filepath.ToSlash(rel)] = b
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// skillName is the skill a file is, when it's one: skills/<name>.md or
// skills/<name>/SKILL.md.
func skillName(p string) (string, bool) {
	parts := strings.Split(p, "/")
	switch {
	case len(parts) == 2 && strings.HasSuffix(parts[1], ".md"):
		return strings.TrimSuffix(parts[1], ".md"), true
	case len(parts) == 3 && parts[2] == bundle.SkillFile:
		return parts[1], true
	}
	return "", false
}

// A role is built from skills that exist: every plugin it uses is a
// skills plugin here.
func TestRolesUseSkillsPlugins(t *testing.T) {
	for _, id := range folders(t) {
		raw, err := os.ReadFile(filepath.Join(id, "manifest.json"))
		if err != nil {
			t.Fatal(err)
		}
		m, err := bundle.ParseManifest(raw)
		if err != nil || m.Kind != bundle.KindRole {
			continue
		}
		for _, use := range m.Contributes.Role.Uses {
			raw, err := os.ReadFile(filepath.Join(use, "manifest.json"))
			if err != nil {
				t.Errorf("%s uses %s, which isn't a plugin here", id, use)
				continue
			}
			if u, err := bundle.ParseManifest(raw); err != nil || u.Kind != bundle.KindSkills {
				t.Errorf("%s uses %s, which isn't a skills plugin", id, use)
			}
		}
	}
}
