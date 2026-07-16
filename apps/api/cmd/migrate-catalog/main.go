// cmd/migrate-catalog — the SINGLE migration entry point for the content hub
// (wiki-retirement W5, charter ruling 6). It migrates TWO model families over
// two independent connection pools:
//
//   - galgame (wiki-family) models → cfg.GalgameDatabase (KUN_GALGAME_PG_DATABASE)
//   - catalog models + seeds       → cfg.CatalogDatabase (KUN_CATALOG_PG_DATABASE)
//
// In production both env vars point at kun_catalog (one database, two disjoint
// table families — W1 verified zero collision); in local dev they stay split
// (kun_galgame_wiki / kun_catalog). Both postures are correct by construction
// because each family only ever touches its own pool.
//
// Order: galgame first, catalog second. The families share no DDL objects, so
// the order is convention, not dependency — galgame-first keeps the catalog
// registry seeds as the very last step, exactly like the pre-W5 flow where
// the retired cmd/migrate-galgame and this binary ran independently.
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

	// ── pool 1: galgame (wiki-family) schema ────────────────────────────────
	slog.Info("connecting to galgame content database", "dbname", cfg.GalgameDatabase.DBName)
	galgameDB, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		slog.Error("failed to connect to galgame content database", "error", err)
		os.Exit(1)
	}
	migrateGalgame(galgameDB) // exits the process on failure
	if err := galgameDB.Close(); err != nil {
		slog.Error("failed to close galgame content database", "error", err)
	}

	// ── pool 2: catalog schema + registry seeds ─────────────────────────────
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
