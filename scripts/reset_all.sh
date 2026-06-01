#!/usr/bin/env zsh
#
# reset_all.sh — Reset all databases before running the migration pipeline.
#
# What it does, in order:
#
#   1. Drops + recreates the 3 application databases (empty; the apps'
#      own schema-migration commands will rebuild structure):
#        - kun_galgame_infra   (OAuth target / user_migrations)
#        - kun_images_dev    (image_service)
#        - kun_galgame_wiki  (galgame_wiki)
#
#   2. Drops + recreates + restores the 2 source databases from local dumps:
#        - kungalgame        ← kungalgame_backup.dump
#        - kungalgame_patch  ← kungalgame_patch_backup.dump
#
# After this script finishes, the 5 databases are in their pre-migration
# pristine state and you can run the migration commands in sequence
# (see docs/migration/user/05-execution.md and
#       docs/galgame_wiki/02-moyu-migration-design.md).
#
# DESTRUCTIVE — drops 5 databases without confirmation. If you have dev
# data in any of them you care about, dump it first.
#
# Usage:
#   ./scripts/reset_all.sh            # from repo root, OR
#   cd scripts && ./reset_all.sh      # from scripts dir
#

set -e

DB_USER="postgres"
DB_HOST="localhost"
DB_PORT="5432"

# 3 application databases — drop + create empty
APP_DBS=(kun_galgame_infra kun_images_dev kun_galgame_wiki)

# 2 source databases — drop + create + restore from .dump in same dir
typeset -A SOURCE_DBS
SOURCE_DBS=(
    kungalgame        kungalgame_backup.dump
    kungalgame_patch  kungalgame_patch_backup.dump
)

# Resolve script dir so backup files are found regardless of cwd
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

run_psql() {
    psql -U "$DB_USER" -h "$DB_HOST" -p "$DB_PORT" "$@"
}

# reset_empty_db NAME → drop + recreate, leave empty
reset_empty_db() {
    local db="$1"
    echo "▶ Resetting application database: $db"
    run_psql -c "DROP DATABASE IF EXISTS $db;"
    run_psql -c "CREATE DATABASE $db;"
    echo "✅ $db recreated (empty)"
}

# restore_source_db NAME DUMP_FILENAME → drop + recreate + pg_restore
# DUMP_FILENAME is relative to $SCRIPT_DIR.
restore_source_db() {
    local db="$1"
    local dump="$SCRIPT_DIR/$2"

    if [[ ! -f "$dump" ]]; then
        echo "✗ Backup file not found: $dump" >&2
        echo "  Expected dump file relative to scripts/ directory." >&2
        return 1
    fi

    echo "▶ Resetting source database: $db"
    run_psql -c "DROP DATABASE IF EXISTS $db;"
    run_psql -c "CREATE DATABASE $db;"

    echo "▶ Restoring $db from $(basename "$dump")..."
    pg_restore -U "$DB_USER" -h "$DB_HOST" -p "$DB_PORT" \
        -d "$db" -n public -F c "$dump"
    echo "✅ $db restored from backup"
}

echo "═══════════════════════════════════════════════════════════════"
echo "  Resetting all databases for migration run"
echo "═══════════════════════════════════════════════════════════════"

# Sanity check: can we even reach Postgres?
if ! run_psql -c '\q' >/dev/null 2>&1; then
    echo "✗ Cannot connect to PostgreSQL at $DB_HOST:$DB_PORT as $DB_USER" >&2
    echo "  Check pg_hba.conf / .pgpass / server status before retrying." >&2
    exit 1
fi

# ── Step 1: drop + recreate empty application DBs ──
for db in "${APP_DBS[@]}"; do
    echo
    reset_empty_db "$db"
done

# ── Step 2: drop + recreate + restore source DBs from backup ──
for db in "${(@k)SOURCE_DBS}"; do
    echo
    restore_source_db "$db" "${SOURCE_DBS[$db]}"
done

echo
echo "═══════════════════════════════════════════════════════════════"
echo "  ✅ All 5 databases ready"
echo "═══════════════════════════════════════════════════════════════"
echo "    application (empty): ${APP_DBS[*]}"
echo "    source (restored):   ${(@k)SOURCE_DBS}"
echo
echo "Next: run schema migrations and data migrations from apps/api."
echo "See docs/migration/user/05-execution.md for the full sequence."
