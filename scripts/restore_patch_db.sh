#!/usr/bin/env zsh
set -e

DB_NAME="kungalgame_patch"
DB_USER="postgres"
DB_HOST="localhost"
DB_PORT="5432"
BACKUP_FILE="./kungalgame_patch_backup.dump"

echo "▶ Dropping database: $DB_NAME"
psql -U "$DB_USER" -h "$DB_HOST" -p "$DB_PORT" \
  -c "DROP DATABASE IF EXISTS $DB_NAME;"

echo "▶ Creating database: $DB_NAME"
psql -U "$DB_USER" -h "$DB_HOST" -p "$DB_PORT" \
  -c "CREATE DATABASE $DB_NAME;"

echo "▶ Restoring backup from:"
echo "  $BACKUP_FILE"
pg_restore -U "$DB_USER" -h "$DB_HOST" -p "$DB_PORT" \
  -d "$DB_NAME" -n public -F c "$BACKUP_FILE"

echo "✅ Database $DB_NAME has been replaced with backup data."
