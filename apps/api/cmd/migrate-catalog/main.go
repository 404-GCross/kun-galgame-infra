package main

import (
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
	"api/internal/platform/catalog/model"
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

	slog.Info("running catalog migrations...")

	if err := db.AutoMigrate(
		// Registry (vocabulary) tables, doc 17 R1. catalog_role before
		// catalog_source_role_map so the role_id FK can be created.
		&model.CatalogMedium{},
		&model.CatalogSource{},
		&model.CatalogRole{},
		&model.CatalogSourceRoleMap{},
		&model.CatalogRelationType{},
	); err != nil {
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
