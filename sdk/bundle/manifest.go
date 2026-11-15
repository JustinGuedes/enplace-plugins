package bundle

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Manifest is manifest.json: everything Enplace needs to know about a plugin
// before running any of it. Only id, name and version are required; a Kind
// fills in most of the rest.
type Manifest struct {
	// Schema points editors at schema/manifest.schema.json. Enplace
	// ignores it.
	Schema string `json:"$schema,omitempty"`

	// Who the plugin is.

	// ID is 2-40 lowercase letters, digits or dashes, and never changes.
	ID string `json:"id"`
	// Name is shown everywhere the plugin is, up to 40 characters.
	Name string `json:"name"`
	// Version is semantic (1.4.0). Servers only pick up a new version.
	Version string `json:"version"`
	// Kind is what the plugin is for: KindSkills, KindAgent, KindSource or
	// KindService. It fills in Scope, Levels, Inherit and Extends when
	// they're left out, and keeps the plugin to that kind's pieces. Left
	// out, the plugin may use every piece and says where it goes itself.
	Kind string `json:"kind,omitempty"`
	// Author, Summary (one line in the store), Description (its page in
	// the store) and Homepage describe it to people.
	Author      string `json:"author,omitempty"`
	Summary     string `json:"summary,omitempty"`
	Description string `json:"description,omitempty"`
	Homepage    string `json:"homepage,omitempty"`

	// How it looks.

	// Colour is the plugin's colour, like #ffc069.
	Colour string `json:"colour,omitempty"`
	// Icon names a symbol from Enplace's shared set, used when the plugin
	// has no Glyph of its own.
	Icon  string `json:"icon,omitempty"`
	Glyph Glyph  `json:"glyph,omitempty"`

	// Where it goes.

	// Levels say where the plugin can be switched on: "workspace",
	// "project" (a project as a whole) and "product" (one of the things a
	// project ships, like its iPhone app or its website).
	Levels []string `json:"levels,omitempty"`
	// Scope is how the plugin is installed: "workspace" (the whole
	// workspace), "project" (switched on per project or product) or
	// "both". Left out, it follows Kind, or else Levels.
	Scope string `json:"scope,omitempty"`
	// Inherit says how the plugin flows down the levels, workspace ›
	// project › product, when it's on at one of them:
	//   - "down": on above means on below. A level below can switch it off
	//     for itself (XP, release lanes).
	//   - "offer": a product is covered by its project's, sharing its
	//     settings, until it takes its own; from the workspace, a project
	//     is only offered it (release cycle, GitHub).
	//   - "none" (the default): it stays where it's switched on (App Store).
	// A product that takes its own starts from a copy of its project's
	// settings.
	Inherit string `json:"inherit,omitempty"`
	// SuggestFor are product types the plugin is suggested for when a
	// product of that type is added, like ["app"] for App Store.
	SuggestFor []string `json:"suggest_for,omitempty"`
	// Default: new workspaces start with it. Only honoured for the plugins
	// that come with Enplace.
	Default bool `json:"default,omitempty"`
	// Base marks a plugin that only exists to be extended, like the sync
	// engine work sources build on. It's never switched on by itself: its
	// settings, contributions and permissions only take effect folded into
	// each plugin that extends it.
	Base bool `json:"base,omitempty"`

	// What it builds on.

	// Extends names plugins this one builds on: it inherits their
	// settings, cards, pages and permissions (and, from a bundle, its files
	// and logic) and overrides what it sets itself. It also counts as them,
	// so a plugin extending "releases" gets the release cycle. Pin versions
	// of what it extends in Requires.
	Extends []string `json:"extends,omitempty"`
	// Requires are plugins that must be installed alongside, with the
	// versions this one works with.
	Requires []Dependency `json:"requires,omitempty"`

	// What it may do and what it adds.

	// Permissions are what the plugin may read and which changes it may ask
	// for. Asking still goes through the person's rules and Approvals.
	Permissions Permissions `json:"permissions"`
	// Settings are fields on the plugin's settings page.
	Settings []Setting `json:"settings,omitempty"`
	// CardSettings are the keys of the settings that sum the plugin up on
	// its card in Settings, like ["repo", "branch"] for "owner/app · main".
	// Left out, the card shows the first setting that has a value.
	CardSettings []string `json:"card_settings,omitempty"`
	// Contributes are the pieces of Enplace the plugin fills.
	Contributes Contributes `json:"contributes"`
}

// Inherit values (see Manifest.Inherit).
const (
	InheritDown  = "down"
	InheritOffer = "offer"
	InheritNone  = "none"
)

// Flows reports how the plugin flows down: down, offer or none.
func (m Manifest) Flows() string {
	switch m.Inherit {
	case InheritDown, InheritOffer:
		return m.Inherit
	}
	return InheritNone
}

// LevelNames are the places a plugin can be switched on.
var LevelNames = []string{"workspace", "project", "product"}

// OnLevels are where the plugin can be switched on (see Levels).
func (m Manifest) OnLevels() []string {
	if len(m.Levels) > 0 && m.Scope != "workspace" {
		out := slices.Clone(m.Levels)
		if m.Scope == "both" && !slices.Contains(out, "workspace") {
			out = append([]string{"workspace"}, out...)
		}
		return out
	}
	var out []string
	switch m.Scope {
	case "workspace":
		return []string{"workspace"}
	case "both":
		out = []string{"workspace", "project"}
	default:
		out = []string{"project"}
	}
	return out
}

// scopeFor is the scope that goes with levels: on the workspace alone,
// the workspace and below, or projects and products.
func scopeFor(levels []string) string {
	switch {
	case slices.Equal(levels, []string{"workspace"}):
		return "workspace"
	case slices.Contains(levels, "workspace"):
		return "both"
	}
	return "project"
}

// On says whether the plugin can be switched on at a level.
func (m Manifest) On(level string) bool { return slices.Contains(m.OnLevels(), level) }

// Dependency is another plugin and a version range, like "^1.2".
type Dependency struct {
	ID      string `json:"id"`
	Version string `json:"version,omitempty"`
	// Optional links the two plugins without needing the other: neither is
	// switched on with the other, but where both are on they read what the
	// other publishes (Request.Peers). A plugin that can do more when
	// Release cycle is there, but works without it, lists it this way.
	Optional bool `json:"optional,omitempty"`
}

// Permissions a plugin asks for.
type Permissions struct {
	// Read: "projects" (names, colours, settings), "work" (work items in
	// the projects the plugin is on) and "memory" (the memory notes a memory
	// engine searches when it answers recall).
	Read []string `json:"read,omitempty"`
	// Ops are the changes it may propose, like "work.create". They are
	// filed through the gate as the plugin, so the rules decide.
	Ops []string `json:"ops,omitempty"`
	// Hosts are the outside hosts its logic may fetch from, like
	// "api.example.com" or "*.example.com". Hosts a person types into a
	// setting marked Fetch are allowed too.
	Hosts []string `json:"hosts,omitempty"`
	// FetchLinks lets it fetch any public link a person gave it and it
	// keeps in its vault, like a calendar's private address.
	FetchLinks bool `json:"fetch_links,omitempty"`
	// AI lets its logic ask for small AI jobs through Enplace's queue
	// (plugin.AIAsk), on whoever the person chose to do them.
	AI bool `json:"ai,omitempty"`
}

// Setting is one field on the plugin's settings page.
type Setting struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	// Type: text, number, toggle, select, url, urls (one per line) or
	// secret. Secrets are stored encrypted with the workspace's other
	// secrets, never shown again, and only handed to the logic.
	Type    string   `json:"type"`
	Scope   string   `json:"scope"` // workspace or project
	Options []string `json:"options,omitempty"`
	Default any      `json:"default,omitempty"`
	Help    string   `json:"help,omitempty"`
	// Multiline shows a bigger box, for things like a pasted key file.
	Multiline bool `json:"multiline,omitempty"`
	// Fetch lets the logic fetch from the hosts of the links a person
	// puts in this setting (url, urls, text or secret).
	Fetch bool `json:"fetch,omitempty"`
	// Allows ties a project select to the plugin's permission: while the
	// person has it set to When, the plugin may take Action in that
	// project without asking (a rule they can see and change in
	// Permissions). Choosing another option puts the rule back.
	Allows *SettingAllows `json:"allows,omitempty"`
	// Level limits a project setting to "project" (the project as a whole)
	// or "product" (a product with the plugin on itself); empty is both.
	Level string `json:"level,omitempty"`
	// HideWhen names a toggle setting that, while it's on, makes this one
	// not count, so it isn't shown (like dates taken from elsewhere).
	HideWhen string `json:"hide_when,omitempty"`
}

// SettingAllows is what choosing one option of a setting allows.
type SettingAllows struct {
	When   string `json:"when"`
	Action string `json:"action"` // "create": adding work
}

// Setting types.
var SettingTypes = []string{"text", "number", "toggle", "select", "url", "urls", "secret"}

// Contributes are the extension points a plugin fills. Core draws each
// place; the plugin only supplies what goes in it.
type Contributes struct {
	// Brief: a sentence in Today's brief, from the plugin's logic.
	Brief bool `json:"brief,omitempty"`
	// RailChip: a small live card on the main rail, from the logic.
	RailChip bool `json:"rail_chip,omitempty"`
	// Dashboard: cards on Today.
	Dashboard []Card `json:"dashboard,omitempty"`
	// ProjectCards: cards on a project's overview, where it's switched on.
	ProjectCards []Card `json:"project_cards,omitempty"`
	// Pages: whole pages, with a nav item in the main rail (workspace) or
	// a project's rail (project).
	Pages []Page `json:"pages,omitempty"`
	// Look: design tokens for the workspace, like "--accent".
	Look *Look `json:"look,omitempty"`
	// Events: work events the logic wants: work.created, work.moved,
	// work.done, work.updated.
	Events []string `json:"events,omitempty"`
	// Schedule runs the logic's schedule hook every few minutes, where it
	// can fetch from outside and store what it found.
	Schedule *Schedule `json:"schedule,omitempty"`
	// Calendar: the logic supplies busy times shown on Calendar and Today.
	Calendar bool `json:"calendar,omitempty"`
	// Deadlines: the logic says when work has to be done by (render slot
	// "deadlines"), so arranging the day puts it first.
	Deadlines bool `json:"deadlines,omitempty"`
	// SettingsPanel: blocks at the top of the plugin's settings, in the
	// workspace and in each project (render slot "settings_panel"), for
	// things like a "Check now" or "Export" button.
	SettingsPanel bool `json:"settings_panel,omitempty"`
	// WorkPanel: blocks on each work item's panel, in the projects the
	// plugin is on (render slot "work_panel", with Request.WorkID and
	// WorkKey), like the code that mentions the item.
	WorkPanel bool `json:"work_panel,omitempty"`
	// WorkFields are fields the plugin adds to work in the projects it's on,
	// stored on each item as "<plugin id>.<key>". The logic supplies a
	// choice field's choices (render slot "work_fields").
	WorkFields []WorkField `json:"work_fields,omitempty"`
	// WorkViews are ways to lay out a project's work, beside Board and
	// List. The logic groups the work (the view hook); Enplace draws it.
	WorkViews []WorkView `json:"work_views,omitempty"`
	// ProjectStatus: the logic sets the status line and progress ring of
	// the projects it's on.
	ProjectStatus bool `json:"project_status,omitempty"`
	// Tools are read-only tools agents get through the MCP: each runs one
	// of the logic's actions and returns its data.
	Tools []Tool `json:"tools,omitempty"`
	// Skills are skills the plugin brings, from its skills/ folder.
	Skills []Skill `json:"skills,omitempty"`
	// Recall makes the plugin a memory engine: when an agent calls recall
	// at a level the plugin is on, Enplace runs the logic's "recall" action
	// (read-only, args {"query": "..."}) and adds what it answers (Data as
	// a list of {name, summary, body}) to its own matches.
	Recall bool `json:"recall,omitempty"`
	// Runner makes the plugin a way to run work: Send hands a work item to
	// the agent it reaches (see Runner).
	Runner *Runner `json:"runner,omitempty"`
	// Webhook lets an outside service tell the plugin something changed,
	// for each project (or product) it's on: Enplace gives each one its
	// own address and signing secret, checks every delivery's signature
	// itself, and only then calls the webhook hook.
	Webhook *Webhook `json:"webhook,omitempty"`
}

// Webhook says how an outside service signs what it sends: an HMAC-SHA256
// of the body with the project's secret, in hex, in a header, after an
// optional prefix (GitHub: X-Hub-Signature-256 and "sha256=").
type Webhook struct {
	SignatureHeader string `json:"signature_header"`
	SignaturePrefix string `json:"signature_prefix,omitempty"`
	// EventHeader names the header saying what happened (X-GitHub-Event),
	// shown as the last delivery's event.
	EventHeader string `json:"event_header,omitempty"`
	// Connect is an action Enplace runs right after the webhook is turned
	// on, with Request.Webhook holding the address and secret, so the
	// plugin can register it with the service itself. Disconnect runs
	// before it's turned off. Both optional.
	Connect    string `json:"connect,omitempty"`
	Disconnect string `json:"disconnect,omitempty"`
}

// WorkField is a field a plugin adds to work.
type WorkField struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	// Type is "choice" (one of the choices the plugin offers), "toggle"
	// (on or off) or "label": a value only the plugin sets, shown as it is
	// on cards (like "LIN-142" for work linked to Linear) and never offered
	// for people to edit.
	Type string `json:"type"`
	Icon string `json:"icon,omitempty"`
	// OnCard shows it on work cards.
	OnCard bool `json:"on_card,omitempty"`
	// GoalScope lets a project goal be for one of a choice field's choices,
	// like a goal for one release.
	GoalScope string `json:"goal_scope,omitempty"` // what such a goal is called, like "A release"
}

// WorkView is a work layout a plugin adds.
type WorkView struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Icon string `json:"icon,omitempty"`
}

// Tool is a read-only MCP tool.
type Tool struct {
	Name        string      `json:"name"` // like list_releases
	Description string      `json:"description"`
	Action      string      `json:"action"`
	Params      []ToolParam `json:"params,omitempty"`
	// Project: the tool is for one project (the agent passes its key).
	Project bool `json:"project,omitempty"`
}

// ToolParam is a text argument of a tool.
type ToolParam struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required,omitempty"`
}

// Skill is a skill a plugin brings: skills/<name>.md in the bundle.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Applies     string `json:"applies,omitempty"`
}

// Schedule says how often the schedule hook runs.
type Schedule struct {
	Every int `json:"every"` // minutes, 5 to 1440
	// Per: "workspace" (one call) or "project" (one call for each project
	// the plugin is on).
	Per string `json:"per"`
}

// Card is a dashboard or project overview card. Its content comes from
// the logic as blocks, which Enplace draws with its own components on
// every platform.
type Card struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Size string `json:"size,omitempty"` // small, wide or tall
	// Where puts a project card somewhere other than the overview:
	// "work" draws it at the top of the project's work (for things that
	// wait to become work). Project cards only.
	Where string `json:"where,omitempty"`
	// Together lets a person put this project card from several projects
	// into one card on Today. Project cards only.
	Together *Together `json:"together,omitempty"`
}

// Together says how one project card reads across several projects.
type Together struct {
	// How is "sum" (add the numbers up), "average" or "list" (each
	// project's value, no total). Numbers come from each project's
	// Signal.Number; values from Signal.Value.
	How string `json:"how"`
	// Label says what the total is, like "new downloads this week".
	Label string `json:"label,omitempty"`
}

// Page is a page with its own nav item, drawn from blocks like cards.
type Page struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Icon string `json:"icon,omitempty"`
	// Glyph is this page's own icon; without one it uses the plugin's.
	Glyph Glyph  `json:"glyph,omitempty"`
	Where string `json:"where"` // workspace or project
	// Nav "main" puts a workspace page beside Today, Calendar and All work.
	// Without it, the page opens from the plugin's rail chip, and only gets
	// a nav item of its own when the plugin has no chip.
	Nav string `json:"nav,omitempty"`
}

// Look changes design tokens. Optional looks get a switch on the plugin's
// settings page, off until someone turns them on.
type Look struct {
	Tokens   map[string]string `json:"tokens"`
	Optional bool              `json:"optional,omitempty"`
}

// Events plugins can ask for.
var Events = []string{"work.created", "work.updated", "work.moved", "work.done"}

// ReadScopes plugins can ask for.
var ReadScopes = []string{"projects", "work", "memory"}

var (
	idRe     = regexp.MustCompile(`^[a-z][a-z0-9-]{1,39}$`)
	keyRe    = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,39}$`)
	tokenRe  = regexp.MustCompile(`^--[a-z][a-z0-9-]{0,39}$`)
	colourRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	// Token values may not break out of a CSS declaration.
	tokenValRe = regexp.MustCompile(`^[A-Za-z0-9#%.,() _-]{1,80}$`)
)

// Reserved ids can't be taken by a bundle. Core also refuses the ids of its
// built-in plugins.
var Reserved = []string{"enplace", "core", "settings", "plugins", "today"}

// ParseManifest reads and checks manifest.json.
func ParseManifest(raw []byte) (*Manifest, error) {
	if len(raw) == 0 {
		return nil, errors.New("the manifest is empty")
	}
	var m Manifest
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("manifest.json: %w", err)
	}
	if err := m.Check(); err != nil {
		return nil, fmt.Errorf("manifest.json: %w", err)
	}
	return &m, nil
}

// Check validates a manifest.
func (m *Manifest) Check() error {
	if !idRe.MatchString(m.ID) {
		return fmt.Errorf("id %q must be 2-40 lowercase letters, digits or dashes", m.ID)
	}
	if slices.Contains(Reserved, m.ID) {
		return fmt.Errorf("id %q is built into Enplace", m.ID)
	}
	m.Name = strings.TrimSpace(m.Name)
	if m.Name == "" || len(m.Name) > 40 {
		return errors.New("name must be 1-40 characters")
	}
	if _, err := ParseVersion(m.Version); err != nil {
		return fmt.Errorf("version: %w", err)
	}
	if m.Colour == "" {
		m.Colour = "#b79cff"
	}
	if !colourRe.MatchString(m.Colour) {
		return errors.New("colour must look like #ffc069")
	}
	if err := m.Glyph.Check(); err != nil {
		return err
	}
	if len(m.Summary) > 200 || len(m.Description) > 2000 {
		return errors.New("summary or description is too long")
	}
	if err := m.applyKind(); err != nil {
		return err
	}
	if m.Scope == "" {
		m.Scope = scopeFor(m.Levels)
	}
	switch m.Scope {
	case "workspace", "project", "both":
	default:
		return errors.New(`scope must be "workspace", "project" or "both"`)
	}
	for i, l := range m.Levels {
		if !slices.Contains(LevelNames, l) || slices.Contains(m.Levels[:i], l) {
			return fmt.Errorf("levels are %s, each once", strings.Join(LevelNames, ", "))
		}
	}
	if m.Scope == "workspace" && slices.ContainsFunc(m.Levels, func(l string) bool { return l != "workspace" }) {
		return errors.New("a workspace plugin can't be on a project or a product")
	}
	if m.Scope != "workspace" && len(m.Levels) > 0 && !slices.Contains(m.Levels, "project") && !slices.Contains(m.Levels, "product") {
		return errors.New("a project plugin is on projects, products or both")
	}
	if m.Scope == "project" && slices.Contains(m.Levels, "workspace") {
		return errors.New(`a plugin on the workspace as well has scope "both"`)
	}
	if len(m.SuggestFor) > 8 {
		return errors.New("suggest_for lists up to 8 product types")
	}
	for _, t := range m.SuggestFor {
		if !idRe.MatchString(t) {
			return fmt.Errorf("bad product type %q", t)
		}
	}
	switch m.Inherit {
	case "", InheritDown, InheritOffer, InheritNone:
	default:
		return errors.New(`inherit must be "down", "offer" or "none"`)
	}
	if m.Scope == "workspace" && m.Flows() != InheritNone {
		return errors.New("a plugin only on the workspace has nothing below it to flow to")
	}
	if len(m.CardSettings) > 3 {
		return errors.New("card_settings lists up to 3 settings")
	}
	for _, k := range m.CardSettings {
		if len(m.Extends) > 0 {
			break // its settings may come from what it extends
		}
		if !slices.ContainsFunc(m.Settings, func(s Setting) bool { return s.Key == k && s.Type != "secret" }) {
			return fmt.Errorf("card_settings: no setting %q (secrets can't be shown)", k)
		}
	}
	if len(m.SuggestFor) > 0 && !m.On("product") {
		return errors.New("only a plugin that can be on a product is suggested for product types")
	}
	deps := map[string]bool{}
	check := func(d Dependency) error {
		if !idRe.MatchString(d.ID) || d.ID == m.ID {
			return fmt.Errorf("bad dependency %q", d.ID)
		}
		if deps[d.ID] {
			return fmt.Errorf("%s is listed twice", d.ID)
		}
		deps[d.ID] = true
		if _, err := ParseRange(d.Version); err != nil {
			return fmt.Errorf("%s: %w", d.ID, err)
		}
		return nil
	}
	for _, d := range m.Requires {
		if err := check(d); err != nil {
			return err
		}
	}
	for i, id := range m.Extends {
		if !idRe.MatchString(id) || id == m.ID || slices.Contains(m.Extends[:i], id) {
			return fmt.Errorf("bad extends %q", id)
		}
	}
	if len(m.Extends) > 4 {
		return errors.New("a plugin extends at most 4 others")
	}
	for _, r := range m.Permissions.Read {
		if !slices.Contains(ReadScopes, r) {
			return fmt.Errorf("unknown read permission %q", r)
		}
	}
	for _, op := range m.Permissions.Ops {
		if !slices.Contains(PluginOps, op) {
			return fmt.Errorf("plugins can't ask for %q", op)
		}
	}
	seen := map[string]bool{}
	for _, s := range m.Settings {
		if !keyRe.MatchString(s.Key) || seen["s:"+s.Key] {
			return fmt.Errorf("bad or repeated setting key %q", s.Key)
		}
		seen["s:"+s.Key] = true
		switch s.Type {
		case "select":
			if len(s.Options) == 0 {
				return fmt.Errorf("setting %s needs options", s.Key)
			}
		default:
			if !slices.Contains(SettingTypes, s.Type) {
				return fmt.Errorf("setting %s: type must be one of %s", s.Key, strings.Join(SettingTypes, ", "))
			}
		}
		if s.Fetch && s.Type != "url" && s.Type != "urls" && s.Type != "text" && s.Type != "secret" {
			return fmt.Errorf("setting %s: only links can be fetched", s.Key)
		}
		if a := s.Allows; a != nil && (s.Type != "select" || s.Scope != "project" || !slices.Contains(s.Options, a.When) || a.Action != "create") {
			return fmt.Errorf("setting %s: allows needs a project select, one of its options, and the action create", s.Key)
		}
		if s.Type == "secret" && s.Default != nil {
			return fmt.Errorf("setting %s: a secret can't have a default", s.Key)
		}
		if s.Scope != "workspace" && s.Scope != "project" {
			return fmt.Errorf("setting %s: scope must be workspace or project", s.Key)
		}
		if s.Scope == "project" && m.Scope == "workspace" {
			return fmt.Errorf("setting %s is per project, but the plugin is workspace only", s.Key)
		}
		if s.Level != "" && (s.Scope != "project" || (s.Level != "project" && s.Level != "product")) {
			return fmt.Errorf("setting %s: level must be project or product, on a project setting", s.Key)
		}
		if s.HideWhen != "" && !slices.ContainsFunc(m.Settings, func(o Setting) bool {
			return o.Key == s.HideWhen && o.Type == "toggle" && o.Key != s.Key && o.Scope == s.Scope
		}) {
			return fmt.Errorf("setting %s: hide_when must name another toggle setting at the same scope", s.Key)
		}
	}
	c := &m.Contributes
	cards := func(kind string, list []Card) error {
		for i := range list {
			cd := &list[i]
			if !keyRe.MatchString(cd.Key) || seen[kind+cd.Key] || cd.Name == "" {
				return fmt.Errorf("bad or repeated %s card %q", kind, cd.Key)
			}
			seen[kind+cd.Key] = true
			if cd.Size == "" {
				cd.Size = "small"
			}
			if cd.Size != "small" && cd.Size != "wide" && cd.Size != "tall" {
				return fmt.Errorf("card %s: size must be small, wide or tall", cd.Key)
			}
			if cd.Where != "" && (kind != "project" || cd.Where != "work") {
				return fmt.Errorf("card %s: where can only be \"work\", on a project card", cd.Key)
			}
			if t := cd.Together; t != nil {
				if kind != "project" {
					return fmt.Errorf("card %s: only project cards can be put together", cd.Key)
				}
				if t.How != "sum" && t.How != "average" && t.How != "list" {
					return fmt.Errorf("card %s: together.how must be sum, average or list", cd.Key)
				}
				if len(t.Label) > 60 {
					return fmt.Errorf("card %s: together.label is too long", cd.Key)
				}
			}
		}
		return nil
	}
	if err := cards("dashboard", c.Dashboard); err != nil {
		return err
	}
	if err := cards("project", c.ProjectCards); err != nil {
		return err
	}
	if len(c.ProjectCards) > 0 && m.Scope == "workspace" {
		return errors.New("project cards need a project or both scope")
	}
	for i := range c.Pages {
		p := &c.Pages[i]
		if !keyRe.MatchString(p.Key) || seen["page"+p.Key] || p.Name == "" || len(p.Name) > 30 {
			return fmt.Errorf("bad or repeated page %q", p.Key)
		}
		seen["page"+p.Key] = true
		if err := p.Glyph.Check(); err != nil {
			return fmt.Errorf("page %s: %w", p.Key, err)
		}
		if p.Where != "workspace" && p.Where != "project" {
			return fmt.Errorf("page %s: where must be workspace or project", p.Key)
		}
		if p.Nav != "" && (p.Nav != "main" || p.Where != "workspace") {
			return fmt.Errorf("page %s: nav can only be main, on a workspace page", p.Key)
		}
		if p.Where == "project" && m.Scope == "workspace" {
			return fmt.Errorf("page %s is per project, but the plugin is workspace only", p.Key)
		}
	}
	if c.Look != nil {
		if len(c.Look.Tokens) == 0 || len(c.Look.Tokens) > 40 {
			return errors.New("a look sets 1-40 tokens")
		}
		for k, v := range c.Look.Tokens {
			if !tokenRe.MatchString(k) || !tokenValRe.MatchString(v) {
				return fmt.Errorf("look token %s: %q isn't allowed", k, v)
			}
		}
	}
	for _, e := range c.Events {
		if !slices.Contains(Events, e) {
			return fmt.Errorf("unknown event %q", e)
		}
	}
	if sc := c.Schedule; sc != nil {
		if sc.Every < 5 || sc.Every > 1440 {
			return errors.New("schedule: every must be 5 to 1440 minutes")
		}
		if sc.Per == "" {
			sc.Per = "workspace"
		}
		if sc.Per != "workspace" && sc.Per != "project" {
			return errors.New(`schedule: per must be "workspace" or "project"`)
		}
		if sc.Per == "project" && m.Scope == "workspace" {
			return errors.New("schedule: per project needs a project or both scope")
		}
	}
	if wh := c.Webhook; wh != nil {
		if !headerRe.MatchString(wh.SignatureHeader) || (wh.EventHeader != "" && !headerRe.MatchString(wh.EventHeader)) || len(wh.SignaturePrefix) > 20 {
			return errors.New("webhook: signature_header (and event_header) must be header names")
		}
		if m.Scope == "workspace" {
			return errors.New("webhook: needs a project or both scope")
		}
		if len(wh.Connect) > 60 || len(wh.Disconnect) > 60 {
			return errors.New("webhook: connect and disconnect are action names")
		}
	}
	for _, f := range c.WorkFields {
		if !keyRe.MatchString(f.Key) || seen["wf"+f.Key] || f.Name == "" || len(f.Name) > 30 {
			return fmt.Errorf("bad or repeated work field %q", f.Key)
		}
		seen["wf"+f.Key] = true
		if f.Type != "choice" && f.Type != "toggle" && f.Type != "label" {
			return fmt.Errorf("work field %s: type must be choice, toggle or label", f.Key)
		}
		if f.GoalScope != "" && (f.Type != "choice" || len(f.GoalScope) > 30) {
			return fmt.Errorf("work field %s: only a choice field can scope goals", f.Key)
		}
	}
	for _, v := range c.WorkViews {
		if !keyRe.MatchString(v.Key) || seen["wv"+v.Key] || v.Name == "" || len(v.Name) > 30 {
			return fmt.Errorf("bad or repeated work view %q", v.Key)
		}
		seen["wv"+v.Key] = true
	}
	if (len(c.WorkFields) > 0 || len(c.WorkViews) > 0 || c.ProjectStatus) && m.Scope == "workspace" {
		return errors.New("work fields, work views and project status need a project or both scope")
	}
	if len(c.Tools) > 12 {
		return errors.New("a plugin adds at most 12 tools")
	}
	for _, t := range c.Tools {
		if !toolRe.MatchString(t.Name) || seen["tool"+t.Name] || t.Description == "" || len(t.Description) > 600 || !keyRe.MatchString(t.Action) {
			return fmt.Errorf("bad or repeated tool %q", t.Name)
		}
		seen["tool"+t.Name] = true
		for _, p := range t.Params {
			if !toolRe.MatchString(p.Name) || p.Name == "project" || len(p.Description) > 300 {
				return fmt.Errorf("tool %s: bad argument %q", t.Name, p.Name)
			}
		}
	}
	if err := c.checkSkills(); err != nil {
		return err
	}
	if err := m.checkRunner(); err != nil {
		return err
	}
	if len(m.Permissions.Hosts) > 20 {
		return errors.New("a plugin lists at most 20 hosts")
	}
	for _, h := range m.Permissions.Hosts {
		if !hostRe.MatchString(h) {
			return fmt.Errorf("host %q should look like api.example.com or *.example.com", h)
		}
	}
	return nil
}

var toolRe = regexp.MustCompile(`^[a-z][a-z0-9_]{1,39}$`)

var hostRe = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

var headerRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]{1,63}$`)

// HostAllowed reports whether host matches one of the patterns.
func HostAllowed(patterns []string, host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, p := range patterns {
		if rest, ok := strings.CutPrefix(p, "*."); ok {
			if host == rest || strings.HasSuffix(host, "."+rest) {
				return true
			}
		} else if host == p {
			return true
		}
	}
	return false
}

// PluginOps are the changes a plugin may ask for in its manifest.
var PluginOps = []string{"work.create", "work.import", "work.update", "work.move", "work.note", "work.fields", "project.icon"}

// checkSkills checks each skill's name and description, and that no name
// repeats.
func (c Contributes) checkSkills() error {
	seen := map[string]bool{}
	for _, k := range c.Skills {
		if !idRe.MatchString(k.Name) || seen[k.Name] || k.Description == "" || len(k.Description) > 200 {
			return fmt.Errorf("bad or repeated skill %q", k.Name)
		}
		seen[k.Name] = true
	}
	return nil
}

// HasLogic reports whether the bundle files include logic.
func HasLogic(files map[string][]byte) bool { _, ok := files["logic.wasm"]; return ok }

// Merge returns child laid over parent: the result of child extending parent.
// Child's own fields win; lists of cards, pages and settings are merged by
// key, with child's entries replacing parent's.
func Merge(parent, child Manifest) Manifest {
	out := child
	if out.Summary == "" {
		out.Summary = parent.Summary
	}
	if out.Description == "" {
		out.Description = parent.Description
	}
	if out.Icon == "" {
		out.Icon = parent.Icon
	}
	if len(out.Glyph) == 0 {
		out.Glyph = parent.Glyph
	}
	if out.Author == "" {
		out.Author = parent.Author
	}
	if out.Inherit == "" {
		out.Inherit = parent.Inherit
	}
	if len(out.CardSettings) == 0 {
		out.CardSettings = parent.CardSettings
	}
	out.Extends = union(parent.Extends, child.Extends)
	out.Requires = mergeBy(parent.Requires, child.Requires, func(d Dependency) string { return d.ID })
	out.Permissions.Read = union(parent.Permissions.Read, child.Permissions.Read)
	out.Permissions.Ops = union(parent.Permissions.Ops, child.Permissions.Ops)
	out.Permissions.Hosts = union(parent.Permissions.Hosts, child.Permissions.Hosts)
	out.Permissions.FetchLinks = parent.Permissions.FetchLinks || child.Permissions.FetchLinks
	out.Settings = mergeBy(parent.Settings, child.Settings, func(s Setting) string { return s.Key })
	pc, cc := parent.Contributes, child.Contributes
	out.Contributes.Brief = pc.Brief || cc.Brief
	out.Contributes.RailChip = pc.RailChip || cc.RailChip
	out.Contributes.Dashboard = mergeBy(pc.Dashboard, cc.Dashboard, func(c Card) string { return c.Key })
	out.Contributes.ProjectCards = mergeBy(pc.ProjectCards, cc.ProjectCards, func(c Card) string { return c.Key })
	out.Contributes.Pages = mergeBy(pc.Pages, cc.Pages, func(p Page) string { return p.Key })
	out.Contributes.Events = union(pc.Events, cc.Events)
	if cc.Look == nil {
		out.Contributes.Look = pc.Look
	}
	if cc.Schedule == nil {
		out.Contributes.Schedule = pc.Schedule
	}
	if cc.Webhook == nil {
		out.Contributes.Webhook = pc.Webhook
	}
	if cc.Runner == nil {
		out.Contributes.Runner = pc.Runner
	}
	out.Contributes.Calendar = pc.Calendar || cc.Calendar
	out.Contributes.Deadlines = pc.Deadlines || cc.Deadlines
	out.Contributes.SettingsPanel = pc.SettingsPanel || cc.SettingsPanel
	out.Contributes.WorkPanel = pc.WorkPanel || cc.WorkPanel
	out.Contributes.WorkFields = mergeBy(pc.WorkFields, cc.WorkFields, func(f WorkField) string { return f.Key })
	out.Contributes.WorkViews = mergeBy(pc.WorkViews, cc.WorkViews, func(v WorkView) string { return v.Key })
	out.Contributes.ProjectStatus = pc.ProjectStatus || cc.ProjectStatus
	out.Contributes.Tools = mergeBy(pc.Tools, cc.Tools, func(t Tool) string { return t.Name })
	out.Contributes.Skills = mergeBy(pc.Skills, cc.Skills, func(k Skill) string { return k.Name })
	return out
}

func mergeBy[T any](parent, child []T, key func(T) string) []T {
	out := slices.Clone(parent)
	for _, c := range child {
		if i := slices.IndexFunc(out, func(p T) bool { return key(p) == key(c) }); i >= 0 {
			out[i] = c
		} else {
			out = append(out, c)
		}
	}
	return out
}

func union(a, b []string) []string {
	out := slices.Clone(a)
	for _, x := range b {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}
