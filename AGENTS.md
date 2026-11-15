# Working on enplace-plugins

- This repo is public. Keep it to the plugin contract (`sdk/`), the author
  tool and plugin source. Nothing about how the Enplace server works inside
  belongs here, and never commit keys or secrets.
- Bump a plugin's `version` in its `manifest.json` whenever its folder
  changes; the Publish check fails otherwise. If main bumped it meanwhile,
  take main's version and bump once more.
- Publishing is the Publish Action's job: it deploys the catalogue to
  GitHub Pages. Nothing is committed back to main.
- Changing `sdk/` changes what the server builds against: the server's
  `go.mod` then needs the new version, in a PR there.
- Give a new plugin a `kind` when one fits, and let the kind fill in what
  it can. Test logic with `sdk/plugin/plugintest`: every plugin has a smoke
  test, and behaviour worth keeping gets its own test.
- Commits here are authored as Justin Guedes <justin.guedes@gmail.com>.
- Before every push: `gofmt -l .` (empty), `go vet ./...`, `go test -race ./...`,
  `GOOS=wasip1 GOARCH=wasm go build ./plugins/...` and
  `scripts/check-versions.sh origin/main`.
