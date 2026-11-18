package bundle

import (
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"
)

// Kinds of plugin. A kind says what a plugin is for: it fills in where the
// plugin can be switched on and how it flows down, and keeps it to the
// pieces that kind uses, so a manifest only says what's its own. Every
// plugin names one; a bundle from before kinds reads as KindExtension.
const (
	// KindSkills brings skills for agents, and nothing else: no logic.
	// Each skills/<name>.md describes itself in its front matter, so the
	// manifest needn't list them.
	KindSkills = "skills"
	// KindAgent does work items for people (Send to Claude, Codex…):
	// contributes.runner, or contributes.ways for one agent with several
	// ways to connect, on the workspace.
	KindAgent = "agent"
	// KindSource keeps a project's work in step with another tool, like
	// Linear or GitHub Issues. It builds on the work-sync plugin.
	KindSource = "source"
	// KindService follows something a product ships to: a store, a site,
	// a repo.
	KindService = "service"
	// KindExtension adds a feature across Enplace, like the release cycle
	// or XP. It may use every piece and fills nothing in: the manifest
	// says where it goes with Levels.
	KindExtension = "extension"
	// KindRole is a job on the person's team, like Designer or App shipper:
	// contributes.role says what work it takes and what its agent is told
	// to do with it, and its skills (its own, and those of the skills
	// plugins it uses) go to the agent on every item. No logic.
	KindRole = "role"
)

// SkillFileTypes are the kinds of file a skills plugin's skills may hold.
var SkillFileTypes = []string{".md", ".txt", ".json", ".csv", ".tsv", ".yaml", ".yml", ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg"}

// kindRule is what a kind fills in and allows.
type kindRule struct {
	scope   string
	levels  []string
	inherit string
	extends []string
	// pieces are the contributes keys the kind may use; nil allows all.
	pieces []string
}

var kinds = map[string]kindRule{
	KindSkills: {
		scope: "both", levels: []string{"workspace", "project", "product"}, inherit: InheritDown,
		pieces: []string{"skills"},
	},
	KindAgent: {
		scope:  "workspace",
		pieces: []string{"runner", "ways"},
	},
	KindSource: {
		scope: "project", levels: []string{"project", "product"}, inherit: InheritOffer,
		extends: []string{"work-sync"},
		pieces: []string{"brief", "events", "project_cards", "project_status", "schedule", "settings_panel",
			"skills", "tools", "webhook", "work_fields", "work_panel", "work_views"},
	},
	KindService: {
		scope: "project", levels: []string{"product"}, inherit: InheritNone,
		pieces: []string{"brief", "calendar", "deadlines", "events", "pages", "project_cards", "project_status",
			"schedule", "settings_panel", "skills", "tools", "webhook", "work_fields", "work_panel"},
	},
	KindExtension: {},
	KindRole: {
		scope: "workspace", levels: []string{"workspace"},
		pieces: []string{"role", "skills"},
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
		m.Kind = KindExtension
	}
	rule, ok := kinds[m.Kind]
	if !ok {
		return fmt.Errorf("kind must be one of %s", strings.Join(KindNames(), ", "))
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
	if m.Kind != KindRole && m.Contributes.Role != nil {
		return fmt.Errorf("only a role plugin contributes role")
	}
	if rule.pieces == nil {
		return nil
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
	if m.Kind == KindRole && m.Contributes.Role == nil {
		return fmt.Errorf("a role plugin needs contributes.role")
	}
	if m.Kind == KindAgent && m.Contributes.Runner == nil && len(m.Contributes.Ways) == 0 {
		return fmt.Errorf("an agent plugin needs contributes.runner or contributes.ways")
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

// checkKindFiles checks the files a kind allows: a skills or role plugin
// has no logic and holds words and pictures only. It runs where the files are known, packing and reading.
func checkKindFiles(m *Manifest, files map[string][]byte) error {
	if m.Kind != KindSkills && m.Kind != KindRole {
		return nil
	}
	if HasLogic(files) {
		return fmt.Errorf("a %s plugin has no logic: remove logic.wasm", m.Kind)
	}
	// Words only: nothing an agent could be told to run comes with a skill
	// pack, so packs can update by themselves.
	for p := range files {
		if strings.HasPrefix(p, "skills/") && !slices.Contains(SkillFileTypes, strings.ToLower(path.Ext(p))) {
			return fmt.Errorf("%s: a %s plugin holds words and pictures only (%s)", p, m.Kind, strings.Join(SkillFileTypes, " "))
		}
	}
	return nil
}
