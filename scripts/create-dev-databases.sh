#!/usr/bin/env bash
# Create the databases the dev stack expects, on YOUR Postgres server.
#
# docker/initdb.d/01-create-databases.sh only runs when Postgres itself
# initialises an empty data dir — and Postgres is deliberately NOT a compose
# service here (the box's own server is the source of truth), so on a fresh
# checkout it never runs at all. That is one of the two things behind a migrate
# job that exits 1 seconds after start; this script is the other half of the fix.
#
# Idempotent: databases that already exist are left alone, never dropped.
#
#   ./scripts/create-dev-databases.sh          # create what's missing
#   ./scripts/create-dev-databases.sh --check  # report only, create nothing
set -euo pipefail
cd "$(dirname "$0")/.."

CHECK_ONLY=0
[[ "${1:-}" == "--check" ]] && CHECK_ONLY=1

command -v psql >/dev/null || {
  echo "✗ psql not on PATH. Install the Postgres client:"
  echo "    Debian/Ubuntu (incl. WSL2):  sudo apt install postgresql-client"
  echo "    Arch:                        sudo pacman -S postgresql-libs"
  exit 1
}

# The coordinates come from the rendered compose config, NOT from a second copy
# of the defaults: that way a root .env override (KUN_PG_HOST / _PORT / _USER /
# _PASSWORD) applies here and to the containers identically, and there is no
# way for the two to disagree. `compose config` needs the CLI but not a running
# daemon.
eval "$(docker compose -f docker-compose.dev.yml config --format json \
  | jq -r '.services.migrate.environment
           | "PGHOST=\(.KUN_PG_HOST)\nPGPORT=\(.KUN_PG_PORT)\nPGUSER=\(.KUN_PG_USER)\nPGPASSWORD=\(.KUN_PG_PASSWORD|@sh)"')"
export PGHOST PGPORT PGUSER PGPASSWORD

# The database list has exactly one home — the initdb hook above. Reading the
# names out of it keeps this script from becoming a second list that silently
# goes stale the next time a domain is added.
mapfile -t DBS < <(sed -n 's/^[[:space:]]*CREATE DATABASE \([a-zA-Z0-9_]*\);.*/\1/p' \
  docker/initdb.d/01-create-databases.sh)
# The entrypoint creates POSTGRES_DB itself, so the hook never lists it.
DBS=(kun_galgame_infra "${DBS[@]}")

echo "▶ Postgres ${PGUSER}@${PGHOST}:${PGPORT} — checking ${#DBS[@]} databases"

# One round trip for the whole inventory. Per-database SELECTs would also work,
# but a server carrying a collation-version mismatch reprints its WARNING on
# every connection, and twelve copies of it buries this script's own output.
if ! present=$(psql -d postgres -tAc 'SELECT datname FROM pg_database' 2>/dev/null); then
  echo "✗ cannot connect. Most likely: the server isn't running, or the password"
  echo "  differs from the compose default. Override it in a root .env (gitignored):"
  echo "      KUN_PG_PASSWORD=whatever-your-local-postgres-uses"
  exit 1
fi

created=0 existing=0
for db in "${DBS[@]}"; do
  if grep -qxF "$db" <<<"$present"; then
    existing=$((existing + 1))
    continue
  fi
  if ((CHECK_ONLY)); then
    echo "  · missing: $db"
    created=$((created + 1))
    continue
  fi
  psql -d postgres -q -c "CREATE DATABASE \"$db\""
  echo "  + created $db"
  created=$((created + 1))
done

if ((CHECK_ONLY)); then
  ((created)) && { echo "✗ $created missing — run without --check to create them"; exit 1; }
  echo "✓ all $existing databases present"
else
  echo "✓ $created created, $existing already present"
fi
