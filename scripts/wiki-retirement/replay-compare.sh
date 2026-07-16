#!/usr/bin/env bash
# W1 · byte-compat replay (章程裁定 3). Replays the hot read-face routes against
# two galgame-surface instances (historically the standalone galgame binary,
# retired W5; the surface now lives in cmd/catalog) and diffs the responses.
# In the rehearsal the two
# instances differ only in KUN_GALGAME_PG_DATABASE (baseline wiki DB vs merged
# kun_catalog_w1); in production it is the same binary before/after the DSN
# flip. Any non-empty diff (beyond the pinned dynamic-field strip list) fails.
#
#   A_BASE = baseline instance (default http://127.0.0.1:19288 -> a wiki DB)
#   B_BASE = candidate instance (default http://127.0.0.1:19289 -> kun_catalog_w1)
# In the rehearsal, baseline points at an ISOLATED clone (kun_galgame_wiki_base),
# never the live kun_galgame_wiki: the detail GET writes view++, so a live source
# would be mutated. In production the two "instances" are the one service before
# and after the DSN flip (there is no separate baseline DB).
#
# Normalization: bodies are canonicalized with `jq -S` (sorted keys), then the
# STRIP filter removes the one mutating field. STRIP LIST (recorded per task):
#   * .view  — the internal GET /galgame/:gid detail path async-increments the
#     view counter (galgame_service.go IncrementView, UpdateColumn "view+1";
#     touches ONLY view, not updated). It is timing/traffic-dependent, so it is
#     deleted everywhere before comparison. Nothing else in the read-face bodies
#     is dynamic (created/updated are stable DB values; request-ids live only in
#     headers/logs, which are not compared). Extend STRIP + this note if a new
#     dynamic field appears.
#
# Sample ids (gid/tag/official/engine) are drawn from the SOURCE wiki DB so the
# URL set is representative and non-empty. Usage:
#   source apps/api/.env && scripts/wiki-retirement/replay-compare.sh
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HERE/lib.sh"

A_BASE="${A_BASE:-http://127.0.0.1:19288}"
B_BASE="${B_BASE:-http://127.0.0.1:19289}"
SOURCE_DB="${SOURCE_DB:-kun_galgame_wiki_base}"   # sample ids from the isolated clone, not live wiki
# jq filter applied after `-S`; default strips the async view counter (see header).
STRIP="${STRIP:-walk(if type==\"object\" then del(.view) else . end)}"
WORK="$(mktemp -d)"; trap 'rm -rf "$WORK"' EXIT

# --- representative id samples from the source DB ---------------------------
GIDS=$(psql_val "$SOURCE_DB" "SELECT id FROM galgame WHERE status=0 ORDER BY id LIMIT 12")
GIDS+=$'\n'$(psql_val "$SOURCE_DB" "SELECT id FROM galgame WHERE status=0 ORDER BY random() LIMIT 8")
GID_CSV=$(echo "$GIDS" | paste -sd, -)
TAG_ID=$(psql_val   "$SOURCE_DB" "SELECT id FROM galgame_tag ORDER BY id LIMIT 1")
TAG_NAME=$(psql_val "$SOURCE_DB" "SELECT name FROM galgame_tag WHERE name<>'' ORDER BY id LIMIT 1")
OFF_ID=$(psql_val   "$SOURCE_DB" "SELECT id FROM galgame_official ORDER BY id LIMIT 1")
ENG_ID=$(psql_val   "$SOURCE_DB" "SELECT id FROM galgame_engine ORDER BY id LIMIT 1")
SER_ID=$(psql_val   "$SOURCE_DB" "SELECT id FROM galgame_series ORDER BY id LIMIT 1")
MONTH=$(psql_val    "$SOURCE_DB" "SELECT to_char(release_date,'YYYY-MM') FROM galgame WHERE release_date IS NOT NULL AND status=0 ORDER BY random() LIMIT 1")

# --- route list (relative paths) -------------------------------------------
ROUTES=(
  "/api/galgame/?page=1&limit=24"
  "/api/galgame/?page=2&limit=24&sort_field=updated&sort_order=desc"
  "/api/galgame/?page=1&limit=24&content_limit=all"
  "/api/galgame/batch?ids=$GID_CSV&view=detail"
  "/api/galgame/batch?ids=$GID_CSV&view=brief"
  "/api/galgame/batch?ids=$GID_CSV&view=detail&content_limit=all"
  "/api/galgame/drafts?page=1&limit=24"
  "/api/galgame/stats"
  "/api/galgame/calendar?month=$MONTH"
  "/api/galgame/calendar?month=$MONTH&content_limit=all"
  "/api/galgame/calendar/pending"
  "/api/galgame/calendar/tba"
  "/api/galgame/officials/$OFF_ID/galgames?page=1&limit=24"
  "/api/galgame/tags/$TAG_ID/galgames?page=1&limit=24"
  "/api/tag/?page=1&limit=24"
  "/api/tag/multi?ids=$TAG_ID"
  "/api/tag/$TAG_ID/galgame-ids"
  "/api/official/?page=1&limit=24"
  "/api/official/$OFF_ID/galgame-ids"
  "/api/engine/?page=1&limit=24"
  "/api/engine/$ENG_ID/galgame-ids"
  "/api/series/"
  "/api/series/$SER_ID"
  # auth-gated NextMoe /v1 face — identical 401 on both proves the gated path
  "/v1/galgame/?page=1"
  "/v1/galgame/changes"
)
# per-gid detail + sub-resources
for g in $GIDS; do
  ROUTES+=("/api/galgame/$g" "/api/galgame/$g/scores" "/api/galgame/$g/links" "/api/galgame/$g/aliases" "/api/galgame/$g/contributors" "/api/galgame/$g/revisions?page=1&limit=20" "/api/galgame/$g/prs")
done

# Two-tier normalization:
#   strict = canonical (sorted keys). Byte-identical bodies match here.
#   deep   = strict + every array recursively sorted by content. A body that
#            matches only here differs solely in ARRAY ORDER — i.e. the same
#            data in a different tie-order (heap-dependent; see step 01 report:
#            the List ORDER BY has no unique tiebreaker, so restore-reordered
#            ties reshuffle). That is NOT a data difference, so it is reported
#            as TIE, not FAIL.
strict() { jq -S "$STRIP" "$1" 2>/dev/null || cat "$1"; }
deep()   { jq -S "$STRIP"' | walk(if type=="array" then sort_by(tojson) else . end)' "$1" 2>/dev/null || cat "$1"; }

ok=0; tie=0; diff=0; err=0; total=0; tielist=""
echo "gids=$(echo "$GIDS" | tr '\n' ' ')"
echo "tag=$TAG_ID official=$OFF_ID engine=$ENG_ID series=$SER_ID month=$MONTH"
echo "replaying ${#ROUTES[@]} routes  A=$A_BASE  B=$B_BASE"
for r in "${ROUTES[@]}"; do
  total=$((total+1))
  sa=$(curl -s -m 20 -o "$WORK/a.body" -w '%{http_code}' "$A_BASE$r") || { echo "ERR curl A $r"; err=$((err+1)); continue; }
  sb=$(curl -s -m 20 -o "$WORK/b.body" -w '%{http_code}' "$B_BASE$r") || { echo "ERR curl B $r"; err=$((err+1)); continue; }
  if [ "$sa" != "$sb" ]; then echo "DIFF(status $sa!=$sb) $r"; diff=$((diff+1)); continue; fi
  if cmp -s <(strict "$WORK/a.body") <(strict "$WORK/b.body"); then ok=$((ok+1)); continue; fi
  if cmp -s <(deep "$WORK/a.body") <(deep "$WORK/b.body"); then
    tie=$((tie+1)); tielist+="  TIE(order-only) $r"$'\n'; continue
  fi
  echo "DIFF(body) $r"; diff <(strict "$WORK/a.body") <(strict "$WORK/b.body") | head -20; diff=$((diff+1))
done
echo "-----------------------------------------------"
[ -n "$tielist" ] && { echo "order-only ties (same data, heap tie-order):"; printf '%s' "$tielist"; }
echo "TOTAL=$total  BYTE_IDENTICAL=$ok  TIE_ORDER_ONLY=$tie  REAL_DIFF=$diff  CURL_ERR=$err"
[ "$diff" = 0 ] && [ "$err" = 0 ] && { echo "RESULT: PASS (no data diff; ties are documented heap tie-order)"; exit 0; } || { echo "RESULT: FAIL"; exit 1; }
