package main

import (
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
	"api/internal/platform/galgame/model"
	"api/pkg/config"
	"api/pkg/logger"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	logger.Init(cfg.Server.Env)
	slog.Info("connecting to galgame wiki database", "dbname", cfg.GalgameDatabase.DBName)

	db, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		slog.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	slog.Info("running galgame wiki migrations...")

	if err := db.AutoMigrate(
		// Independent tables first (no FK to galgame)
		&model.GalgameSeries{},
		&model.GalgameTag{},
		&model.GalgameTagAlias{},
		&model.GalgameOfficial{},
		&model.GalgameOfficialAlias{},
		&model.GalgameEngine{},

		// Core galgame table (FK to galgame_series)
		&model.Galgame{},

		// Tables with FK to galgame
		&model.GalgameAlias{},
		&model.GalgameTagRelation{},
		&model.GalgameOfficialRelation{},
		&model.GalgameEngineRelation{},
		&model.GalgameLink{},
		&model.GalgamePR{},
		&model.GalgameRevision{},
		&model.GalgameHistory{}, // Legacy, kept for migration
		&model.GalgameContributor{},

		// Migration audit trail (mirrors auth/model.UserMigration). Records
		// (source_db, source_id) → galgame_id mapping for idempotent re-runs
		// of migrate-moyu-galgame and for future scripts that need to reverse-
		// look up legacy ids.
		&model.GalgameMigration{},

		// Submission / review event log. Consumed by:
		//   - admin web UI (queue: WHERE type='submitted'/'edited_pending')
		//   - end users via /messages/mine (approved/declined notifications)
		//   - kungal/moyu cron via /messages/feed
		// Read state is owned by each consumer, NOT this table.
		&model.GalgameMessage{},
	); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}

	// Post-AutoMigrate raw SQL:
	//
	// (1) vndb_id partial unique. Originally vndb_id had a plain unique index;
	// once user-submitted entries are allowed without VNDB (vndb_id = ''),
	// plain unique would block N entries from sharing the empty value. The
	// partial index enforces uniqueness only when vndb_id is non-empty.
	//
	// We must remove the previous plain unique BEFORE inserting any
	// vndb_id='' rows. Scan pg_indexes for ANY unique constraint touching
	// only the vndb_id column (whether it was named idx_*, uni_*, or
	// something user-defined) and drop them — except our partial unique,
	// which we then create idempotently.
	const partialUniqueName = "uq_galgame_vndb_id_nonempty"
	type indexRow struct {
		IndexName string `gorm:"column:indexname"`
	}
	var stale []indexRow
	if err := db.DB().Raw(`
		SELECT i.indexname
		FROM pg_indexes i
		JOIN pg_class c ON c.relname = i.indexname
		JOIN pg_index x ON x.indexrelid = c.oid
		JOIN pg_attribute a ON a.attrelid = x.indrelid AND a.attnum = ANY(x.indkey)
		WHERE i.schemaname = 'public'
		  AND i.tablename = 'galgame'
		  AND x.indisunique
		  AND array_length(x.indkey, 1) = 1
		  AND a.attname = 'vndb_id'
		  AND i.indexname <> ?
	`, partialUniqueName).Scan(&stale).Error; err != nil {
		slog.Error("scan stale vndb_id unique indexes", "error", err)
		os.Exit(1)
	}
	for _, idx := range stale {
		stmt := `DROP INDEX IF EXISTS "` + idx.IndexName + `"`
		slog.Info("dropping legacy unique index on vndb_id", "name", idx.IndexName)
		if err := db.DB().Exec(stmt).Error; err != nil {
			slog.Error("drop stale unique index failed", "stmt", stmt, "error", err)
			os.Exit(1)
		}
	}
	if err := db.DB().Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS ` + partialUniqueName + `
		    ON galgame(vndb_id) WHERE vndb_id <> ''
	`).Error; err != nil {
		slog.Error("create partial unique on vndb_id failed", "error", err)
		os.Exit(1)
	}

	// (2) galgame_revision.action CHECK. GORM's AutoMigrate creates this
	// constraint from the model tag only on a fresh table; it NEVER alters
	// an existing one. The submission/claim/admin-review flows added new
	// action values (claimed/edited_pending/approved/banned/unbanned/
	// status_changed) on top of the original created/updated/merged/
	// reverted/declined set, so existing wiki DBs still carry the stale
	// 5-value constraint and every POST /galgame/:gid/claim 23514s.
	//
	// Drop-then-add by the GORM default name. Idempotent: DROP IF EXISTS
	// tolerates first run / re-run; the new set is a strict superset of
	// the old so ADD CONSTRAINT validates all existing rows. Keep this
	// list in sync with the model tag in galgame/model/pr.go.
	if err := db.DB().Exec(`
		ALTER TABLE galgame_revision
		    DROP CONSTRAINT IF EXISTS chk_galgame_revision_action
	`).Error; err != nil {
		slog.Error("drop stale chk_galgame_revision_action failed", "error", err)
		os.Exit(1)
	}
	if err := db.DB().Exec(`
		ALTER TABLE galgame_revision
		    ADD CONSTRAINT chk_galgame_revision_action
		    CHECK (action IN (
		        'created','updated','merged','reverted','declined',
		        'claimed','edited_pending','approved','banned',
		        'unbanned','status_changed'
		    ))
	`).Error; err != nil {
		slog.Error("recreate chk_galgame_revision_action failed", "error", err)
		os.Exit(1)
	}

	slog.Info("galgame wiki migration completed successfully")
}
