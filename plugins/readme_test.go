package plugins_test

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JustinGuedes/enplace-plugins/sdk/bundle"
)

var update = flag.Bool("update", false, "rewrite the plugin list in README.md")

// kindHeadings are the README's headings for each kind, in the order shown.
var kindHeadings = []struct{ kind, heading string }{
	{bundle.KindAgent, "Agents"},
	{bundle.KindSource, "Work sources"},
	{bundle.KindService, "Services"},
	{bundle.KindExtension, "Extensions"},
	{bundle.KindSkills, "Skill packs"},
	{bundle.KindRole, "Roles"},
}

const (
	listStart = "<!-- plugins: go test ./plugins -update -->\n"
	listEnd   = "<!-- /plugins -->\n"
)

// TestReadmeListsEveryPlugin keeps the README's list of plugins, grouped
// by kind, in step with the folders.
func TestReadmeListsEveryPlugin(t *testing.T) {
	byKind := map[string][]*bundle.Manifest{}
	for _, id := range folders(t) {
		raw, err := os.ReadFile(filepath.Join(id, "manifest.json"))
		if err != nil {
			t.Fatal(err)
		}
		m, err := bundle.ParseManifest(raw)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		byKind[m.Kind] = append(byKind[m.Kind], m)
	}
	var list strings.Builder
	for i, k := range kindHeadings {
		if i > 0 {
			list.WriteString("\n")
		}
		fmt.Fprintf(&list, "### %s\n\n", k.heading)
		for _, m := range byKind[k.kind] {
			if m.ReplacedBy != "" {
				continue // the store no longer offers it
			}
			fmt.Fprintf(&list, "- [%s](plugins/%s): %s\n", m.Name, m.ID, m.Summary)
		}
		delete(byKind, k.kind)
	}
	for kind := range byKind {
		t.Errorf("kind %q has no heading in kindHeadings", kind)
	}

	path := filepath.Join("..", "README.md")
	readme, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	before, rest, ok := strings.Cut(string(readme), listStart)
	_, after, ok2 := strings.Cut(rest, listEnd)
	if !ok || !ok2 {
		t.Fatalf("README.md needs the markers %q and %q", listStart, listEnd)
	}
	want := before + listStart + "\n" + list.String() + "\n" + listEnd + after
	if *update {
		if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if string(readme) != want {
		t.Error("README.md's plugin list is out of date: run go test ./plugins -update")
	}
}
