#!/usr/bin/env bash
# Emits a sorted, stable fingerprint of every DDL object in a DB's public schema
# (tables, indexes, sequences, constraints). Diffing the fingerprint before/after
# an operation shows exactly which objects it created/dropped/changed — the proof
# for 章程裁定 6 (the galgame and catalog migration bodies coexist in one DB and
# never touch each other's tables — both live inside cmd/migrate-catalog since
# W5). Filter by prefix to isolate a family, e.g.:
#
#   scripts/wiki-retirement/object-fingerprint.sh kun_catalog_w1 | grep -E ' (catalog|src)_' > before.catalog
#   ... run the galgame migration body against kun_catalog_w1 ...
#   scripts/wiki-retirement/object-fingerprint.sh kun_catalog_w1 | grep -E ' (catalog|src)_' > after.catalog
#   diff before.catalog after.catalog   # must be empty
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$HERE/lib.sh"
DB="${1:?usage: object-fingerprint.sh <dbname>}"

psql -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -v ON_ERROR_STOP=1 -tAq -d "$DB" -c "
  SELECT 'TABLE '||tablename FROM pg_tables WHERE schemaname='public'
  UNION ALL SELECT 'INDEX '||tablename||'.'||indexname FROM pg_indexes WHERE schemaname='public'
  UNION ALL SELECT 'SEQ '||sequence_name FROM information_schema.sequences WHERE sequence_schema='public'
  UNION ALL SELECT 'CON '||r.relname||'.'||c.conname||' '||c.contype::text
              FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid
              JOIN pg_namespace n ON n.oid=r.relnamespace WHERE n.nspname='public'
  ORDER BY 1;"
