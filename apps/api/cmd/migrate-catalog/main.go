package main

import (
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
	"api/internal/platform/catalog/migrate"
	"api/internal/platform/catalog/seed"
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
	slog.Info("connecting to catalog database", "dbname", cfg.CatalogDatabase.DBName)

	db, err := database.NewPostgresDB(cfg.CatalogDatabase)
	if err != nil {
		slog.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// Schema (tables + idempotent raw SQL) lives in the migrate package so
	// the integration tests provision their database with the exact
	// production schema.
	slog.Info("running catalog migrations...")
	if err := migrate.Run(db.DB()); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}

	// Seeds run on every migrate (idempotent upserts): registry vocabularies
	// are data, and this is their single write path outside manual curation.
	slog.Info("seeding catalog registries...")
	if err := seed.Run(db.DB()); err != nil {
		slog.Error("seeding failed", "error", err)
		os.Exit(1)
	}

	slog.Info("catalog migration completed successfully")
}
