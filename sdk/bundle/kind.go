package bundle

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Kinds of plugin. A kind says what a plugin is for: it fills in where the
// plugin can be switched on and how it flows down, and keeps it to the
// pieces that kind uses, so a manifest only says what's its own. A plugin
// without a kind can use every piece.
const (
	// KindSkills brings skills for agents, and nothing else: no logic.
	// Each skills/<name>.md describes itself in its front matter, so the
	// manifest needn't list them.
	KindSkills = "skills"
	// KindAgent does work items for people (Send to Claude, Codex…):
	// contributes.runner, on the workspace.
	KindAgent = "agent"
	// KindSource keeps a project's work in step with another tool, like
	// Linear or GitHub Issues. It builds on the work-sync plugin.
	KindSource = "source"
	// KindService follows something a product ships to: a store, a site,
	// a repo.
	KindService = "service"
)

// kindRule is what a kind fills in and allows.
type kindRule struct {
	scope   string
	levels  []string
	inherit string
	extends []string
	// pieces are the contributes keys the kind may use.
	pieces []string
}

var kinds = map[string]kindRule{
	KindSkills: {
		scope: "both", levels: []string{"workspace", "project", "product"}, inherit: InheritDown,
		pieces: []string{"skills"},
	},
	KindAgent: {
		scope:  "workspace",
		pieces: []string{"runner"},
	},
	KindSource: {
		scope: "project", levels: []string{"project", "product"}, inherit: InheritOffer,
		extends: []string{"work-sync"},
		pieces: []string{"brief", "events", "project_cards", "project_status", "schedule", "settings_panel",
			"skills", "tools", "webhook", "work_fields", "work_panel"},
	},
	KindService: {
		scope: "project", levels: []string{"product"}, inherit: InheritNone,
		pieces: []string{"brief", "calendar", "deadlines", "events", "pages", "project_cards", "project_status",
			"schedule", "settings_panel", "skills", "tools", "webhook", "work_fields", "work_panel"},
	},
}

// KindNames are the kinds a manifest can name, in order.
func KindNames() []string {
	names := make([]string, 0, len(kinds))
	for k := range kinds {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// applyKind fills in what the manifest's kind decides and it left out,
// then checks it only uses the kind's pieces.
func (m *Manifest) applyKind() error {
	if m.Kind == "" {
		return nil
	}
	rule, ok := kinds[m.Kind]
	if !ok {
		return fmt.Errorf("kind must be one of %s, or left out", strings.Join(KindNames(), ", "))
	}
	if m.Scope == "" {
		m.Scope = rule.scope
	}
	if len(m.Levels) == 0 && rule.levels != nil {
		m.Levels = slices.Clone(rule.levels)
	}
	if m.Inherit == "" {
		m.Inherit = rule.inherit
	}
	if len(m.Extends) == 0 && rule.extends != nil {
		m.Extends = slices.Clone(rule.extends)
	}
	used, err := m.Contributes.pieces()
	if err != nil {
		return err
	}
	for _, p := range used {
		if !slices.Contains(rule.pieces, p) {
			return fmt.Errorf("a %s plugin doesn't contribute %s", m.Kind, p)
		}
	}
	if m.Kind == KindAgent && m.Contributes.Runner == nil {
		return fmt.Errorf("an agent plugin needs contributes.runner")
	}
	return nil
}

// pieces are the contributes keys that are set.
func (c Contributes) pieces() ([]string, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	var set map[string]json.RawMessage
	if err := json.Unmarshal(raw, &set); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, nil
}

// checkKindFiles checks the files a kind allows: a skills plugin has no
// logic. It runs where the files are known, packing and reading.
func checkKindFiles(m *Manifest, files map[string][]byte) error {
	if m.Kind == KindSkills && HasLogic(files) {
		return fmt.Errorf("a skills plugin has no logic: remove logic.wasm")
	}
	return nil
}
