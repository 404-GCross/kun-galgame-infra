// cmd/migrate-trust is the single migration entry point for the kun_trust
// database (Trust & Safety platform, doc 18). The trust service itself never
// migrates on startup — it only connects. Idempotent: AutoMigrate + CREATE
// INDEX IF NOT EXISTS + seed upserts, so a per-deploy re-run is a fast no-op.
package main

import (
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
	"api/internal/platform/trust/migrate"
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
	slog.Info("connecting to trust database", "dbname", cfg.TrustDatabase.DBName)

	db, err := database.NewPostgresDB(cfg.TrustDatabase)
	if err != nil {
		slog.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// Schema (tables + idempotent raw SQL + reason seed) lives in the migrate
	// package so the integration test provisions its database with the exact
	// production schema.
	slog.Info("running trust migrations...")
	if err := migrate.Run(db.DB()); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}

	slog.Info("trust migration completed successfully")
}
