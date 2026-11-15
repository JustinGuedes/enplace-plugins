#!/bin/sh
# Fails when a plugin's files changed since BASE but its version didn't:
# servers only pick up a new version, so the change would never ship.
#
#   scripts/check-versions.sh origin/main
set -eu
base=${1:?usage: scripts/check-versions.sh BASE}
version() { python3 -c 'import json,sys; print(json.load(sys.stdin).get("version",""))'; }
bad=0
# Tests and their testdata don't go into a bundle, so they don't count.
for id in $(git diff --name-only "$base" HEAD -- plugins ':!*_test.go' ':!*/testdata/*' | awk -F/ 'NF>2 {print $2}' | sort -u); do
  [ -f "plugins/$id/manifest.json" ] || continue
  old=""
  if prev=$(git show "$base:plugins/$id/manifest.json" 2>/dev/null); then
    old=$(printf '%s' "$prev" | version)
  fi
  new=$(version < "plugins/$id/manifest.json")
  if [ -n "$old" ] && [ "$old" = "$new" ]; then
    echo "plugins/$id changed but its version is still $new: bump \"version\" in plugins/$id/manifest.json"
    bad=1
  fi
done
exit $bad
