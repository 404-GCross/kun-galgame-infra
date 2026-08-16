#!/usr/bin/env bash
set -euo pipefail

# Auth comes from the ~/.pgpass localhost:postgres entry; the DSN this script
# prints deliberately carries no password (pgx reads the same pgpass file).
PSQL=(env PGOPTIONS=-c\ client_min_messages=error psql -h localhost -U postgres -d postgres -v ON_ERROR_STOP=1 -qAt)
PATTERN='^kun_ephem_[0-9]{8}_[a-z0-9_]{1,24}$'

usage() {
  cat >&2 <<'EOF'
Self-service throwaway test databases (CLAUDE.md iron rule 14).

  ephemeral-test-db.sh create <slug>   create kun_ephem_<yyyymmdd>_<slug>, print its DSN
  ephemeral-test-db.sh drop <name>     drop one ephemeral database
  ephemeral-test-db.sh list            list ephemeral databases
  ephemeral-test-db.sh sweep           drop ephemeral databases older than 2 days

Drop your database when the session's DB work is done; sweep clears leftovers
from sessions that died before cleaning up.
EOF
  exit 2
}

cmd="${1:-}"
case "$cmd" in
create)
  slug="${2:-}"
  [[ "$slug" =~ ^[a-z0-9_]{1,24}$ ]] || { echo "create: slug must match [a-z0-9_]{1,24}" >&2; exit 2; }
  name="kun_ephem_$(date +%Y%m%d)_${slug}"
  "${PSQL[@]}" -c "CREATE DATABASE \"$name\"" >/dev/null
  echo "$name"
  echo "TEST_DATABASE_DSN=host=localhost port=5432 user=postgres dbname=$name sslmode=disable"
  ;;
drop)
  name="${2:-}"
  [[ "$name" =~ $PATTERN ]] || { echo "drop: refusing '$name' — not an ephemeral database name" >&2; exit 2; }
  "${PSQL[@]}" -c "DROP DATABASE IF EXISTS \"$name\" WITH (FORCE)" >/dev/null
  echo "dropped $name"
  ;;
list)
  "${PSQL[@]}" -c "SELECT datname FROM pg_database WHERE datname LIKE 'kun_ephem_%' ORDER BY datname"
  ;;
sweep)
  cutoff="$(date -d '2 days ago' +%Y%m%d)"
  while IFS= read -r name; do
    [[ "$name" =~ $PATTERN ]] || continue
    stamp="${name:10:8}"
    if [[ "$stamp" < "$cutoff" ]]; then
      "${PSQL[@]}" -c "DROP DATABASE IF EXISTS \"$name\" WITH (FORCE)" >/dev/null
      echo "swept $name"
    fi
  done < <("${PSQL[@]}" -c "SELECT datname FROM pg_database WHERE datname LIKE 'kun_ephem_%'")
  ;;
*)
  usage
  ;;
esac
