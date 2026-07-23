#!/usr/bin/env bash
#
# build-snapshot.sh — produce desensitised dev artifacts ON THE PRODUCTION HOST.
#
# Runs on kungal-neo (invoked over ssh by ../refresh-dev-db.sh --fresh, or by
# hand). Reads production READ-ONLY via pg_dump; every mutation happens inside a
# throwaway `dev_snapshot_scratch_<db>` database that is dropped afterwards. It
# NEVER issues DROP/ALTER/UPDATE against a real production database.
#
# Output: $SNAPSHOT_ROOT/<yyyymmdd-hhmm>/<db>.dump  (pg_dump -Fc)  + manifest.txt
# Keeps the most recent $RETAIN_SNAPSHOTS snapshot directories.
#
# Usage (on the server):
#   sudo bash build-snapshot.sh [--db <core-db-name>]
#
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=/dev/null
source "$HERE/config.sh"

ONLY_DB=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --db) ONLY_DB="${2:?--db needs a value}"; shift 2 ;;
    *) echo "build-snapshot: unknown arg: $1" >&2; exit 2 ;;
  esac
done

# docker needs sudo unless we are already root.
DOCKER=(docker)
[[ "$(id -u)" -ne 0 ]] && DOCKER=(sudo docker)

PG="$PG_CONTAINER"

# exec_sql <db> <sql>  — run one statement, tuples-only. No -i: these use -c and
# must NOT attach stdin (a docker exec -i inside a while-read loop would drain the
# loop's own input and silently process only the first row).
exec_sql() { "${DOCKER[@]}" exec "$PG" psql -U postgres -d "$1" -Atc "$2"; }
# exec_ctl <sql>       — run against the maintenance DB (create/drop database).
exec_ctl() { "${DOCKER[@]}" exec "$PG" psql -U postgres -d postgres -c "$1" >/dev/null; }

in_list() { local n="$1"; shift; local x; for x in "$@"; do [[ "$x" == "$n" ]] && return 0; done; return 1; }

# --- pick target DBs ---------------------------------------------------------

targets=("${CORE_DBS[@]}")
if [[ -n "$ONLY_DB" ]]; then
  if [[ "$ONLY_DB" =~ $REFUSED_DB_REGEX ]]; then
    echo "REFUSED: $ONLY_DB (letmoe uses its own seed system)." >&2; exit 3
  fi
  in_list "$ONLY_DB" "${CORE_DBS[@]}" || { echo "not a core DB: $ONLY_DB" >&2; exit 2; }
  targets=("$ONLY_DB")
fi

TS="$(date +%Y%m%d-%H%M)"
DIR="$SNAPSHOT_ROOT/$TS"
mkdir -p "$DIR"
MANIFEST="$DIR/manifest.txt"
: > "$MANIFEST"
{
  echo "# dev-snapshot manifest — $TS (produced by build-snapshot.sh)"
  echo "# desensitised at source; see scripts/dev-snapshot/scrub/*.sql"
  echo "#"
  echo "# db.table  rows"
} >> "$MANIFEST"

# count_tables <db-to-query> <label-db> — append exact row counts to manifest.
count_tables() {
  local qdb="$1" label="$2" tables q t
  tables="$(exec_sql "$qdb" "SELECT table_name FROM information_schema.tables WHERE table_schema='public' AND table_type='BASE TABLE' ORDER BY table_name")"
  q=""
  while IFS= read -r t; do
    [[ -z "$t" ]] && continue
    q+="SELECT '$t' AS t, count(*) AS n FROM \"$t\" UNION ALL "
  done <<< "$tables"
  q="${q% UNION ALL }"
  [[ -z "$q" ]] && return 0
  exec_sql "$qdb" "$q ORDER BY t" | while IFS='|' read -r t n; do
    printf '%s.%s  %s\n' "$label" "$t" "$n" >> "$MANIFEST"
  done
}

echo ">> snapshot dir: $DIR"
declare -a SUMMARY=()

for db in "${targets[@]}"; do
  echo ">> building $db"
  local_dump="$DIR/$db.dump"

  if in_list "$db" "${SCRUB_DBS[@]}"; then
    scratch="dev_snapshot_scratch_${db}"
    scrub="$HERE/scrub/${db}.sql"
    [[ -f "$scrub" ]] || { echo "missing scrub file: $scrub" >&2; exit 4; }

    # Fresh scratch DB, prod copied in READ-ONLY (pg_dump), scrubbed, dumped, dropped.
    exec_ctl "DROP DATABASE IF EXISTS $scratch;"
    exec_ctl "CREATE DATABASE $scratch;"
    "${DOCKER[@]}" exec -i "$PG" bash -c \
      "pg_dump -U postgres -d '$db' | psql -q -v ON_ERROR_STOP=1 -U postgres -d '$scratch' >/dev/null"

    "${DOCKER[@]}" exec -i "$PG" psql -q -v ON_ERROR_STOP=1 \
      -v dev_argon2="$DEV_ARGON2" \
      -v dev_bcrypt="$DEV_BCRYPT" \
      -v email_domain="$DEV_EMAIL_DOMAIN" \
      -v marker="$SCRUB_MARKER" \
      -U postgres -d "$scratch" < "$scrub" >/dev/null

    # kun_galgame_infra: derive per-client dev secrets in shell (no pgcrypto in
    # the artifact schema). sha256("dev-secret-"+id), stored as "sha256:<hex>".
    # Read every id FIRST (mapfile), then update — so the per-row docker exec
    # never competes with the loop for stdin.
    if [[ "$db" == "kun_galgame_infra" ]]; then
      cids=(); cid=""; h=""
      mapfile -t cids < <(exec_sql "$scratch" "SELECT id FROM oauth_clients")
      for cid in "${cids[@]}"; do
        [[ -z "$cid" ]] && continue
        h="$(printf '%s%s' "$DEV_SECRET_PREFIX" "$cid" | sha256sum | awk '{print $1}')"
        exec_sql "$scratch" "UPDATE oauth_clients SET secret='sha256:$h' WHERE id='$cid'" >/dev/null
      done
    fi

    "${DOCKER[@]}" exec "$PG" pg_dump -Fc -U postgres -d "$scratch" > "$local_dump"
    count_tables "$scratch" "$db"
    exec_ctl "DROP DATABASE IF EXISTS $scratch;"
  else
    # Passthrough: PII survey confirmed zero sensitive data. Dump prod directly.
    "${DOCKER[@]}" exec "$PG" pg_dump -Fc -U postgres -d "$db" > "$local_dump"
    count_tables "$db" "$db"
  fi

  bytes="$(stat -c %s "$local_dump")"
  SUMMARY+=("$db  $(numfmt --to=iec "$bytes" 2>/dev/null || echo "${bytes}B")  ${local_dump##*/}")
done

{
  echo "#"
  echo "# --- summary (db  dump_size  file) ---"
  for s in "${SUMMARY[@]}"; do echo "# $s"; done
} >> "$MANIFEST"

# Rotate: keep newest $RETAIN_SNAPSHOTS timestamped dirs.
mapfile -t all_snaps < <(find "$SNAPSHOT_ROOT" -mindepth 1 -maxdepth 1 -type d -printf '%f\n' \
  | grep -E '^[0-9]{8}-[0-9]{4}$' | sort)
if (( ${#all_snaps[@]} > RETAIN_SNAPSHOTS )); then
  drop=$(( ${#all_snaps[@]} - RETAIN_SNAPSHOTS ))
  for old in "${all_snaps[@]:0:drop}"; do
    echo ">> rotating out old snapshot: $old"
    rm -rf "${SNAPSHOT_ROOT:?}/$old"
  done
fi

echo ">> done."
# Last line = the snapshot dir (the local script captures this).
echo "SNAPSHOT_DIR=$DIR"
