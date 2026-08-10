package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"

	"api/internal/infrastructure/database"
	"api/internal/platform/catalog/repository"
	"api/internal/platform/catalog/service"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env")
	if cfg, err := config.Load(); err == nil {
		logger.Init(cfg.Server.Env)
	}
	if *dsn == "" {
		slog.Error("refresh-tag-counts: --dsn is required")
		os.Exit(2)
	}

	db, err := database.OpenJob(*dsn)
	if err != nil {
		slog.Error("connect catalog db", "error", err)
		os.Exit(1)
	}
	if sqlDB, e := db.DB(); e == nil {
		defer sqlDB.Close()
	}

	svc := service.NewPublicService(db, service.NewReadService(db),
		service.NewResolveService(repository.NewRedirectRepository(db)), "")

	started := time.Now()
	st, err := svc.RefreshTagWorkCounts(context.Background(), started)
	if err != nil {
		slog.Error("refresh tag counts", "error", err)
		os.Exit(1)
	}
	slog.Info("refresh-tag-counts done",
		"rows", st.Rows, "pruned", st.Pruned, "took", time.Since(started).String())
}
