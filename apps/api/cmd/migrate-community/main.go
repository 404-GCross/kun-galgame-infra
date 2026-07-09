package main

import (
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
	"api/internal/platform/community/migrate"
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
	slog.Info("connecting to community database", "dbname", cfg.CommunityDatabase.DBName)

	db, err := database.NewPostgresDB(cfg.CommunityDatabase)
	if err != nil {
		slog.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// Schema (tables + idempotent raw SQL) lives in the migrate package so the
	// integration test provisions its database with the exact production
	// schema. This step has no seeds — the default board is planted per-site at
	// letmoe cut-over.
	slog.Info("running community migrations...")
	if err := migrate.Run(db.DB()); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}

	slog.Info("community migration completed successfully")
}
