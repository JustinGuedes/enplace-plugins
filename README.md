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
- no kind: a plugin that does a bit of everything, like Release cycle.

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
