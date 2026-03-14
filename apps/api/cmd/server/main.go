package main

import (
	"log/slog"
	"os"

	"api/internal/app"
	"api/pkg/config"
	"api/pkg/logger"
)

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Initialize logger
	logger.Init(cfg.Server.Env)

	// Create and run application
	application, err := app.New(cfg)
	if err != nil {
		slog.Error("failed to create application", "error", err)
		os.Exit(1)
	}

	if err := application.Run(); err != nil {
		slog.Error("application error", "error", err)
		os.Exit(1)
	}
}
