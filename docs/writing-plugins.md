# Writing plugins

Each folder in [`plugins/`](../plugins) is one plugin. Nothing in Enplace's
core lists, imports or knows about them: a plugin is a `.enplace` bundle,
and deleting its folder leaves nothing behind.

The plugins in this repo come with Enplace: the server packs each one, at
the version its `go.mod` pins, and offers it in every workspace. Each new
version is also published to the catalogue on GitHub Pages, so it reaches
servers between Enplace releases. Anyone else's plugin installs the same way
from Settings → Plugins, from a file or a link.

A plugin folder has:

- `manifest.json`: its id, version and `kind`, what it reads, its settings,
  and what it contributes. `extends` and `requires` let it build on other
  plugins. [`schema/manifest.schema.json`](../schema/manifest.schema.json)
  describes every field, so editors complete and check it as you type.
- Its own icon, as `glyph` in the manifest (see below). Enplace holds no
  plugin's icon: it draws whatever the plugin ships.
- `logic/` (optional): a Go `main` package built for WASI. It runs in
  Enplace's sandbox (no files, 128 MB, a time limit) and answers each
  request with blocks, the shared UI vocabulary that the web app and the
  Apple app both draw natively. Use the SDK in [`sdk/plugin`](../sdk/plugin)
  and test it with [`sdk/plugin/plugintest`](../sdk/plugin/plugintest).
  Beyond drawing it can, when its manifest asks: fetch from declared hosts
  or links a person gave it (`plugin.Fetch`), keep secrets in its vault,
  run on a schedule, hand people a file, and supply busy time for the
  calendar.
- `skills/` (optional): Markdown skills the plugin gives agents, one file
  each. A skill's front matter describes it (see Skills plugins below).
- `assets/`, `README.md`, `icon.*` (optional).

## Kinds

A plugin's `kind` says what it's for. The kind fills in where the plugin can
be switched on and how it flows down, and keeps it to the pieces that kind
uses, so the manifest only says what's its own. Anything the manifest sets
itself wins over its kind.

| Kind | For | Fills in | May contribute |
| --- | --- | --- | --- |
| `skills` | Skills for agents, no logic | every level, flows down | `skills` |
| `agent` | Doing work items: Send to Claude, Codex… | the workspace | `runner` |
| `source` | Keeping a project's work in step with another tool | projects and products, offered down, extends `work-sync` | cards, fields, panels, schedule, tools, webhook, skills |
| `service` | Following something a product ships to: a store, a site, a repo | products | cards, pages, fields, panels, schedule, tools, webhook, calendar, skills |
| `extension` | A feature across Enplace, like Release cycle or XP | nothing: say where with `levels` | every piece |

Every manifest names its kind. Pick the narrowest one that fits: an
`extension` can do anything, so it has to say where it goes itself.

A whole skills plugin:

```json
{
  "id": "plain-words",
  "name": "Plain-words writing",
  "version": "1.1.0",
  "kind": "skills",
  "summary": "Skills for writing that people read once and act on."
}
```

with each skill in `skills/<name>.md`:

```markdown
---
description: Write a status update someone can read in ten seconds.
applies: writing an update, a report or a summary
---

# When to use
...
```

The front matter comes off before agents see the skill. A skill can be
listed under `contributes.skills` instead; a listed skill wins.

## Levels, and flowing down

Enplace has three levels, workspace › project › product, and each has its
own plugins. `levels` in the manifest says where a plugin can be switched
on (`"workspace"`, `"project"`, `"product"`), and `inherit` says how it
flows down once it's on somewhere:

- `"down"`: on above means on below. Switched on for the workspace, every
  project has it; on a project, it covers every product. Any level below
  can switch it off for itself, and the rest keep it. XP and Release lanes
  flow down.
- `"offer"`: a product is covered by its project's, with the project's
  settings, until it takes its own; from the workspace, a project is only
  offered it. Release cycle and GitHub are offered, so a monorepo puts
  GitHub on the project and every product shares it, while a repo per
  product puts GitHub on each product.
- `"none"` (the default): it stays where it's switched on, like App
  Store, Google Play and Uptime.

A product covered by its project's plugin uses the project's settings
("Uses Enplace's settings"). Taking its own ("Use own settings") switches
the plugin on for the product with a copy of the project's settings, and
going back drops the product's own again. Adding a plugin on any level
installs it for the workspace when it isn't yet; nothing has to be
switched on for the workspace first.

`card_settings` names up to three settings that sum the plugin up on its
card in Settings, like `["repo", "branch"]` for
`justinguedes/enplace-server · main`. Without it the card shows the first
setting that has a value.

## The plugin's icon

Every plugin draws its own icon, so each one is recognisable across
Enplace. It lives in the manifest as `glyph`: up to 8 layers on a 24 by 24
grid, which the web app and the Apple app each draw natively.

```json
"glyph": [
  { "d": "M12 2.5L20 7L20 17L12 21.5L4 17L4 7Z", "paint": "soft" },
  { "d": "M12 2.5L20 7L20 17L12 21.5L4 17L4 7Z" },
  { "d": "M8.5 12L12 8.5L15.5 12M8.5 16L12 12.5L15.5 16" }
]
```

- `d` is SVG path data, kept to five absolute commands so it is a few lines
  to draw anywhere: `M`, `L`, `C`, `Q` and `Z`, with numbers from 0 to 24.
  (Export from a drawing tool, then convert arcs and relative commands.)
- `paint` is how the layer is drawn. A glyph has no colours of its own, so it
  follows the theme: `line` (the default) is a 2 wide stroke with round caps
  and joins in the text colour around it, `solid` is filled in that colour,
  and `soft` is a see-through fill in the plugin's `colour` (or the text
  colour when the icon already sits on a tile of the plugin's colour).
- A page can have a `glyph` of its own; without one it uses the plugin's.
  A plugin that extends another and has no glyph uses that plugin's.
- `icon` (a name from Enplace's shared symbols) is only the stand-in for a
  plugin with no glyph. Names still pick the small symbols inside blocks
  and on work fields.

Each page and card a plugin draws also gets a settings button that opens
that plugin's settings: its project settings inside a project, its
workspace page otherwise. Plugins don't declare it.

## Adding to a project's work

A plugin extends work without Enplace's core knowing what it is. Release
cycle, App, Release lanes and Website are all built from these, and nothing
outside their folders mentions a release or an app.

- **Work fields** (`contributes.work_fields`): a choice, a toggle or a
  label (a value only the plugin sets, shown as it is on cards) on every
  work item of a project that has the plugin on. The value lives in the
  item's fields as `<plugin id>.<key>` (`releases.release`), so the API, the
  MCP and other plugins read and set it like any field. The plugin answers
  the choices (`Response.Fields`), may tidy a value someone set (accepting
  "next" or a version for a release), and can let a goal be for one choice
  (`goal_scope`).
- **Work views** (`contributes.work_views`): another way to lay out the Work
  page, as groups of work (`HookView`). Dropping a card into a group sets the
  fields the group names. A project's layout is `x:<plugin id>:<view>`.
- **Project status** (`contributes.project_status`): the line under a
  project's name, and its progress ring (`Response.Status`).
- **Section and work blocks**: a page can hold sections (with a ring, folded
  or open) and lists of real work items, next to the other blocks.
- **Chart blocks**: a row of bars for a number over time, full size on a
  page or as a small spark on a card. App draws its downloads with it; any
  plugin can.
- **Work panel** (`contributes.work_panel`): blocks on each work item's
  panel, under the plugin's name (render slot `work_panel`, with
  `Request.WorkID` and `WorkKey`). GitHub lists the pull requests and
  commits that mention the item there.
- **Deadlines** (`contributes.deadlines`): when work has to be done by,
  answered in the `deadlines` render slot (`Response.Deadlines`, by work id,
  with a reason like "Open Wide! 1.4 cut-off Fri" and `must` for must-ship
  work). Arranging the day on Calendar puts work due sooner first and says
  why. Release cycle sends each release's cut-off.
- **Agent tools** (`contributes.tools`): read-only tools that appear in the
  MCP beside Enplace's own, answered by the plugin.
- **Ops**: with `permissions.ops`, a plugin files changes like anyone else,
  through the same rules and approvals: `work.fields` to set its own fields,
  `work.move` to move an item to another stage (params `id`, `stage` as a
  key, name or kind like `"done"`, a short `reason` for the history, and an
  optional `note` posted on the item; it's the "Move work" permission, which
  plugins have by default, so a person can let a plugin move work without
  letting it edit anything else; GitHub moves work with its pull requests),
  `project.icon` to give a project its icon (with `"fallback": true`
  it gives way to any other plugin's icon, like Website's beside App's).

## Pieces on Today

Today arranges itself, and everything a plugin can show there is a piece: its
brief line, each of its Today cards (`contributes.dashboard`), and each of its
project cards (`contributes.project_cards`) for every project it's on. Give
each fact its own card with its own key (downloads, rating, next release)
rather than one big card, so a person can pick the ones they care about.

A render can say how much each piece matters right now with
`Response.Signals`, by slot (`brief`, `card:<key>`, `project_card:<key>`):

    res.Signal("card:soon", plugin.LevelLoud, "Open Wide! 1.4 ships Fri")
    res.Say("card:soon", "Open Wide! 1.4 ships Fri with 5 of 7 done.", "1.4 Fri")

- `quiet`: nothing new. Today rests the piece on its shelf.
- `normal` (the default): shown in its usual place.
- `loud`: it changed, it's close, or it needs the person. Today lifts it and
  shows the reason beside it. Keep loud for what really won't wait; a person
  can turn a noisy piece down a step with "Show less often".

`Say` gives the piece in its smaller sizes: a sentence (so a card can be read
in the brief instead) and a few words for its chip while it rests. Enplace
decides where a piece goes, from its level, the clock, what's being worked on
and how the person has steered it; a plugin never places anything. Project
cards are opt-in on Today: they come up on their own when loud, or while
their project is being worked on.

### Putting projects together

A project card can also be put together across projects: one card on Today
with a total on top and a row for each project, like downloads across every
app. Say so on the card in the manifest:

    "project_cards": [
      { "key": "downloads", "name": "Downloads",
        "together": { "how": "sum", "label": "new in the last 7 days" } }
    ]

`how` is `sum` (add the numbers up), `average`, or `list` (each project's
value, no total). Give each project's number with `res.Number(slot, n)` and
its few words with `Say`; Enplace draws the card, so the plugin needs no
code for it. The card is offered once two or more projects have it. A person
picks which projects go in (new projects join on their own), and while it's
on Today its projects' own cards don't also come up.

## Building on another plugin

`requires` switches the other plugin on with this one, in every project.
Mark a requirement `"optional": true` when the plugin works without it but
does more beside it: neither switches the other on or holds it, and where
both are on they read each other's public state. GitHub lists Release cycle
this way.
What a plugin stores under `public.*` is handed to the plugins linked to it
(`Request.Peers`), and they run again when it changes. That is how App tells
Release cycle which version is live, and how Release lanes reads the
releases, without either importing the other.

### Base plugins

A manifest with `"base": true` is only there to be extended. It's never
offered or switched on by itself: Enplace stores it beside each plugin that
extends it, and its settings, contributions and permissions only count
folded into that plugin. The plugin that extends it brings its own logic.

### Work sync: work that lives somewhere else

`work-sync` is the base for work sources (Linear, GitHub Issues, an
Obsidian vault). A source extends it and builds its logic on
`sdk/plugin/worksync`, so it's mostly a client for its app:

```go
func main() {
	plugin.Run(func(req *plugin.Request) (*plugin.Response, error) {
		return worksync.Handle(req, worksync.Config{Name: "Linear", Source: &linear{}})
	})
}
```

A `Source` lists its items (already in Enplace's terms: title, Markdown
body, status, priority, labels, due date) and changes one; it can also
create (`Creator`) and close (`Closer`) items. The base brings the rest:

- **The link table**, in the plugin's own state, with each linked item's
  outside reference in the plugin's `link` work field, a label shown on
  its card (`LIN-142`). `public.links` lists which work is linked where.
- **Sync rules** (setting `direction`): two-way, only bring changes in,
  or only send changes out. When both sides changed the same thing since
  they last agreed, the later change wins and the other is posted as a
  note on the item.
- **Stages**: outside statuses go by kind (the source's own, or a guess
  from the name), and the `stages` setting maps any by name ("In Progress
  = Doing").
- **Ops for everything**, so Approvals apply: the first import is one
  `work.import` (one approval), later ones one each, and a change that
  still waits (`Request.Waiting`) isn't filed twice. A refused import is
  skipped from then on.
- **Nothing is deleted**: work removed outside moves to done with a note;
  work deleted here is closed outside only by a source that can close.
- **A tray, if the source wants one** (`Config.Hold`): new items it holds
  wait in a "tray" project card at the top of the project's work
  (`"where": "work"`) and in a line on Today, each with Accept and Skip.
  Accepting several is one import; a skipped item comes back if it
  changes outside. Accept counts as the person's approval (the op is
  `Asked`), so it doesn't wait in Approvals again. GitHub Issues holds
  other people's issues this way.
- **A setting can stand for a permission** (`"allows": {"when": "Add
  automatically", "action": "create"}` on a project select): choosing that
  option sets the plugin's Create rule for the project to Allow, and
  choosing another (or undoing) removes it. GitHub Issues' "New issues"
  uses it for Wait in the tray, Ask me in Approvals, Add automatically.
- **The work panel** (where it's linked, Open, Sync now, Pause, Unlink),
  the project's status line ("Synced with Linear at 14:05"), and a Sync
  project card that only speaks up when the person has to fix something.

`Request.Waiting` (the plugin's own changes still in Approvals), project
`Stages` and work `UpdatedAt` are there for any plugin, not just sources.

### The code a project ships

A plugin that reads a project's code (GitHub now; GitLab or Bitbucket could
follow) publishes `public.code` for the project, so Release cycle can say
how the code compares with the release it plans:

```json
{"provider": "github", "repo": "owner/name", "url": "https://github.com/owner/name",
 "branch": "main", "file": "ios/project.yml", "version": "1.5",
 "tag": "v1.4", "tag_version": "1.4", "ahead": 12, "sha": "…",
 "commit_at": "…", "checked_at": "…"}
```

`plugins/github/versionfile` finds and reads version files without knowing
where they come from, so another code host plugin can use it as it is.

Pack one plugin into a bundle:

```sh
GOOS=wasip1 GOARCH=wasm go build -trimpath -o /tmp/xp.wasm ./plugins/xp/logic
go run ./cmd/enplace-plugin pack plugins/xp --logic /tmp/xp.wasm --out xp.enplace   # add --key FILE to sign
```

Workspaces trust a publisher's key through `ENPLACE_PLUGIN_KEYS`. Unsigned or
unknown-signer plugins can still be installed, after the owner says they
trust them.

Working on one plugin only touches its own folder, so several plugins can be
built at once without conflicts, and each ships its own updates.

## The store, and publishing plugins

Settings → Plugins has a store. The plugins in this repo are always on
it ("Comes with Enplace"), including the skill packs (`plain-words`,
`ship-an-app`): plugins with only `skills/`, shelved under Skills and
memory. A workspace owner can add more sources: a signed catalogue, or a
public GitHub repo of SKILL.md folders (skills only). `ENPLACE_CATALOGUE_URL`
adds a signed catalogue for every workspace; there's none by default.

A catalogue is for plugins that should reach servers without a new
Enplace release. Ours is on GitHub Pages, and the Publish Action writes it:
on every push to main it brings down what's published, builds, signs and
adds each plugin whose version is new (`enplace-plugin publish plugins/*/
--catalogue site --new-only`), and deploys the result. Bump the version
whenever a plugin's files change; the check on every pull request fails
otherwise. A version is published once and never changes after.

A server reads it once these are set (on Railway for enplace.guedio.com):

    ENPLACE_CATALOGUE_URL=https://justinguedes.github.io/enplace-plugins/catalogue.json
    ENPLACE_PLUGIN_KEYS=<the public key keygen printed>,<any others>

Skill packs (no code) from a trusted publisher update themselves in every
workspace within a day, with undo in History; anything with logic or UI
waits for an owner to press Update.

A plugin can be both built in and in the catalogue. The store shows it
once, at the newer version: publishing a pack at a version above the
built-in one takes over from it, and workspaces that added the built-in one
update to it like any other pack. So nothing leaves the built-in set when
it's published: the built-in copy is what a server without the catalogue
(or offline) still has, and it's picked up again at the next release.

A plugin with `"recall": true` under `contributes` is a memory engine: when
an agent calls `recall` where it's on, Enplace runs its `recall` action
(read-only, args `{"query": "..."}`, plus the notes there if it reads
`memory`) and adds the `plugin.RecallMatch` list it answers.
