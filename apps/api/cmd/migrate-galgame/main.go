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
	// We drop the AutoMigrate-created plain unique (if any) and create the
	// partial unique. Both DDL guards are idempotent via IF EXISTS / IF NOT EXISTS.
	postSQL := []string{
		`DROP INDEX IF EXISTS idx_galgame_vndb_id`,           // AutoMigrate index name (uniqueIndex → idx_*)
		`DROP INDEX IF EXISTS uni_galgame_vndb_id`,           // alt GORM naming
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_galgame_vndb_id_nonempty
		    ON galgame(vndb_id) WHERE vndb_id <> ''`,
	}
	for _, stmt := range postSQL {
		if err := db.DB().Exec(stmt).Error; err != nil {
			slog.Error("post-migration SQL failed", "stmt", stmt, "error", err)
			os.Exit(1)
		}
	}

	slog.Info("galgame wiki migration completed successfully")
}
