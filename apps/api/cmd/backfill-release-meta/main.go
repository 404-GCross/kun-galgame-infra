package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/jobs/releasemeta"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run, counters + samples only)")
	dsn := flag.String("dsn", "", "catalog DSN (also hosts src_bangumi) — REQUIRED; the rehearsal copy locally, the live catalog only in the acceptance run")
	dlsiteDSN := flag.String("dlsite-dsn", "", "DLsite mirror DSN (the dlsite database) — REQUIRED")
	egDSN := flag.String("eg-dsn", "", "EG mirror DSN (the erogamescape database) — REQUIRED")
	limit := flag.Int("limit", 0, "max candidates per lane (0 = all)")
	offset := flag.Int("offset", 0, "skip this many candidates per lane (for chunking)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env")

	if cfg, err := config.Load(); err == nil {
		logger.Init(cfg.Server.Env)
	}

	st, err := releasemeta.Run(context.Background(), releasemeta.Opts{
		Apply:     *apply,
		DSN:       *dsn,
		DlsiteDSN: *dlsiteDSN,
		EGDSN:     *egDSN,
		Limit:     *limit,
		Offset:    *offset,
	})
	if err != nil {
		slog.Error("backfill-release-meta", "error", err)
		os.Exit(1)
	}
	slog.Info("backfill-release-meta summary",
		"apply", *apply,
		"dl_date_candidates", st.DlDateCandidates,
		"dl_date_no_regist", st.DlDateNoRegist,
		"dl_date_planned", st.DlDatePlanned,
		"dl_date_filled", st.DlDateFilled,
		"eg_date_candidates", st.EgDateCandidates,
		"eg_date_covered", st.EgDateCovered,
		"eg_date_planned", st.EgDatePlanned,
		"eg_date_filled", st.EgDateFilled,
		"bgm_date_candidates", st.BgmDateCandidates,
		"bgm_date_covered", st.BgmDateCovered,
		"bgm_date_partial", st.BgmDatePartial,
		"bgm_date_planned", st.BgmDatePlanned,
		"bgm_date_filled", st.BgmDateFilled,
		"rating_candidates", st.RatingCandidates,
		"rating_planned", st.RatingPlanned,
		"rating_filled", st.RatingFilled,
		"rating_all_ages_verdicts", st.RatingDlAllAges,
		"rating_no_verdict", st.RatingNoVerdict,
		"errors", st.Errors,
	)
	if !*apply {
		slog.Info("DRY RUN — nothing written; re-run with --apply")
	}
	if st.Errors > 0 {
		os.Exit(1)
	}
}
