package bundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGlyphCheck(t *testing.T) {
	good := Glyph{{D: "M12 2.5L20 7L20 17Z", Paint: "soft"}, {D: "M 8.5,12 Q 12 8.5 15.5 12 C1 2 3 4 5 6"}}
	if err := good.Check(); err != nil {
		t.Fatal(err)
	}
	if err := (Glyph{}).Check(); err != nil {
		t.Fatal("a plugin without a glyph is fine:", err)
	}
	bad := map[string]Glyph{
		"relative commands":  {{D: "M1 1l2 2"}},
		"arcs":               {{D: "M1 1A2 2 0 0 1 3 3"}},
		"off the grid":       {{D: "M1 1L30 2"}},
		"negative numbers":   {{D: "M1 1L-3 2"}},
		"no leading move":    {{D: "L1 1"}},
		"half a command":     {{D: "M1 1L3"}},
		"markup in a path":   {{D: `M1 1"/><script>`}},
		"an unknown paint":   {{D: "M1 1L2 2", Paint: "url(#x)"}},
		"an empty path":      {{D: ""}},
		"too long a path":    {{D: "M1 1" + strings.Repeat("L2 2", 400)}},
		"too many layers":    make(Glyph, MaxGlyphLayers+1),
		"numbers, no letter": {{D: "M1 1 2 2 3"}},
	}
	for name, g := range bad {
		if g.Check() == nil {
			t.Errorf("%s should be refused", name)
		}
	}
}

// Every plugin that comes with Enplace draws its own icon, and no two are
// the same.
func TestShippedPluginsHaveTheirOwnGlyph(t *testing.T) {
	dirs, _ := filepath.Glob("../../plugins/*/manifest.json")
	if len(dirs) == 0 {
		t.Fatal("no plugins found")
	}
	seen := map[string]string{}
	for _, p := range dirs {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		m, err := ParseManifest(raw)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if len(m.Glyph) == 0 {
			t.Errorf("%s has no glyph", m.ID)
		}
		var all []string
		for _, l := range m.Glyph {
			all = append(all, l.D)
		}
		key := strings.Join(all, "|")
		if other, dup := seen[key]; dup {
			t.Errorf("%s and %s share a glyph", m.ID, other)
		}
		seen[key] = m.ID
	}
}

func TestMergeKeepsParentGlyph(t *testing.T) {
	parent := Manifest{Glyph: Glyph{{D: "M1 1L2 2"}}}
	if got := Merge(parent, Manifest{}); len(got.Glyph) != 1 {
		t.Fatal("a plugin without a glyph uses the one it extends")
	}
	if got := Merge(parent, Manifest{Glyph: Glyph{{D: "M3 3L4 4"}}}); got.Glyph[0].D != "M3 3L4 4" {
		t.Fatal("a plugin's own glyph wins")
	}
}
