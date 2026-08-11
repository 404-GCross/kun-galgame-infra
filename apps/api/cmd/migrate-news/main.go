package main

import (
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
	"api/internal/platform/news/migrate"
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
	slog.Info("connecting to news database", "dbname", cfg.NewsDatabase.DBName)

	db, err := database.NewPostgresDB(cfg.NewsDatabase)
	if err != nil {
		slog.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	slog.Info("running news migrations...")
	if err := migrate.Run(db.DB()); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}

	slog.Info("news migration completed successfully")
}
