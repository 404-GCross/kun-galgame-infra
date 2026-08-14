package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/jobs/bgmworkmeta"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run, counters + samples only)")
	dsn := flag.String("dsn", "", "catalog DSN (also hosts src_bangumi) — REQUIRED; the rehearsal copy locally, the live catalog only in the acceptance run")
	limit := flag.Int("limit", 0, "max candidate works (0 = all)")
	offset := flag.Int("offset", 0, "skip this many candidate works (for chunking)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env")

	if cfg, err := config.Load(); err == nil {
		logger.Init(cfg.Server.Env)
	}

	st, err := bgmworkmeta.Run(context.Background(), bgmworkmeta.Opts{
		Apply:  *apply,
		DSN:    *dsn,
		Limit:  *limit,
		Offset: *offset,
	})
	if err != nil {
		slog.Error("backfill-bgm-work-meta", "error", err)
		os.Exit(1)
	}
	slog.Info("backfill-bgm-work-meta summary",
		"apply", *apply,
		"candidates", st.Candidates,
		"meta_no_tags", st.MetaNoTags,
		"meta_not_array", st.MetaNotArray,
		"meta_name_blank", st.MetaNameBlank,
		"meta_dup", st.MetaDup,
		"meta_planned", st.MetaPlanned,
		"meta_distinct_names", st.MetaDistinct,
		"meta_written", st.MetaWritten,
		"meta_conflict", st.MetaConflict,
		"fav_no_object", st.FavNoObject,
		"fav_unknown_key", st.FavUnknownKey,
		"fav_bad_value", st.FavBadValue,
		"fav_planned", st.FavPlanned,
		"fav_written", st.FavWritten,
		"fav_unchanged", st.FavUnchanged,
		"errors", st.Errors,
	)
	if !*apply {
		slog.Info("DRY RUN — nothing written; re-run with --apply")
	}
	// 2026-08-05: the sibling backfill-work-tags run logged errors=120646 and still exited 0 - nothing fired.
	// The per-row cause is at WARN under "write meta tag" / "write favorite shelf", neither of which names
	// this tool, so the weekly log read as a bare errors=N for 15 days. Restate the first one here.
	if st.Errors > 0 {
		slog.Error("backfill-bgm-work-meta write failures", "errors", st.Errors, "first_error", st.FirstError)
		os.Exit(1)
	}
}
