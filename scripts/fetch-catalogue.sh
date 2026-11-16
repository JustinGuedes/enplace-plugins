#!/bin/sh
# Downloads the published catalogue and every bundle it lists into DIR, so
# publishing adds to what's there instead of starting over.
#
#   scripts/fetch-catalogue.sh https://justinguedes.github.io/enplace-plugins/ site
#
# Nothing published yet (a 404) leaves DIR empty. Any other failure stops,
# so a bad download never replaces the live catalogue with a partial one.
set -eu
base=${1:?usage: scripts/fetch-catalogue.sh BASE_URL DIR}
dir=${2:?usage: scripts/fetch-catalogue.sh BASE_URL DIR}
base=${base%/}/
mkdir -p "$dir/bundles"

status=$(curl -sS -o "$dir/catalogue.json" -w '%{http_code}' "${base}catalogue.json")
case $status in
  200) ;;
  404) rm -f "$dir/catalogue.json"; echo "Nothing published yet."; exit 0 ;;
  *) echo "Fetching ${base}catalogue.json: HTTP $status" >&2; exit 1 ;;
esac

for file in $(jq -r '.plugins[].download' "$dir/catalogue.json"); do
  case $file in
    bundles/*.enplace) ;;
    *) echo "Unexpected download path $file" >&2; exit 1 ;;
  esac
  curl -fsS -o "$dir/$file" "$base$file"
done
echo "Fetched $(jq '.plugins | length' "$dir/catalogue.json") published plugins."
