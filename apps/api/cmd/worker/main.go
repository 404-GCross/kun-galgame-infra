package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"api/internal/infrastructure/database"
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

	// Initialize database
	db, err := database.NewPostgresDB(cfg.Database)
	if err != nil {
		slog.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// TODO: Initialize asynq server
	// server := asynq.NewServer(
	// 	asynq.RedisClientOpt{Addr: cfg.Redis.Addr, Password: cfg.Redis.Password, DB: cfg.Redis.DB},
	// 	asynq.Config{
	// 		Concurrency: 10,
	// 		Queues: map[string]int{
	// 			"critical": 6,
	// 			"default":  3,
	// 			"low":      1,
	// 		},
	// 	},
	// )

	// TODO: Register task handlers
	// mux := asynq.NewServeMux()
	// mux.HandleFunc(tasks.TypeModerationJob, handleModerationJob)
	// mux.HandleFunc(tasks.TypeArtifactProcess, handleArtifactProcess)

	slog.Info("starting worker...")

	// Graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// TODO: Start asynq server
	// go func() {
	// 	if err := server.Run(mux); err != nil {
	// 		slog.Error("worker error", "error", err)
	// 	}
	// }()

	// Wait for interrupt
	<-ctx.Done()
	slog.Info("shutting down worker...")

	// TODO: Shutdown asynq server
	// server.Shutdown()

	slog.Info("worker stopped")
}
