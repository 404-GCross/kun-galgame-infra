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

	_ = godotenv.Load("apps/api/.env")

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
