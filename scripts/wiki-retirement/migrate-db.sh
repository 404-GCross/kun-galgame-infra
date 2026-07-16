#!/usr/bin/env bash
# W1 · storage move (章程裁定 2): dump the wiki DB and restore its tables INTO
# the target catalog DB. Zero schema change — pg_dump carries the tables,
# indexes, constraints and sequences byte-identically; the service then flips
# KUN_GALGAME_PG_DATABASE to the target. Collision-free by T1 audit (galgame_* /
# taxonomy_revision vs catalog_* share no object name).
#
#   Production: TARGET_DB=kun_catalog   (already live with catalog_* tables)
#   Rehearsal:  TARGET_DB=kun_catalog_w1
#
# The SOURCE (wiki) DB is only ever read. The dump file is retained (DUMP_DIR)
# so it doubles as the frozen pre-cutover backup.
#
# Idempotency / anti-double-load: if the target already holds the wiki tables
# the script refuses, unless --force-clean is given (drops the wiki tables from
# the target first — catalog_* tables are never touched).
#
# Usage: source apps/api/.env && TARGET_DB=kun_catalog_w1 \
#          scripts/wiki-retirement/migrate-db.sh [--force-clean]
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HERE/lib.sh"

SOURCE_DB="${SOURCE_DB:-kun_galgame_wiki}"
TARGET_DB="${TARGET_DB:?set TARGET_DB (e.g. kun_catalog_w1 for rehearsal, kun_catalog for prod)}"
DUMP_DIR="${DUMP_DIR:-/tmp/wiki-retirement}"
FORCE_CLEAN=0; for a in "$@"; do [ "$a" = "--force-clean" ] && FORCE_CLEAN=1; done

if [ "$SOURCE_DB" = "$TARGET_DB" ]; then echo "ERROR: source == target" >&2; exit 2; fi

mapfile -t TABLES < <(wiki_tables "$SOURCE_DB")
echo "SOURCE $SOURCE_DB has ${#TABLES[@]} wiki tables to move."

# --- anti-double-load guard (probe for a signature wiki table) -------------
present=$(psql_val "$TARGET_DB" "SELECT (to_regclass('public.galgame') IS NOT NULL)::int")
if [ "${present:-0}" = "1" ]; then
  if [ "$FORCE_CLEAN" = 1 ]; then
    echo "[--force-clean] dropping ${#TABLES[@]} wiki tables from $TARGET_DB (catalog_* untouched)"
    for t in "${TABLES[@]}"; do psql_do "$TARGET_DB" -c "DROP TABLE IF EXISTS public.\"$t\" CASCADE" >/dev/null; done
  else
    echo "ERROR: $TARGET_DB already contains wiki tables (public.galgame exists)." >&2
    echo "       Refusing to double-load. Re-run with --force-clean to redo the move." >&2
    exit 3
  fi
fi

mkdir -p "$DUMP_DIR"
DUMP="$DUMP_DIR/${SOURCE_DB}.dump"
echo "[1/3] pg_dump $SOURCE_DB -> $DUMP  (custom format; data+indexes+constraints+sequences; --no-owner --no-privileges)"
t0=$(date +%s)
pg_dump -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -Fc --no-owner --no-privileges -f "$DUMP" "$SOURCE_DB"
t1=$(date +%s)
echo "    dump: $((t1-t0))s  size=$(du -h "$DUMP" | cut -f1)"

echo "[2/3] pg_restore -> $TARGET_DB  (--exit-on-error: any conflict aborts loudly)"
pg_restore -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" --no-owner --no-privileges --exit-on-error \
  -d "$TARGET_DB" "$DUMP"
t2=$(date +%s)
echo "    restore: $((t2-t1))s"

echo "[3/3] sequence sanity — nextval never rewinds; target last_value >= source last_value"
seq_bad=0
while read -r seq; do
  [ -z "$seq" ] && continue
  s=$(psql_val "$SOURCE_DB" "SELECT last_value FROM \"$seq\"")
  d=$(psql_val "$TARGET_DB" "SELECT last_value FROM \"$seq\"")
  if [ "$d" -lt "$s" ]; then echo "    !! $seq target=$d < source=$s"; seq_bad=1; fi
done < <(psql_val "$SOURCE_DB" "SELECT sequence_name FROM information_schema.sequences WHERE sequence_schema='public' ORDER BY 1")
[ "$seq_bad" = 0 ] && echo "    sequences OK"

echo "TOTAL migrate-db: $((t2-t0))s   (verify next: TARGET_DB=$TARGET_DB scripts/wiki-retirement/verify-wiki-merge.sh)"
[ "$seq_bad" = 0 ] || exit 4
