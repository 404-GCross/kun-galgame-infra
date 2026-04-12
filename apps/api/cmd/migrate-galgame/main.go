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
	); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}

	slog.Info("galgame wiki migration completed successfully")
}
