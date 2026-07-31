// cmd/migrate-catalog — the migration entry point for the content hub: the
// catalog models + registry seeds against cfg.CatalogDatabase.
//
// It carried a SECOND family until wave 161. Since wiki-retirement W5 it also
// AutoMigrated the galgame (wiki) models against cfg.GalgameDatabase, which is
// how those 27 tables got (re)created on every single deploy. That half is
// deleted here, and the deletion is load-bearing rather than tidy-up: with it
// still in place, the N5 window would DROP the family and the very next deploy
// would silently recreate all 27 tables as empty shells — leaving the registry
// looking healthy while every consumer read nothing.
//
// cfg.GalgameDatabase is consequently no longer opened by this binary.
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

	// ── catalog schema + registry seeds ─────────────────────────────────────
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
