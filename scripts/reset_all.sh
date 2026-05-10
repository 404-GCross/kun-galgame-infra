#!/usr/bin/env zsh
#
# reset_all.sh — Reset all databases before running the migration pipeline.
#
# What it does, in order:
#
#   1. Drops + recreates the 3 application databases (empty; the apps'
#      own schema-migration commands will rebuild structure):
#        - kun_oauth_admin   (OAuth target / wiki user_migrations)
#        - kun_images_dev    (image_service)
#        - kun_galgame_wiki  (galgame_wiki)
#
#   2. Calls the existing source-DB restore scripts:
#        - ./restore_db.sh        → kungalgame       (from kungalgame_backup.dump)
#        - ./restore_patch_db.sh  → kungalgame_patch (from kungalgame_patch_backup.dump)
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
APP_DBS=(kun_oauth_admin kun_images_dev kun_galgame_wiki)

# Resolve script dir so sibling restore scripts work regardless of cwd
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

run_psql() {
    psql -U "$DB_USER" -h "$DB_HOST" -p "$DB_PORT" "$@"
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
for DB in "${APP_DBS[@]}"; do
    echo
    echo "▶ Resetting application database: $DB"
    run_psql -c "DROP DATABASE IF EXISTS $DB;"
    run_psql -c "CREATE DATABASE $DB;"
    echo "✅ $DB recreated (empty)"
done

# ── Step 2: restore source DBs from backups (kungal + moyu) ──
echo
echo "▶ Restoring kungal source database from backup..."
( cd "$SCRIPT_DIR" && ./restore_db.sh )

echo
echo "▶ Restoring moyu source database from backup..."
( cd "$SCRIPT_DIR" && ./restore_patch_db.sh )

echo
echo "═══════════════════════════════════════════════════════════════"
echo "  ✅ All 5 databases ready"
echo "═══════════════════════════════════════════════════════════════"
echo "    application (empty): kun_oauth_admin, kun_images_dev, kun_galgame_wiki"
echo "    source (restored):   kungalgame, kungalgame_patch"
echo
echo "Next: run schema migrations and data migrations from apps/api."
echo "See docs/migration/user/05-execution.md for the full sequence."
