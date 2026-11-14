# Enplace plugins

Plugins and skill packs for [Enplace](https://enplace.guedio.com), published
so they reach any Enplace server without a new Enplace release.

This repo holds only what a server downloads:

- `catalogue.json`: the list of plugins, each with its version, what it
  adds, and the SHA-256 of its bundle. It's signed by the publisher.
- `bundles/<id>-<version>.enplace`: each plugin, packed and signed.

The plugins' source isn't here. Don't edit these files by hand: a changed
file no longer matches its signature or hash, and servers refuse it.

## Using it on a server

Set these where the server runs:

    ENPLACE_CATALOGUE_URL=https://raw.githubusercontent.com/JustinGuedes/enplace-plugins/main/catalogue.json
    ENPLACE_PLUGIN_KEYS=<the publisher's public key>

Or, for one workspace, add the `catalogue.json` link as a source in
Settings → Plugins → Store.

What happens then:

- The plugins show in the store. A plugin that also comes with Enplace
  shows once, at the newer version.
- Skill packs (skills only, no code) from a publisher the server trusts
  update themselves within a day. Each update is in History with Undo.
- Plugins with code wait for a workspace owner to press Update. A server
  installs code only when it trusts the key that signed it.

## Publishing

From a checkout of the Enplace server repo, with this repo checked out
next to it and the signing key at hand:

    make publish-plugins KEY=~/enplace-plugin.key
    cd ../enplace-plugins && git add -A && git commit -m "plain-words 1.1.0" && git push

Bump a plugin's version whenever its files change. Publishing refuses a
version that's already here with different files.
