package bundle

import (
	"strings"
	"testing"
)

const conn = `{"id":"github-account","name":"GitHub","version":"1.0.0","kind":"connection","format":4,
  "permissions":{"hosts":["api.github.com"]},
  "settings":[{"key":"token","name":"Token","type":"secret","scope":"workspace"}],
  "contributes":{"connection":{"service":"github","link":"https://github.com/settings/tokens","scopes":[{"key":"repo"}],"adopts":["github"]}}}`

func TestAConnectionSignsInOnTheWorkspace(t *testing.T) {
	m, err := ParseManifest([]byte(conn))
	if err != nil {
		t.Fatal(err)
	}
	if m.Scope != "workspace" || m.Contributes.Connection.CheckAction() != "check" {
		t.Errorf("scope %q, check %q", m.Scope, m.Contributes.Connection.CheckAction())
	}
	files := map[string][]byte{"manifest.json": []byte(conn)}
	if _, err := Pack(files, nil); err == nil || !strings.Contains(err.Error(), "logic") {
		t.Errorf("packed a connection without logic: %v", err)
	}
}

func TestBadConnections(t *testing.T) {
	cases := map[string]string{
		"no connection":   `{"id":"x-account","name":"X","version":"1.0.0","kind":"connection","settings":[{"key":"t","name":"T","type":"secret","scope":"workspace"}]}`,
		"not a kind":      `{"id":"x","name":"X","version":"1.0.0","kind":"service","contributes":{"connection":{"service":"x"}}}`,
		"project field":   strings.Replace(conn, `"scope":"workspace"`, `"scope":"project"`, 1),
		"no fields":       strings.Replace(conn, `"settings":[{"key":"token","name":"Token","type":"secret","scope":"workspace"}],`, ``, 1),
		"bad service":     strings.Replace(conn, `"service":"github"`, `"service":"Git Hub"`, 1),
		"twice":           `{"id":"x","name":"X","version":"1.0.0","kind":"service","connections":[{"service":"github"},{"service":"github"}]}`,
		"plain link":      strings.Replace(conn, `https://github.com`, `http://github.com`, 1),
		"signs in itself": strings.Replace(conn, `"format":4,`, `"format":4,"connections":[{"service":"apple"}],`, 1),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseManifest([]byte(raw)); err == nil {
				t.Error("parsed")
			}
		})
	}
}

func TestAPluginNamesTheAccountsItSignsInWith(t *testing.T) {
	m, err := ParseManifest([]byte(`{"id":"github-actions","name":"GitHub Actions","version":"1.0.0","kind":"service","format":4,
	  "connections":[{"service":"github","scopes":["repo","workflow"],"for":"runs your workflows"},{"service":"apple","optional":true}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if n, ok := m.NeedsConnection("github"); !ok || len(n.Scopes) != 2 {
		t.Errorf("github: %+v %v", n, ok)
	}
	if _, ok := m.NeedsConnection("google"); ok {
		t.Error("needs google")
	}
}
