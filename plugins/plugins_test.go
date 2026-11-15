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
			skills := skillFiles(t, id)
			for name, body := range skills {
				files["skills/"+name+".md"] = body
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
			// Every skill file is one the server installs: listed in the
			// manifest, or describing itself in its front matter.
			var listed []string
			for _, sk := range b.Manifest.Contributes.Skills {
				listed = append(listed, sk.Name)
			}
			for name := range skills {
				if !slices.Contains(listed, name) {
					t.Errorf("skills/%s.md is neither listed nor has front matter with a description", name)
				}
			}
		})
	}
}

func skillFiles(t *testing.T, id string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	root := filepath.Join(id, "skills")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return fs.SkipDir
		}
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasSuffix(p, ".md") {
			t.Errorf("%s: skills are Markdown files", p)
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(b)) == "" {
			t.Errorf("%s is empty", p)
		}
		out[strings.TrimSuffix(filepath.Base(p), ".md")] = b
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
