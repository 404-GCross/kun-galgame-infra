package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/jobs/workratings"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run, counters + samples only)")
	dsn := flag.String("dsn", "", "catalog DSN (also hosts src_bangumi) — REQUIRED; the rehearsal copy locally, the live catalog only in the acceptance run")
	egDSN := flag.String("eg-dsn", "", "EG mirror DSN (the erogamescape database) — REQUIRED")
	dlsiteDSN := flag.String("dlsite-dsn", "", "DLsite mirror DSN (the dlsite database) — REQUIRED")
	limit := flag.Int("limit", 0, "max candidate works per lane (0 = all)")
	offset := flag.Int("offset", 0, "skip this many candidate works per lane (for chunking)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env")

	if cfg, err := config.Load(); err == nil {
		logger.Init(cfg.Server.Env)
	}

	st, err := workratings.Run(context.Background(), workratings.Opts{
		Apply:     *apply,
		DSN:       *dsn,
		EGDSN:     *egDSN,
		DlsiteDSN: *dlsiteDSN,
		Limit:     *limit,
		Offset:    *offset,
	})
	if err != nil {
		slog.Error("backfill-work-ratings", "error", err)
		os.Exit(1)
	}
	slog.Info("backfill-work-ratings summary",
		"apply", *apply,
		"bgm_candidates", st.BgmCandidates,
		"bgm_no_score", st.BgmNoScore,
		"bgm_planned", st.BgmPlanned,
		"bgm_written", st.BgmWritten,
		"bgm_unchanged", st.BgmUnchanged,
		"eg_candidates", st.EgCandidates,
		"eg_multi_anchor", st.EgMultiAnchor,
		"eg_missing_mirror", st.EgMissingMirror,
		"eg_no_median", st.EgNoMedian,
		"eg_planned", st.EgPlanned,
		"eg_written", st.EgWritten,
		"eg_unchanged", st.EgUnchanged,
		"dl_candidates", st.DlCandidates,
		"dl_missing_mirror", st.DlMissingMirror,
		"dl_no_rating", st.DlNoRating,
		"dl_rating_planned", st.DlRatingPlanned,
		"dl_rating_written", st.DlRatingWritten,
		"dl_rating_unchanged", st.DlRatingUnchanged,
		"pop_planned", st.PopPlanned,
		"pop_written", st.PopWritten,
		"pop_unchanged", st.PopUnchanged,
		"errors", st.Errors,
	)
	if !*apply {
		slog.Info("DRY RUN — nothing written; re-run with --apply")
	}
	if st.Errors > 0 {
		os.Exit(1)
	}
}
