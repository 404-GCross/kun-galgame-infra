// enrich-bgm-summaries fills catalog_work_intro rows for galgame works from
// Bangumi summaries (BGM enrichment wave, refs/proj/57; population + language
// rework in refs/proj/166). The EXACT anchors point each work at a
// src_bangumi.subject whose summary is already in the catalog DB — src_bangumi
// is a schema INSIDE kun_catalog, so ONE --dsn covers both sides (no API, no
// token, no bytes, no schema changes).
//
// Fill-missing-language discipline: a summary is written in its detected
// language ONLY when the work has no intro row in that language yet (any
// source) — so a work whose DLsite ja intro already exists skips its duplicate
// ja summary, and a claimed work's own curated text is never displaced. Logic
// lives in internal/jobs/bgmsummaries. Idempotent: a second --apply writes zero.
//
// Dry-run is the DEFAULT (repo convention); pass --apply to write. --dsn is
// REQUIRED and never defaulted — the rehearsal copy locally
// (kun_catalog_rehearsal), the live catalog only in the acceptance run.
//
//	# dry-run: report zh_new / ja_fill / skip_dup_lang / no_summary / no_lang
//	go run ./cmd/enrich-bgm-summaries \
//	    --dsn "host=127.0.0.1 port=5432 user=postgres password=... dbname=kun_catalog_rehearsal sslmode=disable"
//
//	# apply, published works only (the staged 166 rollout). NOT "claimed":
//	# claimed includes the draft sea, roughly 3x the rows and 3x the downstream
//	# translation bill.
//	go run ./cmd/enrich-bgm-summaries --apply --population published \
//	    --dsn "host=127.0.0.1 port=5432 user=postgres password=... dbname=kun_catalog_rehearsal sslmode=disable"
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/jobs/bgmsummaries"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run, counters + samples only)")
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED; the rehearsal copy locally (kun_catalog_rehearsal), the live catalog only in the acceptance run")
	population := flag.String("population", bgmsummaries.PopulationAll, "which works to enrich: all | bodyless | claimed | published")
	limit := flag.Int("limit", 0, "max candidate works to process (0 = all)")
	offset := flag.Int("offset", 0, "skip this many candidate works (for chunking)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env") // allow running from the repo root

	// config drives only logging here; the DB is reached exclusively via --dsn.
	if cfg, err := config.Load(); err == nil {
		logger.Init(cfg.Server.Env)
	}

	st, err := bgmsummaries.Run(context.Background(), bgmsummaries.Opts{
		Apply:      *apply,
		DSN:        *dsn,
		Population: *population,
		Limit:      *limit,
		Offset:     *offset,
	})
	if err != nil {
		slog.Error("enrich-bgm-summaries", "error", err)
		os.Exit(1)
	}
	slog.Info("enrich-bgm-summaries done",
		"apply", *apply,
		"population", *population,
		"candidates", st.Candidates,
		"no_summary", st.NoSummary,
		"no_lang", st.NoLang,
		"skip_dup_lang", st.SkipDupLang,
		"zh_new", st.ZhNew,
		"ja_fill", st.JaFill,
		"zh_written", st.ZhWritten,
		"ja_written", st.JaWritten,
		"claimed_written", st.ClaimedWritten,
		"conflict", st.Conflict,
		"errors", st.Errors,
	)
}
