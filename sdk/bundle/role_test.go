package bundle

import (
	"strings"
	"testing"
)

func TestRoleKind(t *testing.T) {
	ok := `{"id":"designer-x","name":"Designer","version":"1.0.0","kind":"role","contributes":{"role":{"takes":{"tags":["design"]},"brief":"Make options."}}}`
	m, err := ParseManifest([]byte(ok))
	if err != nil {
		t.Fatal(err)
	}
	if m.Scope != "workspace" || len(m.Levels) != 1 || m.Levels[0] != "workspace" {
		t.Fatalf("kind didn't fill in where it goes: %q %v", m.Scope, m.Levels)
	}
	for _, c := range []struct{ json, want string }{
		{`{"id":"rr","name":"R","version":"1.0.0","kind":"role"}`, "needs contributes.role"},
		{`{"id":"rr","name":"R","version":"1.0.0","kind":"role","contributes":{"role":{"takes":{},"brief":"x"}}}`, "takes needs"},
		{`{"id":"rr","name":"R","version":"1.0.0","kind":"role","contributes":{"role":{"takes":{"anything":true}}}}`, "brief"},
		{`{"id":"rr","name":"R","version":"1.0.0","kind":"role","contributes":{"role":{"takes":{"anything":true},"brief":"x","tone":"pink"}}}`, "tone"},
		{`{"id":"rr","name":"R","version":"1.0.0","kind":"role","contributes":{"role":{"takes":{"tags":["Design!"]},"brief":"x"}}}`, "tag"},
		{`{"id":"rr","name":"R","version":"1.0.0","kind":"role","contributes":{"role":{"takes":{"anything":true},"brief":"x"},"tools":[{"name":"x"}]}}`, "doesn't contribute tools"},
		{`{"id":"rr","name":"R","version":"1.0.0","kind":"extension","levels":["workspace"],"contributes":{"role":{"takes":{"anything":true},"brief":"x"}}}`, "only a role plugin"},
	} {
		if _, err := ParseManifest([]byte(c.json)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %q", c.json, err, c.want)
		}
	}
	if err := checkKindFiles(m, map[string][]byte{"logic.wasm": {0}}); err == nil {
		t.Error("a role plugin with logic packed")
	}
}
