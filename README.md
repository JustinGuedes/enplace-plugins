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
- `connection`: signs in to an outside service once, like GitHub or App
  Store Connect, for every plugin that names it in `connections`. People
  add named accounts and pick one per project or product.
- `service`: follows something a product ships to: a store, a site, a repo.
- `extension`: adds a feature across Enplace, like Release cycle or XP.
- `role`: a job on your team, like App shipper: the work it takes, what its
  agent is told to do with it, and the skills it brings or `uses`.

## The plugins

<!-- plugins: go test ./plugins -update -->

### Agents

- [Claude](plugins/claude): Send work to Claude: on your Mac, in the cloud on claude.ai, or on your own API key.
- [Codex](plugins/codex): Send work to Codex: on your Mac with the Codex CLI, or in the cloud on chatgpt.com.
- [Gemini](plugins/gemini): Send work to Gemini on your Mac with the Gemini CLI, on your own plan.

### Work sources

- [GitHub Issues](plugins/github-issues): Use a repository's issues as a project's work.
- [Linear](plugins/linear): Use a Linear team as a project's work.
- [Obsidian vault](plugins/obsidian): Keep a project's work as Markdown notes in your Obsidian vault.

### Connections

- [Apple account](plugins/apple-account): Sign in to App Store Connect once, for the App Store, TestFlight and Xcode Cloud.
- [GitHub account](plugins/github-account): Sign in to GitHub once, for every plugin that works with your repos.
- [Google Play account](plugins/google-account): Sign in to the Google Play Console once, for Google Play and Play testing.

### Services

- [App Store](plugins/app): Follow an app on the App Store: its icon, live version, review, rating, reviews and downloads.
- [GitHub](plugins/github): Link a project to its GitHub repo: pull requests move work, and Versions reads and bumps the version.
- [GitHub Actions](plugins/github-actions): A repo's GitHub Actions builds: each run and each platform's job, a Run button, and a bug for agents when one fails.
- [Google Play](plugins/google-play): Keep an app's Google Play listing beside the product: its package, page and live version.
- [TestFlight](plugins/testflight): Get an iPhone app's builds onto your own phone with TestFlight, and see each build through.
- [Uptime](plugins/website): Check a website is up every 15 minutes, with how fast it answered, its page title and its icon.
- [Xcode Cloud](plugins/xcode-cloud): Apple's CI for an app: Enplace sets up its own workflow, starts builds, shows each one and files a bug for agents when one fails.

### Extensions

- [Calendar feeds](plugins/calendar-feeds): Show your calendars on the timeline so plans fit around meetings.
- [Content](plugins/content): Plan videos and posts for your channels: where each one posts, when, what it promotes, and whether this week's slots are filled.
- [Markdown export](plugins/markdown-export): Download a project's work as Markdown files with front matter.
- [Release lanes](plugins/release-lanes): See work grouped by the release it ships in.
- [Releases](plugins/releases): Releases on a cadence, with ship rules at cut-off, or deploys as work is done.
- [Versions](plugins/versions): Where a project's version lives, how it's numbered, and bumping it in the code.
- [Work sync](plugins/work-sync): The shared engine work sources build on, like Linear, GitHub Issues and Obsidian.
- [XP](plugins/xp): Turn finished work into XP, levels, streaks, quests and badges.

### Skill packs

- [Agent skills](plugins/agent-skills): Engineering habits for agents: ship and launch safely, build in thin slices, plan tasks, harden security and simplify code.
- [Caveman](plugins/caveman-skills): Say less and stay in scope: a terse voice that keeps every fact, and small skills that stop work once it's proven.
- [Design engineering](plugins/design-engineering): Apple-style design, animation and UI polish: springs, depth, gestures and restraint.
- [Engineering](plugins/engineering-skills): Grill a plan, build test first, review code and diagnose bugs: the process a good engineer follows.
- [Lemo-Opuscar film styles](plugins/film-styles): 99 film styles made in code, with their own setup. Its write-ups are non-commercial, so it's a link to the repo, not a copy.
- [Humanizer](plugins/humanizer): Takes the signs of AI writing out of a draft without changing what it says.
- [Marketing](plugins/marketing-skills): SEO audits, copywriting, launches, pricing, emails and ads for the things you ship.
- [Plain-words writing](plugins/plain-words): Skills for writing that people read once and act on: updates, release notes and replies.
- [PM skills](plugins/pm-skills): Product skills for planning: prioritise, write stories and PRDs, run a pre-mortem and turn stories into tests.
- [Ponytail](plugins/ponytail-skills): Lazy senior dev mode: the smallest change that fully solves the task, plus reviews that ask whether every line is needed.
- [Ship an app](plugins/ship-an-app): Skills for getting an iPhone or Android app out: TestFlight builds, store screenshots and review notes.
- [Short video](plugins/short-video): Skills for short videos and posts: hooks, 30-second scripts, captions and hashtags, and one video cut for TikTok, Reels and Shorts.
- [Superpowers](plugins/superpowers-skills): Process for coding agents: evidence before claiming done, systematic debugging, plans, and taking code review well.

### Roles

- [App shipper](plugins/app-shipper): A role for your team that gets apps out: TestFlight builds, store screenshots and review notes.
- [Video maker](plugins/video-maker): A role for your team that writes short videos: hooks, 30-second scripts, captions and one cut for every place.

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
