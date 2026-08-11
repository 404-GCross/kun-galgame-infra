#!/bin/sh
# Fetch character figure originals by content hash into a flat directory.
#
#   scripts/character-cutout/fetch.sh hashes.txt out/ [base_url] [parallel]
#
# hashes.txt is one sha256 per line, e.g.
#   psql -d kun_catalog -Atc "select figure_hash from catalog_character
#                             where figure_hash is not null and figure_hash <> ''"

set -eu

list="${1:?usage: fetch.sh <hash-list> <out-dir> [base-url] [parallel]}"
out="${2:?usage: fetch.sh <hash-list> <out-dir> [base-url] [parallel]}"
base="${3:-https://image.kungal.iloveren.link}"
par="${4:-16}"

mkdir -p "$out"

xargs -P "$par" -I{} sh -c '
    h="$1"; out="$2"; base="$3"
    dst="$out/$h.webp"
    [ -s "$dst" ] && exit 0
    url="$base/$(printf %s "$h" | cut -c1-2)/$(printf %s "$h" | cut -c3-4)/$h.webp"
    curl -sfL --max-time 30 --retry 2 "$url" -o "$dst" || { rm -f "$dst"; echo "MISS $h"; }
' _ {} "$out" "$base" < "$list"

got=$(find "$out" -name '*.webp' -size +0 | wc -l)
want=$(wc -l < "$list")
echo "fetched $got / $want" >&2
