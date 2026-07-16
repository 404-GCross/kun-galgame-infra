#!/usr/bin/env bash
# REHEARSAL-ONLY (W1 step 01). Builds the drill target DB kun_catalog_w1 as a
# clone of the live kun_catalog, so the wiki restore lands beside real catalog_*
# tables exactly as it will in production.
#
# In PRODUCTION this step DOES NOT EXIST: the target IS the already-live
# kun_catalog. Never run this against production.
#
# Clone method: pg_dump | pg_restore (NOT `CREATE DATABASE ... TEMPLATE`).
# pg_dump takes only AccessShare locks, so it is safe to run while letmoe's
# local tests hold connections to kun_catalog; TEMPLATE would require zero
# connections and would abort. The source is read-only throughout.
#
# Usage: source apps/api/.env && scripts/wiki-retirement/setup-rehearsal-db.sh [--force]
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HERE/lib.sh"

SRC="${CATALOG_SRC_DB:-kun_catalog}"
DST="${TARGET_DB:-kun_catalog_w1}"
DUMP_DIR="${DUMP_DIR:-/tmp/wiki-retirement}"
FORCE=0; for a in "$@"; do [ "$a" = "--force" ] && FORCE=1; done

exists=$(psql_val postgres "SELECT 1 FROM pg_database WHERE datname='$DST'")
if [ "${exists:-}" = "1" ]; then
  if [ "$FORCE" = 1 ]; then
    echo "[force] dropping existing $DST"
    psql_do postgres -c "DROP DATABASE IF EXISTS \"$DST\""
  else
    echo "ERROR: $DST already exists. Pass --force to rebuild it." >&2; exit 2
  fi
fi

mkdir -p "$DUMP_DIR"
DIR="$DUMP_DIR/${SRC}.dir"; rm -rf "$DIR"
echo "[1/3] pg_dump $SRC (parallel, read-only) -> $DIR"
t0=$(date +%s)
pg_dump -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -Fd -j 4 --no-owner --no-privileges -f "$DIR" "$SRC"
t1=$(date +%s); echo "    dump ${SRC}: $((t1-t0))s, size=$(du -sh "$DIR" | cut -f1)"

echo "[2/3] createdb $DST"
psql_do postgres -c "CREATE DATABASE \"$DST\" OWNER $PGUSER"

echo "[3/3] pg_restore -> $DST (parallel)"
pg_restore -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -j 4 --no-owner --no-privileges -d "$DST" "$DIR"
t2=$(date +%s); echo "    restore: $((t2-t1))s"
echo "DONE base clone in $((t2-t0))s. $DST is ready for migrate-db.sh."
