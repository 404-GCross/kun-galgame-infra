#!/usr/bin/env bash
# 09-open-api-phase2 · 05 wave · A2 read-face retirement matrix (gate G-a2-1).
#
# A2 收敛s the legacy /api galgame face to WRITE + STAFF only: the 44 GET read
# routes and the three S2S cron feeds that used to live on /api are retired
# there. The rich reads now live ONLY on the devapi-gated /internal face (with
# the two live feeds), and taxonomy/recent was a confirmed dead route (dropped).
#
# This script proves the retirement on a running candidate (post-A2) binary:
#   - every one of the 44 read routes on the /api prefix is RETIRED — it no
#     longer returns a 2xx read body. (It returns 401 where the write-face's
#     empty-prefix jwtAuth/seriesAuth fence intercepts the path, 405 where a
#     write shares the exact `/` path, or 404 otherwise — never 200. The exact
#     code is a Fiber routing artifact of the surviving write face; the gate is
#     "no /api read serves data", i.e. status != 2xx.)
#   - every one of the 44 read routes on the /internal prefix (X-API-Key, plus a
#     Bearer JWT for the two dual-credential reads /mine + /messages/mine) is
#     PRESENT — 200. reads.register(internal) mounts all 44, so any non-routing
#     status there would be a handler-level 404 (missing entity), which the
#     DB-derived ids avoid.
#   - the 3 feeds: /api/*  retired; /internal/{messages/feed,revisions/recent}
#     with key → 200; /internal/taxonomy/recent → 404 (never mounted).
#
# The route list + representative ids are derived from the galgame content DB
# exactly as scripts/open-api-phase2/replay-internal-face.sh does.
#
# Usage:
#   source apps/api/.env   # for KUN_PG_*
#   BASE=http://127.0.0.1:19301 \
#   INTERNAL_KEY=nm_test_... JWT=<hs256 dev jwt> \
#     scripts/open-api-phase2/verify-a2-read-retirement.sh
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HERE/../wiki-retirement/lib.sh" # psql_val + KUN_PG_* connection env

BASE="${BASE:-http://127.0.0.1:19301}"     # a POST-A2 candidate binary
SOURCE_DB="${SOURCE_DB:-kun_galgame_wiki}"  # local galgame content DB
INTERNAL_KEY="${INTERNAL_KEY:?set INTERNAL_KEY (an internal-tier devapi key)}"
JWT="${JWT:?set JWT (an HS256 dev token; needed for /mine + /messages/mine)}"
WORK="$(mktemp -d)"; trap 'rm -rf "$WORK"' EXIT
urlenc() { jq -rn --arg s "$1" '$s|@uri'; }

# --- representative ids from the source DB (same picks as replay-internal-face) -
GID=$(psql_val    "$SOURCE_DB" "SELECT id FROM galgame WHERE status=0 ORDER BY id LIMIT 1")
GID_CSV=$(psql_val "$SOURCE_DB" "SELECT string_agg(id::text, ',') FROM (SELECT id FROM galgame WHERE status=0 ORDER BY id LIMIT 6) q")
TAG_ID=$(psql_val   "$SOURCE_DB" "SELECT id FROM galgame_tag ORDER BY id LIMIT 1")
TAG_NAME=$(psql_val "$SOURCE_DB" "SELECT name FROM galgame_tag WHERE name<>'' ORDER BY id LIMIT 1")
OFF_ID=$(psql_val   "$SOURCE_DB" "SELECT id FROM galgame_official ORDER BY id LIMIT 1")
OFF_NAME=$(psql_val "$SOURCE_DB" "SELECT name FROM galgame_official WHERE name<>'' ORDER BY id LIMIT 1")
ENG_ID=$(psql_val   "$SOURCE_DB" "SELECT id FROM galgame_engine ORDER BY id LIMIT 1")
ENG_NAME=$(psql_val "$SOURCE_DB" "SELECT name FROM galgame_engine WHERE name<>'' ORDER BY id LIMIT 1")
SER_ID=$(psql_val   "$SOURCE_DB" "SELECT id FROM galgame_series ORDER BY id LIMIT 1")
MONTH=$(psql_val    "$SOURCE_DB" "SELECT to_char(release_date,'YYYY-MM') FROM galgame WHERE release_date IS NOT NULL AND status=0 ORDER BY id LIMIT 1")
VNDB=$(psql_val     "$SOURCE_DB" "SELECT vndb_id FROM galgame WHERE vndb_id<>'' AND status=0 ORDER BY id LIMIT 1")
TAXREV=$(psql_val   "$SOURCE_DB" "SELECT entity||' '||target_id||' '||revision FROM taxonomy_revision ORDER BY id LIMIT 1" 2>/dev/null || true)
REV_ENT=$(echo "$TAXREV" | awk '{print $1}'); REV_ID=$(echo "$TAXREV" | awk '{print $2}'); REV_N=$(echo "$TAXREV" | awk '{print $3}')
Q=$(urlenc "恋"); TAGN=$(urlenc "$TAG_NAME"); OFFN=$(urlenc "$OFF_NAME"); ENGN=$(urlenc "$ENG_NAME"); KW=$(urlenc "夏")

# --- the 44 read routes as prefix-relative paths ---------------------------
# JWT column: 1 = route requires a real end-user JWT on the /internal side.
# name|relpath|needs_jwt
ROUTES=$(cat <<EOF
galgame.list|/galgame/?page=1&limit=6|0
galgame.search|/galgame/search?q=$Q&page=1&limit=6|0
galgame.batch|/galgame/batch?ids=$GID_CSV&view=brief|0
galgame.drafts|/galgame/drafts?page=1&limit=6|0
galgame.check|/galgame/check?vndb_id=$VNDB|0
galgame.user.stats|/galgame/user/1/stats|0
galgame.user.galgames|/galgame/user/1/galgames?page=1&limit=6|0
galgame.user.contributed|/galgame/user/1/contributed?page=1&limit=6|0
galgame.calendar|/galgame/calendar?month=$MONTH|0
galgame.calendar.pending|/galgame/calendar/pending|0
galgame.calendar.tba|/galgame/calendar/tba|0
galgame.stats|/galgame/stats|0
galgame.officials.galgames|/galgame/officials/$OFF_ID/galgames?page=1&limit=6|0
galgame.tags.galgames|/galgame/tags/$TAG_ID/galgames?page=1&limit=6|0
galgame.mine|/galgame/mine|1
galgame.messages.mine|/galgame/messages/mine|1
galgame.get|/galgame/$GID|0
galgame.links|/galgame/$GID/links|0
galgame.aliases|/galgame/$GID/aliases|0
galgame.contributors|/galgame/$GID/contributors|0
galgame.scores|/galgame/$GID/scores|0
tag.list|/tag/?page=1&limit=6|0
tag.search|/tag/search?q=$Q|0
tag.multi|/tag/multi?ids=$TAG_ID|0
tag.byname|/tag/$TAGN?tag_id=$TAG_ID|0
tag.galgameids|/tag/$TAG_ID/galgame-ids|0
tag.revisions|/tag/$TAG_ID/revisions?page=1&limit=6|0
official.list|/official/?page=1&limit=6|0
official.search|/official/search?q=$Q|0
official.byname|/official/$OFFN?official_id=$OFF_ID|0
official.galgameids|/official/$OFF_ID/galgame-ids|0
official.revisions|/official/$OFF_ID/revisions?page=1&limit=6|0
engine.list|/engine/?page=1&limit=6|0
engine.byname|/engine/$ENGN?engine_id=$ENG_ID|0
engine.galgameids|/engine/$ENG_ID/galgame-ids|0
engine.revisions|/engine/$ENG_ID/revisions?page=1&limit=6|0
series.list|/series/?page=1&limit=6|0
series.search|/series/search?keywords=$KW|0
series.get|/series/$SER_ID|0
series.revisions|/series/$SER_ID/revisions?page=1&limit=6|0
EOF
)
# The four entity /:id/revisions/:rev routes (tag/official/engine/series) — always
# tested so the read count is the full 44. rev=1 (or the real revision number for
# whichever entity a taxonomy_revision exists for). On /internal these are
# route-present; a specific revision may not exist, so a handler 404 there is
# accepted as "route mounted" (never a routing 404 — reads.register mounts all).
TREV=1; OREV=1; EREV=1; SREV=1
case "${REV_ENT:-}" in
  tag)      TREV="$REV_N" ;;
  official) OREV="$REV_N" ;;
  engine)   EREV="$REV_N" ;;
  series)   SREV="$REV_N" ;;
esac
ROUTES+=$'\n'"tag.revision.get|/tag/$TAG_ID/revisions/$TREV|0"
ROUTES+=$'\n'"official.revision.get|/official/$OFF_ID/revisions/$OREV|0"
ROUTES+=$'\n'"engine.revision.get|/engine/$ENG_ID/revisions/$EREV|0"
ROUTES+=$'\n'"series.revision.get|/series/$SER_ID/revisions/$SREV|0"

hit() { # prefix relpath extra_header...
  local prefix="$1" rel="$2"; shift 2
  curl -s -m 20 -o /dev/null -w '%{http_code}' "$@" "$BASE$prefix$rel"
}

echo "BASE=$BASE  gid=$GID tag=$TAG_ID off=$OFF_ID eng=$ENG_ID series=$SER_ID month=$MONTH vndb=$VNDB taxrev='${TAXREV:-none(synthetic /:rev)}'"
echo "=============================================================="
api_ok=0; api_bad=0; int_ok=0; int_bad=0; total=0
declare -A api_dist
while IFS='|' read -r name rel needs; do
  [ -z "$name" ] && continue
  total=$((total+1))
  # /api side (candidate): must be retired → NOT 2xx.
  sa=$(hit /api "$rel")
  api_dist[$sa]=$(( ${api_dist[$sa]:-0} + 1 ))
  if [ "${sa:0:1}" != "2" ]; then api_ok=$((api_ok+1)); else echo "FAIL /api STILL 2xx ($sa) $name $rel"; api_bad=$((api_bad+1)); fi
  # /internal side (candidate): must be present → 200 (key + JWT where needed).
  if [ "$needs" = 1 ]; then
    sb=$(hit /internal "$rel" -H "X-API-Key: $INTERNAL_KEY" -H "Authorization: Bearer $JWT")
  else
    sb=$(hit /internal "$rel" -H "X-API-Key: $INTERNAL_KEY")
  fi
  if [ "$sb" = 200 ]; then int_ok=$((int_ok+1));
  elif [ "$sb" = 404 ] && [[ "$name" == *revision.get ]]; then
    int_ok=$((int_ok+1)); echo "note /internal $name → 404 (rev id absent; route present, handler no-such-rev)"
  else
    echo "FAIL /internal not-200 ($sb) $name $rel"; int_bad=$((int_bad+1))
  fi
done <<< "$ROUTES"

echo "=============================================================="
echo "3 legacy feeds:"
declare -A feed_res
f_api_msg=$(hit /api "/galgame/messages/feed?since_id=0&limit=3")
f_api_rev=$(hit /api "/galgame/revisions/recent?since_id=0&limit=3")
f_api_tax=$(hit /api "/galgame/taxonomy/recent?limit=3")
f_int_msg=$(hit /internal "/galgame/messages/feed?since_id=0&limit=3" -H "X-API-Key: $INTERNAL_KEY")
f_int_rev=$(hit /internal "/galgame/revisions/recent?since_id=0&limit=3" -H "X-API-Key: $INTERNAL_KEY")
f_int_tax=$(hit /internal "/galgame/taxonomy/recent?limit=3" -H "X-API-Key: $INTERNAL_KEY")
printf '  /api  messages/feed=%s  revisions/recent=%s  taxonomy/recent=%s  (all must be non-2xx)\n' "$f_api_msg" "$f_api_rev" "$f_api_tax"
printf '  /int  messages/feed=%s  revisions/recent=%s  (must be 200)  taxonomy/recent=%s (must be 404)\n' "$f_int_msg" "$f_int_rev" "$f_int_tax"
feeds_ok=1
for s in "$f_api_msg" "$f_api_rev" "$f_api_tax"; do [ "${s:0:1}" = "2" ] && feeds_ok=0; done
[ "$f_int_msg" = 200 ] || feeds_ok=0
[ "$f_int_rev" = 200 ] || feeds_ok=0
[ "$f_int_tax" = 404 ] || feeds_ok=0

echo "=============================================================="
echo "/api retirement status distribution: $(for k in "${!api_dist[@]}"; do printf '%s×%s ' "$k" "${api_dist[$k]}"; done)"
echo "READ ROUTES total=$total  /api retired=$api_ok (still-2xx=$api_bad)  /internal present=$int_ok (not-200=$int_bad)"
echo "FEEDS ok=$feeds_ok"
if [ "$api_bad" = 0 ] && [ "$int_bad" = 0 ] && [ "$feeds_ok" = 1 ]; then
  echo "RESULT: PASS"; exit 0
else
  echo "RESULT: FAIL"; exit 1
fi
