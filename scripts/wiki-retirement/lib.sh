#!/usr/bin/env bash
# Shared helpers + connection env for the galgame-wiki retirement (W1) scripts.
#
# Connection: reads the standard KUN_PG_* variables (same names cmd/* use), so
# `source apps/api/.env` (or exporting them) wires production and local alike.
# Nothing here writes to a source DB — the callers enforce read-only-on-source.

set -euo pipefail

PGHOST="${KUN_PG_HOST:-localhost}"
PGPORT="${KUN_PG_PORT:-5432}"
PGUSER="${KUN_PG_USER:-postgres}"
export PGPASSWORD="${KUN_PG_PASSWORD:?KUN_PG_PASSWORD must be set (source apps/api/.env)}"
export PGHOST PGPORT PGUSER

# psql that aborts on the first error and prints nothing but the value.
psql_val() { psql -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -v ON_ERROR_STOP=1 -tAqc "$2" -d "$1"; }
psql_do()  { psql -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -v ON_ERROR_STOP=1 -d "$1" "${@:2}"; }

# The exact set of tables W1 moves: every table in the wiki DB's public schema
# (galgame_* + the one unprefixed taxonomy_revision). Derived from the SOURCE at
# run time — never hard-coded — so a new wiki table is picked up automatically.
wiki_tables() {
  psql_val "$1" "SELECT tablename FROM pg_tables WHERE schemaname='public' ORDER BY tablename"
}
