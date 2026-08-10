// refresh-tag-counts recomputes catalog_tag_work_count — the work_count behind
// every canonical-tag chip, precomputed because the tag edge is the one
// taxonomy edge whose live aggregate is too expensive to pay per request (see
// model.CatalogTagWorkCount).
//
// It is a full recompute, not an increment: two passes over the tag edge, a
// batched upsert and a delete of everything the passes did not produce, all in
// one transaction. Running it twice in a row is a no-op beyond a new
// computed_at, so it is safe on a schedule and safe to re-run after a failure.
//
// Run it whenever the numbers could have moved — after a tag import, after a
// tag merge, after a claim wave — and on a schedule for everything else. The
// read face degrades to the live aggregate only while the table has never been
// filled at all; once it holds rows, a tag missing from it reads as zero, so a
// long gap between runs shows up as stale numbers rather than absent ones.
//
// --dsn is REQUIRED and never defaulted (repo convention: a maintenance tool
// must not discover which database it is about to rewrite).
//
//	go run ./cmd/refresh-tag-counts --dsn "host=localhost port=5432 user=postgres dbname=kun_catalog sslmode=disable"
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

	_ = godotenv.Load("apps/api/.env") // allow running from the repo root
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

	// The refresh is a method on the read service on purpose: it composes the
	// population gates from the very same helpers the read path does, so the
	// stored numbers cannot mean something different from the served ones. The
	// CDN base is irrelevant here — nothing this call touches renders a URL.
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
