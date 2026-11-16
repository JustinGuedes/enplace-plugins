# Enplace plugins

Every plugin and skill pack [Enplace](https://enplace.guedio.com) uses, and
the kit to write your own.

Enplace's server is private and stays small. This repo is its public side:
the contract a plugin is built against, never how Enplace works inside.

```
cmd/enplace-plugin/   the author's tool: keygen, pack, publish
sdk/
  plugin/             what a plugin's logic receives and answers
    plugintest/       run logic in plain go test
    worksync/         the engine work-source plugins share
  bundle/             the .enplace bundle: manifest, kinds, signing
  catalogue/          the signed catalogue servers read
plugins/<id>/         each plugin's source
schema/               the manifest's JSON schema, for editors
docs/                 how to write a plugin
scripts/              what the Publish Action runs
```

Start with [docs/writing-plugins.md](docs/writing-plugins.md).

## Kinds of plugin

One manifest format, with a `kind` that fills in the rest:

- `skills`: skills for agents, nothing else. A manifest of five lines.
- `agent`: does work items for people, like Claude Code or Codex.
- `source`: keeps a project's work in step with another tool, like Linear.
- `service`: follows something a product ships to: a store, a site, a repo.
- `extension`: adds a feature across Enplace, like Release cycle or XP.

## The plugins

<!-- plugins: go test ./plugins -update -->

### Agents

- [Claude on your API key](plugins/claude-api): Enplace runs Claude itself on your own Anthropic API key, up to the caps you set.
- [Claude in the cloud](plugins/claude-cloud): Send work to your own Claude Code task on claude.ai, so it runs with your laptop closed.
- [Claude Code](plugins/claude-code): Send work to Claude Code on your own computer, on your Claude plan.
- [Codex CLI](plugins/codex-cli): Send work to the Codex CLI on your own computer, on your own plan.
- [Codex in the cloud](plugins/codex-cloud): Send work to Codex on chatgpt.com, so it runs in the cloud on your ChatGPT plan.
- [Gemini CLI](plugins/gemini-cli): Send work to the Gemini CLI on your own computer, on your own plan.

### Work sources

- [GitHub Issues](plugins/github-issues): Use a repository's issues as a project's work.
- [Linear](plugins/linear): Use a Linear team as a project's work.
- [Obsidian vault](plugins/obsidian): Keep a project's work as Markdown notes in your Obsidian vault.

### Services

- [App Store](plugins/app): Follow an app on the App Store: its icon, live version, review, rating, reviews and downloads.
- [GitHub](plugins/github): Link a project to its GitHub repo, and see the version in the code beside the release plan.
- [Google Play](plugins/google-play): Keep an app's Google Play listing beside the product: its package, page and live version.
- [Uptime](plugins/website): Check a website is up every 15 minutes, with how fast it answered, its page title and its icon.

### Extensions

- [Calendar feeds](plugins/calendar-feeds): Show your calendars on the timeline so plans fit around meetings.
- [Markdown export](plugins/markdown-export): Download a project's work as Markdown files with front matter.
- [Release lanes](plugins/release-lanes): See work grouped by the release it ships in.
- [Release cycle](plugins/releases): Releases on a cadence, with ship rules at cut-off, or deploys as work is done.
- [Work sync](plugins/work-sync): The shared engine work sources build on, like Linear, GitHub Issues and Obsidian.
- [XP](plugins/xp): Turn finished work into XP, levels, streaks, quests and badges.

### Skill packs

- [Plain-words writing](plugins/plain-words): Skills for writing that people read once and act on: updates, release notes and replies.
- [Ship an app](plugins/ship-an-app): Skills for getting an iPhone or Android app out: TestFlight builds, store screenshots and review notes.

<!-- /plugins -->

## Publishing happens on its own

When a change reaches `main`, the [Publish](.github/workflows/publish.yml)
Action checks everything, then builds, signs and publishes each plugin whose
version is new to the catalogue on GitHub Pages. It commits nothing back,
so `main` only holds people's own commits.

To ship a change to a plugin, **bump its `version`** and merge. A pull
request that changes a plugin without a new version fails its check, since
servers only pick up new versions. A version is published once: its
bundle never changes after that.

The Action signs with the `ENPLACE_PLUGIN_SIGNING_KEY` secret: the private
key file `enplace-plugin keygen` wrote.

## Using it on a server

    ENPLACE_CATALOGUE_URL=https://justinguedes.github.io/enplace-plugins/catalogue.json
    ENPLACE_PLUGIN_KEYS=<the publisher's public key>

Or, for one workspace, add that link as a source in Settings → Plugins →
Store. Skill packs from a trusted publisher update themselves within a day;
plugins with code wait for a workspace owner to press Update.

## Working on it

    go vet ./... && go test -race ./...
    GOOS=wasip1 GOARCH=wasm go build ./plugins/...
    scripts/check-versions.sh origin/main
    go test ./sdk/bundle -update        # after changing the manifest type: rewrites the schema
