// Package plugin is the Go SDK for Enplace plugin logic.
//
// Plugin logic is a WASI program. Enplace runs it in a sandbox for each
// call, hands it one Request on stdin, and reads one Response from stdout.
// Build it with:
//
//	GOOS=wasip1 GOARCH=wasm go build -o logic.wasm ./logic
//
// and write main as:
//
//	func main() { plugin.Run(handle) }
//
// The same JSON works from any language that compiles to WASI.
package plugin

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

// Hooks: why Enplace is calling.
const (
	// HookRender asks for what goes in the slots listed in Request.Slots.
	// State changes in its response are ignored, so drawing is always safe.
	HookRender = "render"
	// HookPage asks for a page's content (Request.Page).
	HookPage = "page"
	// HookEvent reports a work event (Request.Event).
	HookEvent = "event"
	// HookAction runs something a person pressed or a view asked for
	// (Request.Action).
	HookAction = "action"
	// HookSchedule runs every few minutes, as the manifest's
	// contributes.schedule says: once for the workspace, or once for each
	// project the plugin is on (Request.ProjectID). It's where logic fetches
	// from outside and stores what it found.
	HookSchedule = "schedule"
	// HookView asks for a work view's groups (Request.View), for a project.
	HookView = "view"
	// HookWebhook hands over one delivery to the project's webhook
	// (Request.Webhook.Delivery), with contributes.webhook. Enplace has
	// already checked its signature. Set Response.Refresh to run the
	// schedule hook for the project now.
	HookWebhook = "webhook"
)

// SlotWorkFields is the render slot that asks for the plugin's work
// fields in a project: the choices each offers (Response.Fields).
const SlotWorkFields = "work_fields"

// Slots a render can ask for. Cards are "card:<key>" and project cards
// "project_card:<key>".
const (
	SlotBrief    = "brief"
	SlotRailChip = "rail_chip"
	// SlotWorkPanel is the plugin's blocks on one work item's panel
	// (Request.WorkID, WorkKey), with contributes.work_panel.
	SlotWorkPanel = "work_panel"
	// SlotDeadlines asks when work in the plugin's projects has to be done
	// by, for arranging the day (Response.Deadlines; needs
	// contributes.deadlines).
	SlotDeadlines = "deadlines"
	// SlotSettingsPanel is the plugin's blocks at the top of its settings
	// page, with contributes.settings_panel.
	SlotSettingsPanel = "settings_panel"
)

// Request is everything the logic knows for one call.
type Request struct {
	Hook      string    `json:"hook"`
	Now       time.Time `json:"now"`
	Date      string    `json:"date"` // today in the workspace's timezone, YYYY-MM-DD
	Timezone  string    `json:"timezone"`
	WeekStart int       `json:"week_start"` // 0 Sunday, 1 Monday
	Workspace Workspace `json:"workspace"`
	Plugin    Info      `json:"plugin"`
	// ProjectID is set for project cards, project pages and actions there.
	ProjectID string   `json:"project_id,omitempty"`
	Slots     []string `json:"slots,omitempty"`
	// WorkID and WorkKey (like ENP-12) are the work item a "work_panel"
	// render is for.
	WorkID  string  `json:"work_id,omitempty"`
	WorkKey string  `json:"work_key,omitempty"`
	Page    string  `json:"page,omitempty"`
	View    string  `json:"view,omitempty"`
	Event   *Event  `json:"event,omitempty"`
	Action  *Action `json:"action,omitempty"`
	// Settings are the plugin's workspace settings; ProjectSettings are
	// per project, by project id.
	Settings        map[string]any            `json:"settings"`
	ProjectSettings map[string]map[string]any `json:"project_settings,omitempty"`
	// Tracks are each product's tracks (ENP-350), by product scope id, for
	// a plugin with settings that are "per": "track". The first track's
	// values are also in ProjectSettings, so a plugin that ignores tracks
	// works as before.
	Tracks map[string][]Track `json:"tracks,omitempty"`
	State  State              `json:"state"`
	// Projects are the workspace's projects, with On set where the plugin
	// is switched on. Details beyond names need the "projects" permission.
	Projects []Project `json:"projects"`
	// Work is every item in the projects the plugin is on, with the "work"
	// permission.
	Work []Work `json:"work,omitempty"`
	// Secrets are the plugin's secret settings (API keys and the like), by
	// key: the project's own when the call is for a project, else the
	// workspace's. They're only sent to schedule, action and event hooks.
	Secrets map[string]string `json:"secrets,omitempty"`
	// Connections are the accounts the plugin signs in with (the
	// manifest's connections), by service: the one the product or project
	// picked, else the workspace's. Same hooks as Secrets. A connection
	// plugin's check action gets the account it checks here.
	Connections map[string]Account `json:"connections,omitempty"`
	// Vault is what the plugin stored with VaultPut, by name (same hooks).
	Vault map[string]string `json:"vault,omitempty"`
	// Peers is what linked plugins published: the state keys starting
	// "public." of the plugins this one requires or extends, and of the
	// plugins that require or extend it, by plugin id. It's how plugins
	// build on each other without sharing code.
	Peers map[string]State `json:"peers,omitempty"`
	// Webhook is the project's webhook, with contributes.webhook and a
	// ProjectID: its address and whether it's on, and on action, schedule
	// and webhook hooks its signing secret. A webhook hook also carries
	// the delivery.
	Webhook *Webhook `json:"webhook,omitempty"`
	// ReadOnly is set when an agent's tool runs an action: anything the
	// response would change is ignored, only Data goes back.
	ReadOnly bool `json:"read_only,omitempty"`
	// Notes are the memory notes an agent can see at the level a memory
	// engine's "recall" action runs for (contributes.recall), when the
	// plugin reads "memory". Answer with Data: a list of RecallMatch.
	Notes []Note `json:"notes,omitempty"`
	// Waiting are the changes this plugin asked for that still wait in
	// Approvals, oldest first, so it doesn't ask again (schedule, action
	// and event hooks). Once one is gone from here it was approved (and
	// shows in Work) or refused.
	Waiting []Op `json:"waiting,omitempty"`
}

// KeyTestBuild is the project state key a plugin that makes test builds
// (contributes.test_builds) publishes its latest one under, as a TestBuild,
// for the product it's on. Enplace shows it on the round's recap.
const KeyTestBuild = "public.test_build"

// TestBuildAsk is what Enplace hands a test_builds action as its args: a
// finished Autopilot round of the product, and what to try in it.
type TestBuildAsk struct {
	// ID is this ask's own; publish it back in TestBuild.ID.
	ID string `json:"id"`
	// Round is the round's number, and Term what a round is called there
	// ("Round", "Sprint").
	Round int    `json:"round"`
	Term  string `json:"term"`
	// WhatToTest is the round's changes and how to try each, as plain
	// text for the build's notes ("What to test" in TestFlight).
	WhatToTest string `json:"what_to_test"`
}

// TestBuild is how a test build is going (KeyTestBuild).
type TestBuild struct {
	ID    string `json:"id"`
	Round int    `json:"round"`
	// State is building, ready (on the person's device), waiting (on
	// something only the person can do; Note says what) or failed.
	State string `json:"state"`
	// Name is the build's own name, like "1.0 (14)".
	Name string `json:"name,omitempty"`
	// Note is a few words on where it is, or why it failed.
	Note string `json:"note,omitempty"`
	// Link opens the build where the person installs it.
	Link string `json:"link,omitempty"`
	// At is when it was asked for; ReadyAt when it was ready.
	At      time.Time `json:"at"`
	ReadyAt time.Time `json:"ready_at,omitzero"`
}

// TestBuild states.
const (
	TestBuilding = "building"
	TestReady    = "ready"
	TestWaiting  = "waiting"
	TestFailed   = "failed"
)

// Note is a memory note, for a memory engine's recall.
type Note struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Summary string   `json:"summary,omitempty"`
	Body    string   `json:"body"`
	Tags    []string `json:"tags,omitempty"`
	Pinned  bool     `json:"pinned,omitempty"`
}

// RecallMatch is one thing a memory engine found for recall. Enplace adds
// these to its own matches, under the plugin's name.
type RecallMatch struct {
	Name    string `json:"name"`
	Summary string `json:"summary,omitempty"`
	Body    string `json:"body"`
}

// RecallAction is the action a memory engine answers; its args are
// {"query": "..."}.
const RecallAction = "recall"

// Workspace is the workspace the plugin runs in.
type Workspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Info is the plugin's own id and version.
type Info struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

// State is the plugin's own storage: values by key for the workspace, and
// for each project by project id.
type State struct {
	Workspace map[string]json.RawMessage            `json:"workspace"`
	Projects  map[string]map[string]json.RawMessage `json:"projects,omitempty"`
}

// Project is a project in the workspace, or one of a project's products.
//
// A project can hold several products (apps and websites), each with its
// own plugins. To a plugin that's on a product, the product is a project of
// its own: its ID is "<project id>/<product key>", its Name says both
// ("Enplace · Apple app"), and its settings, state, cards, pages, status
// and work are its own. So a plugin written for projects works on
// products unchanged.
type Project struct {
	ID     string `json:"id"`
	Key    string `json:"key"`
	Name   string `json:"name"`
	Colour string `json:"colour"`
	Icon   string `json:"icon,omitempty"`
	On     bool   `json:"on"`
	// Product is set for a product: its key, and ProductName its own name
	// ("Apple app"). Parent is the project it's in. ProductType is what it
	// is (app, website, physical, service, channel or other) and ProductStage where
	// it is (idea, building, live or retired).
	Product      string `json:"product,omitempty"`
	ProductName  string `json:"product_name,omitempty"`
	ProductType  string `json:"product_type,omitempty"`
	ProductStage string `json:"product_stage,omitempty"`
	// Products are a project's products, for a plugin with the "projects"
	// permission: a plugin on the project (a shared release cycle) also
	// carries the work of the products that don't have it themselves.
	Products []ProductInfo `json:"products,omitempty"`
	Parent   string        `json:"parent,omitempty"`
	// Stages are the project's stages in order (a product has its
	// project's), with the "projects" permission.
	Stages []Stage `json:"stages,omitempty"`
}

// Stage is one of a project's stages.
type Stage struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Kind string `json:"kind"` // todo, doing, review or done
}

// ProductInfo is one of a project's products.
type ProductInfo struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	Stage string `json:"stage"`
	// On: the plugin is on this product itself (it has its own).
	On bool `json:"on"`
}

// Work is a work item.
type Work struct {
	ID         string    `json:"id"`
	Key        string    `json:"key"`
	ProjectID  string    `json:"project_id"`
	Title      string    `json:"title"`
	Stage      string    `json:"stage"`
	StageKind  string    `json:"stage_kind"` // todo, doing, review or done
	Priority   int       `json:"priority"`   // 0 none .. 4 urgent
	Estimate   int       `json:"estimate"`   // minutes
	Due        string    `json:"due,omitempty"`
	PlannedFor string    `json:"planned_for,omitempty"`
	Tags       []string  `json:"tags,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	// UpdatedAt is when the item last changed (a note counts).
	UpdatedAt time.Time `json:"updated_at"`
	DoneAt    time.Time `json:"done_at"`
	Body      string    `json:"body,omitempty"`
	// Fields are the project's own fields by key, and plugins' work fields
	// as "<plugin id>.<key>".
	Fields map[string]string `json:"fields,omitempty"`
	// Product is the key of the project's product the work is for. To a
	// plugin on that product, ProjectID is already the product's.
	Product string `json:"product,omitempty"`
	// Assignee is who the work is for: a member's name, or the name it was
	// given (by a person, or by a source as that source names them). Empty
	// is nobody, and work handed to agents. AssigneeEmail is the member's
	// email when it's a member.
	Assignee      string `json:"assignee,omitempty"`
	AssigneeEmail string `json:"assignee_email,omitempty"`
	// ForAgent says agents do it (the assignee "Agent" in Enplace).
	ForAgent bool `json:"for_agent,omitempty"`
}

// Done reports whether the item is finished.
func (w Work) Done() bool { return w.StageKind == "done" }

// Event is a change to work.
type Event struct {
	Type   string `json:"type"` // work.created, work.updated, work.moved or work.done
	Work   Work   `json:"work"`
	Before *Work  `json:"before,omitempty"`
	Actor  string `json:"actor"` // who made the change
}

// Action is a button press or a call from a view.
type Action struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

// Response is what the logic sends back. Every field is optional.
type Response struct {
	// State changes: a null value deletes the key.
	State *State `json:"state,omitempty"`
	Brief *Brief `json:"brief,omitempty"`
	Chip  *Chip  `json:"chip,omitempty"`
	// Cards are blocks by card key ("card:<key>" or "project_card:<key>").
	Cards map[string][]Block `json:"cards,omitempty"`
	Page  *Page              `json:"page,omitempty"`
	// Ops are changes to ask Enplace for. They go through the permission
	// rules as the plugin, so most wait in Approvals.
	Ops   []Op   `json:"ops,omitempty"`
	Toast string `json:"toast,omitempty"`
	// Data goes back to the view that called an action.
	Data any `json:"data,omitempty"`
	// Download is a file handed to the person who pressed an action.
	Download *File `json:"download,omitempty"`
	// Calendar replaces the busy times this plugin shows on Calendar and
	// Today (needs contributes.calendar). Leave it nil to keep what's there.
	Calendar *Calendar `json:"calendar,omitempty"`
	// Fields answers the "work_fields" render slot: each of the plugin's
	// work fields (by its key) in the project, with the choices it offers.
	Fields map[string]FieldState `json:"fields,omitempty"`
	// View answers the view hook: the groups a work view lays work out in.
	View *View `json:"view,omitempty"`
	// Status sets the one-line status and progress ring projects show on the
	// rail and their cards, by project id (needs contributes.project_status).
	// A nil entry clears it. It's kept until the plugin changes it, so set it
	// from the schedule, event and action hooks.
	Status map[string]*ProjectStatus `json:"status,omitempty"`
	// Log is a line for the workspace's history, like "1.5 is now live",
	// recorded against whoever pressed the action.
	Log string `json:"log,omitempty"`
	// Signals say how much each piece asked for in a render matters right
	// now, by slot ("brief", "card:<key>" or "project_card:<key>"). Today
	// uses them to decide what to lift, what to show and what to rest; a
	// piece without one is LevelNormal.
	Signals map[string]Signal `json:"signals,omitempty"`
	// Deadlines answers the "deadlines" render slot: when work has to be
	// done by, by work id, like a release's cut-off. Enplace uses them to
	// arrange the day: work due sooner goes first.
	Deadlines map[string]Deadline `json:"deadlines,omitempty"`
	// Refresh, from the webhook hook, runs the schedule hook for the
	// project right away (a burst of deliveries runs it once).
	Refresh bool `json:"refresh,omitempty"`
	// Blocks, from an action, are new versions of blocks on the screen the
	// action was pressed on, matched by ID: each takes the place of the
	// block with its ID and nothing else is drawn again, so typing,
	// scrolling and folded sections elsewhere stay as they are. Blocks
	// without an ID are ignored. Leave it empty to have the whole screen
	// asked for again, as before.
	Blocks []Block `json:"blocks,omitempty"`
}

// Webhook is a project's webhook (see Request.Webhook).
type Webhook struct {
	On     bool      `json:"on"`
	URL    string    `json:"url,omitempty"`
	Secret string    `json:"secret,omitempty"`
	LastAt time.Time `json:"last_at,omitempty"`
	// Delivery is what arrived, on the webhook hook.
	Delivery *Delivery `json:"delivery,omitempty"`
}

// Delivery is one signed delivery to a webhook.
type Delivery struct {
	// Event is the event header's value (contributes.webhook.event_header).
	Event string `json:"event,omitempty"`
	// Headers are the delivery's headers, by lower-case name (no cookies
	// or authorization).
	Headers map[string]string `json:"headers,omitempty"`
	Body    json.RawMessage   `json:"body"`
}

// Deadline is when one work item has to be done by, and why.
type Deadline struct {
	Date string `json:"date"` // YYYY-MM-DD
	// Reason is a few words a person would say: "1.4 cut-off Friday".
	Reason string `json:"reason,omitempty"`
	// Must marks work that holds something up if it's late (must-ship).
	Must bool `json:"must,omitempty"`
}

// Levels: how much a piece matters right now.
const (
	// LevelQuiet: nothing new. Today rests the piece until that changes.
	LevelQuiet = "quiet"
	// LevelNormal: worth showing, nothing special. The default.
	LevelNormal = "normal"
	// LevelLoud: it changed, it's close, or it needs the person. Today lifts
	// it and shows Reason beside it, so keep loud for what really is.
	LevelLoud = "loud"
)

// Signal is how much one piece matters right now, and the piece in its
// smaller sizes. Enplace decides where the piece goes; a plugin never does.
type Signal struct {
	Level string `json:"level,omitempty"`
	// Reason is why, in a few words a person would say: "Ships Friday",
	// "Biggest week this month". Shown when the piece is lifted.
	Reason string `json:"reason,omitempty"`
	// Line is a card said as one sentence, for people who'd rather read it
	// in the brief than see the card.
	Line string `json:"line,omitempty"`
	// Value is the piece in a few words, shown on its chip while it rests:
	// "6 days", "1,772 this week".
	Value string `json:"value,omitempty"`
	// Number is the piece as one number, for a project card whose manifest
	// lets it be put together across projects (contributes.project_cards[].
	// together): Enplace adds the numbers up or averages them.
	Number *float64 `json:"number,omitempty"`
}

// Signal sets a piece's level and reason, keeping any Line and Value.
func (r *Response) Signal(slot, level, reason string) {
	sg := r.Signals[slot]
	sg.Level, sg.Reason = level, reason
	r.setSignal(slot, sg)
}

// Say sets a piece's sentence and its few-word value, keeping its level.
func (r *Response) Say(slot, line, value string) {
	sg := r.Signals[slot]
	sg.Line, sg.Value = line, value
	r.setSignal(slot, sg)
}

// Number sets a piece's number, used when a person puts the same card from
// several projects together on Today. It keeps the rest of the signal.
func (r *Response) Number(slot string, n float64) {
	sg := r.Signals[slot]
	sg.Number = &n
	r.setSignal(slot, sg)
}

func (r *Response) setSignal(slot string, sg Signal) {
	if r.Signals == nil {
		r.Signals = map[string]Signal{}
	}
	r.Signals[slot] = sg
}

// FieldState is one work field in a project right now.
type FieldState struct {
	// Options are the choices of a choice field. The first whose value is
	// "" names work that has none.
	Options []Option `json:"options,omitempty"`
	// Hidden leaves the field off this project.
	Hidden bool `json:"hidden,omitempty"`
}

// Option is one choice of a work field.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Sub   string `json:"sub,omitempty"`
	// Short is what a work card shows for this choice ("" shows nothing).
	Short string `json:"short,omitempty"`
	// Closed choices are kept for work that already has them but aren't
	// offered for new work (a release that shipped). Goals for a closed
	// choice are over.
	Closed bool `json:"closed,omitempty"`
	// Day is the day the choice stands for, YYYY-MM-DD, when it has one
	// (the day a release ships), so Enplace can put it on the calendar.
	Day string `json:"day,omitempty"`
	// Colour tints the choice where a card shows it, as #rrggbb (a lane
	// of an outside board, like a red Blocked).
	Colour string `json:"colour,omitempty"`
}

// View is a work view: groups of work, in order. Enplace draws the work
// cards itself; dragging a card into a group sets the group's fields on it.
type View struct {
	Groups []Group `json:"groups"`
	// Columns lays the groups out side by side, like the lanes of a
	// board, instead of one under another.
	Columns bool `json:"columns,omitempty"`
}

// Group is one lane of a view.
type Group struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Sub   string `json:"sub,omitempty"`   // beside the title, like "Cut-off Friday"
	Badge string `json:"badge,omitempty"` // a pill, like "2 of 5"
	// Accent draws the badge in the project's colour.
	Accent bool `json:"accent,omitempty"`
	// Muted draws the title quietly, for catch-all groups.
	Muted bool `json:"muted,omitempty"`
	// Work is the ids of the work in the group, in order.
	Work []string `json:"work"`
	// Set is the work fields ("<plugin id>.<key>" of this plugin's own
	// fields) a card dropped here, or work added here, gets.
	Set map[string]string `json:"set,omitempty"`
	// Empty is shown when the group has no work.
	Empty string `json:"empty,omitempty"`
	// Add offers a quick-add box with this placeholder.
	Add string `json:"add,omitempty"`
	// Colour marks the group's title, as #rrggbb (a lane's colour on an
	// outside board).
	Colour string `json:"colour,omitempty"`
}

// ProjectStatus is a project's status line and progress ring.
type ProjectStatus struct {
	Text     string   `json:"text"`
	Progress *float64 `json:"progress,omitempty"` // 0..1
}

// Calendar is every busy time a plugin knows about.
type Calendar struct {
	Events []CalendarEvent `json:"events"`
}

// File is a file to download.
type File struct {
	Name string `json:"name"`
	Type string `json:"type"` // like text/markdown or application/zip
	Data []byte `json:"data"`
}

// CalendarEvent is a busy time.
type CalendarEvent struct {
	ID     string    `json:"id"`
	Title  string    `json:"title"`
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
	AllDay bool      `json:"all_day,omitempty"`
	// Calendar is which of the plugin's calendars it's from (its own id).
	Calendar string `json:"calendar,omitempty"`
	// Source names where it came from, like the calendar's name.
	Source string `json:"source,omitempty"`
	// Free is time marked as free (transparent), shown but not counted as busy.
	Free bool `json:"free,omitempty"`
}

// Brief is one sentence in Today's brief.
type Brief struct {
	Text   string  `json:"text"`
	Button *Button `json:"button,omitempty"`
}

// Chip is the small live card on the rail.
type Chip struct {
	Title    string   `json:"title"`
	Subtitle string   `json:"subtitle,omitempty"`
	Progress *float64 `json:"progress,omitempty"` // 0..1, drawn as a ring
	Href     string   `json:"href,omitempty"`
}

// Page is a page's content.
type Page struct {
	Title  string  `json:"title,omitempty"`
	Blocks []Block `json:"blocks"`
}

// Block types Enplace draws with its own components, on the web and in
// the Apple app. Fields each one reads are listed beside it.
const (
	BlockHeading  = "heading"  // Text, Sub
	BlockText     = "text"     // Text, Tone
	BlockStat     = "stat"     // Label, Value, Sub
	BlockStats    = "stats"    // Items: Title (label), Value, Sub, Icon; Tone "figs" at the top of a page draws them as figures under its title, Done ones in green
	BlockProgress = "progress" // Label, Value, Progress, Sub
	BlockRing     = "ring"     // Label, Value, Progress, Sub: a big ring with a number
	BlockList     = "list"     // Items: Title, Sub, Value, Icon, Done, Progress, Button
	BlockBadges   = "badges"   // Items: Title, Sub, Icon, Locked
	BlockTree     = "tree"     // Items with Children: Title, Sub, Icon, Done, Locked
	BlockDays     = "days"     // Items: Title (day letter), Done, Sub (date); a streak strip
	BlockButtons  = "buttons"  // Buttons
	BlockDivider  = "divider"
	// BlockChart is a row of bars, one for each item, for a number over
	// time. Each item's Progress is its bar's height (0..1), Title names it
	// (a date: the first, middle and last are shown), Sub says its value in
	// words, Done makes it stand out and Locked draws it hollow (no value
	// yet). Tone "spark" draws it small, with no labels.
	BlockChart = "chart" // Label, Items: Title, Sub, Progress, Done, Locked; Tone (spark)
	// BlockForm is fields and a button: pressing it runs the button's
	// action with the button's Args plus each field's value by key.
	BlockForm = "form" // Fields, Buttons[0], Text (a line above)
	// BlockSection is a card of its own holding more blocks. With Value or
	// Progress it gets a ring; Collapsed folds its blocks behind "More".
	BlockSection = "section" // Text (title), Sub, Label (eyebrow), Value, Progress, Tone (tinted), Collapsed, Blocks, Buttons (beside the title)
	// BlockWork lists work items, drawn as Enplace's own work rows.
	BlockWork = "work" // Work (ids), Text (shown when empty)
	// BlockStrip is one linked thing drawn as a compact strip, like a pull
	// request on a work item: Label and Text make its name ("PR #81" and
	// its title), Sub is a line of facts, Value is a state pill, Items are
	// a row of dots (Done: passed, Locked: failed, neither: still running)
	// and Buttons[0] opens it. Tone is its state, which colours the pill and
	// tells Enplace whether it's still moving: "waiting" (nothing linked
	// yet), "open" (in progress), "good", "warn", "done" or "muted" (closed).
	BlockStrip = "strip" // Label, Text, Sub, Value, Tone, Items, Buttons[0]
	// BlockEvents are dated things that happened, which Enplace merges into
	// a work item's live stream. Each item's Title says what happened
	// ("Opened a pull request"), Sub names what it happened to, Value is
	// when (RFC 3339), Icon is optional and Button links to it; the
	// button's Label is shown beside it as a fact ("4 of 4 checks").
	BlockEvents = "events" // Items: Title, Sub, Value (time), Icon, Button
	// BlockBoard is columns of cards that people drag between, like a
	// kanban. Each item is a column: Title, Sub, Colour, and Value is its
	// key. A column's Children are its cards: Title, Sub, Icon, Colour,
	// Done, Value is the card's id, Button opens it and Locked keeps it
	// where it is. Enplace moves a card the moment it's dropped and runs
	// Move with the button's Args plus "card" (its id), "from" and "to"
	// (column keys) and "index" (its place in the new column, from 0).
	// Answer with Blocks holding the board again (give it an ID) to settle
	// it; an error puts the card back. With Add, each column offers "Add a
	// card", which runs Add with "column" and "title". Text is shown in
	// an empty column.
	BlockBoard = "board" // Items (columns) with Children (cards), Move, Add, Text
)

// Icons a block can name. Anything else falls back to a dot.
var Icons = []string{"star", "flame", "trophy", "bolt", "target", "check", "lock", "heart", "dumbbell", "book", "code", "rocket", "brush", "music", "leaf", "coin", "shield", "crown", "sparkle", "calendar", "app", "grid"}

// Block is one piece of a card or page.
type Block struct {
	Type string `json:"type"`
	// ID names the block so an action's Response.Blocks can replace just
	// this one. Keep it unique within the plugin, like "board" or
	// "release:1.4".
	ID       string   `json:"id,omitempty"`
	Text     string   `json:"text,omitempty"`
	Label    string   `json:"label,omitempty"`
	Value    string   `json:"value,omitempty"`
	Sub      string   `json:"sub,omitempty"`
	Progress float64  `json:"progress,omitempty"`
	Colour   string   `json:"colour,omitempty"`
	Tone     string   `json:"tone,omitempty"` // muted, good, warn or big (text: a lead sentence); spark on a chart
	Items    []Item   `json:"items,omitempty"`
	Buttons  []Button `json:"buttons,omitempty"`
	Fields   []Field  `json:"fields,omitempty"`
	// Blocks are a section's contents (one level deep).
	Blocks    []Block  `json:"blocks,omitempty"`
	Collapsed bool     `json:"collapsed,omitempty"`
	Work      []string `json:"work,omitempty"`
	// Save makes a section's Fields its facts, drawn as pills in its title
	// row (a version, a date, a state). Changing one runs Save with the
	// field's key and new value added to its args, plus a date field's
	// Follow key when it has one. Clients that draw facts don't show the
	// section's Text and Sub, so keep those for ones that don't yet.
	Save *Button `json:"save,omitempty"`
	// Menu is a section's "more" menu. A menu button with a Key runs from
	// the keyboard while the section is pointed at or has focus.
	Menu []Button `json:"menu,omitempty"`
	// Move and Add are a board's: run when a card is dropped in a column
	// and when one is added (see BlockBoard).
	Move *Button `json:"move,omitempty"`
	Add  *Button `json:"add,omitempty"`
}

// Field is one input in a form block.
type Field struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Type        string   `json:"type,omitempty"` // text (default), url, secret, number, select, date or textarea
	Placeholder string   `json:"placeholder,omitempty"`
	Options     []string `json:"options,omitempty"`
	Value       string   `json:"value,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Help        string   `json:"help,omitempty"`
	// Icon goes at the start of a field drawn as a pill.
	Icon string `json:"icon,omitempty"`
	// For a date: quick picks above the calendar, like "A week later";
	// the day a range runs from up to the picked day (a cut-off), which
	// moves along with it; other days worth a dot; and the earliest and
	// latest days.
	Shortcuts []Option `json:"shortcuts,omitempty"`
	RangeFrom string   `json:"range_from,omitempty"`
	Marks     []string `json:"marks,omitempty"`
	Min       string   `json:"min,omitempty"`
	Max       string   `json:"max,omitempty"`
	// Follow offers a switch for other dates that can move by as many days
	// as this one, shown with where each would land.
	Follow *Follow `json:"follow,omitempty"`
}

// Follow is the switch under a date field for dates that move along with it.
// Its value ("yes" or "no") goes in the save's args under Key.
type Follow struct {
	Key   string   `json:"key"`
	Label string   `json:"label"`
	Sub   string   `json:"sub,omitempty"`
	On    bool     `json:"on,omitempty"`
	Items []Option `json:"items"` // Label and the date (Value) that would move
}

// Item is a row in a list, a badge, or a node in a tree.
type Item struct {
	Title    string   `json:"title"`
	Sub      string   `json:"sub,omitempty"`
	Value    string   `json:"value,omitempty"`
	Icon     string   `json:"icon,omitempty"`
	Colour   string   `json:"colour,omitempty"`
	Done     bool     `json:"done,omitempty"`
	Locked   bool     `json:"locked,omitempty"`
	Progress *float64 `json:"progress,omitempty"`
	Button   *Button  `json:"button,omitempty"`
	Children []Item   `json:"children,omitempty"`
}

// Button runs an action or goes somewhere in Enplace.
type Button struct {
	Label  string          `json:"label"`
	Action string          `json:"action,omitempty"`
	Args   json.RawMessage `json:"args,omitempty"`
	Href   string          `json:"href,omitempty"` // a path in Enplace, like /x/xp/character
	// URL opens an outside http(s) link in a new tab.
	URL string `json:"url,omitempty"`
	// Confirm is a question asked in place before the action runs, like
	// "Skip 1.4?". The button's label answers it.
	Confirm string `json:"confirm,omitempty"`
	// Icon and Key are for menu buttons: an icon, and a one-letter
	// keyboard shortcut. Open, instead of running anything, opens the
	// section's pill for that field key.
	Icon string `json:"icon,omitempty"`
	Key  string `json:"key,omitempty"`
	Open string `json:"open,omitempty"`
	// Pick, instead of running anything, opens the list of one of the
	// plugin's settings that has choices, right where the button is, and
	// saves what the person picks for the project the card is on.
	Pick string `json:"pick,omitempty"`
}

// ChoicesArgs are the args of a setting's choices action (Setting.Choices
// in the manifest).
type ChoicesArgs struct {
	// Setting is the key of the setting the list is for.
	Setting string `json:"setting"`
	// Values are the settings as the person has them on screen, saved or
	// not, so one list can follow another (a repo's branches).
	Values map[string]any `json:"values,omitempty"`
}

// Choice is one thing a setting with choices can be set to.
type Choice struct {
	// Value is what the setting is saved as, like owner/name.
	Value string `json:"value"`
	// Label is what people read, if it differs from Value.
	Label string `json:"label,omitempty"`
	// Detail is a few words beside it, like "2 hours ago".
	Detail string `json:"detail,omitempty"`
	// Badge is one short word that stands out, like "Private".
	Badge string `json:"badge,omitempty"`
	// Group puts choices under a heading, like the repo's owner.
	Group string `json:"group,omitempty"`
}

// Choices is a choices action's answer (Response.Data). Note says why the
// list is empty or short, like "Add a token to see private repos".
type Choices struct {
	Choices []Choice `json:"choices"`
	Note    string   `json:"note,omitempty"`
}

// Op is a change to ask Enplace for, like {"kind":"work.create",...}.
// Besides the work ops, a plugin can set its own fields on work
// ("work.fields", params {"id": work id, "fields": {"<plugin id>.<key>": value}})
// and give a project an icon ("project.icon", params {"icon_url": "https://..."};
// add "fallback": true for an icon that gives way to any other plugin's).
type Op struct {
	Kind      string `json:"kind"`
	ProjectID string `json:"project_id,omitempty"`
	Params    any    `json:"params"`
	// Asked marks a change the person asked for just now, by pressing a
	// button that names it (a tray's "Accept"). In an action's response,
	// Enplace takes the press as their approval for adding or moving work
	// (work.create, work.import, work.move): it doesn't wait in Approvals a second
	// time. A Never rule still refuses it, and it's recorded as decided by
	// them.
	Asked bool `json:"asked,omitempty"`
}

// Handler answers one request.
type Handler func(*Request) (*Response, error)

// Run reads the request, calls h and writes the response. Call it from main.
func Run(h Handler) {
	if err := run(os.Stdin, os.Stdout, h); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(in io.Reader, out io.Writer, h Handler) error {
	var req Request
	if err := json.NewDecoder(in).Decode(&req); err != nil {
		return fmt.Errorf("bad request: %w", err)
	}
	res, err := h(&req)
	if err != nil {
		return err
	}
	if res == nil {
		res = &Response{}
	}
	return json.NewEncoder(out).Encode(res)
}

// Get reads a workspace state value into v, reporting whether it was set.
func (s State) Get(key string, v any) bool {
	raw, ok := s.Workspace[key]
	return ok && json.Unmarshal(raw, v) == nil
}

// GetProject reads a project's state value into v.
func (s State) GetProject(projectID, key string, v any) bool {
	raw, ok := s.Projects[projectID][key]
	return ok && json.Unmarshal(raw, v) == nil
}

// Put stores a workspace value in s, like a test setting up state or logic
// keeping what it just worked out for the rest of the call.
func (s *State) Put(key string, v any) {
	if s.Workspace == nil {
		s.Workspace = map[string]json.RawMessage{}
	}
	s.Workspace[key] = mustRaw(v)
}

// PutProject stores a project's value in s.
func (s *State) PutProject(projectID, key string, v any) {
	if s.Projects == nil {
		s.Projects = map[string]map[string]json.RawMessage{}
	}
	if s.Projects[projectID] == nil {
		s.Projects[projectID] = map[string]json.RawMessage{}
	}
	s.Projects[projectID][key] = mustRaw(v)
}

// Set records a workspace state change in the response.
func (r *Response) Set(key string, v any) {
	r.ensureState()
	r.State.Put(key, v)
}

// SetProject records a project state change in the response.
func (r *Response) SetProject(projectID, key string, v any) {
	r.ensureState()
	r.State.PutProject(projectID, key, v)
}

func (r *Response) ensureState() {
	if r.State == nil {
		r.State = &State{}
	}
	if r.State.Workspace == nil {
		r.State.Workspace = map[string]json.RawMessage{}
	}
}

func mustRaw(v any) json.RawMessage {
	if v == nil {
		return json.RawMessage("null")
	}
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("null")
	}
	return b
}

// BoardMove is what a board's Move action gets on top of its button's
// Args: decode Action.Args into it (or a struct of your own that embeds it).
type BoardMove struct {
	Card  string `json:"card"`
	From  string `json:"from"`
	To    string `json:"to"`
	Index int    `json:"index"`
}

// BoardAdd is what a board's Add action gets on top of its button's Args.
type BoardAdd struct {
	Column string `json:"column"`
	Title  string `json:"title"`
}

// Track is one of a product's tracks: a path its work takes, like
// Internal beta. See bundle.Purpose.
type Track struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	Purpose string `json:"purpose"`
	// Makes says the track's builds are for someone to use; a check track
	// only builds and tests the code.
	Makes   bool    `json:"makes"`
	Trigger Trigger `json:"trigger"`
	// On says this plugin is a step in the track. A plugin that isn't is
	// left out of it: it doesn't build or ship for that track.
	On bool `json:"on"`
	// Settings are this plugin's per-track settings for the track.
	Settings map[string]any `json:"settings,omitempty"`
}

// Trigger is what starts a track: On is push, tag, pull_request,
// schedule, manual or release, and Pattern the branches or tags it
// matches, like "main" or "release/*".
type Trigger struct {
	On      string `json:"on"`
	Pattern string `json:"pattern,omitempty"`
}

// TrackFor finds the track a request names, or the first track of the
// purpose; ok is false when the product has neither.
func (r *Request) TrackFor(scope, key, purpose string) (Track, bool) {
	ts := r.Tracks[scope]
	for _, t := range ts {
		if key != "" && t.Key == key {
			return t, true
		}
	}
	if key != "" {
		return Track{}, false
	}
	for _, t := range ts {
		if t.Purpose == purpose {
			return t, true
		}
	}
	return Track{}, false
}
