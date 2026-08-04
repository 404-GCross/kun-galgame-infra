// backfill-release-meta fills two release-metadata scalars in one field PR
// (refs/proj/66, FILL-EMPTY-ONLY — a non-zero value is never overwritten and
// the survivorship engine stays unused):
//
//   - release dates: empty catalog_release.released_y/m/d from each release's
//     OWN anchor source — DLsite regist_date (SKU-level, wins over the
//     work-level sources), then EG sellday for bodyless 1:1 stubs, then the
//     Bangumi subject date (free text, partial dates legal).
//   - age rating: catalog_work.content_rating=0 (unrated) by source priority
//     ① DLsite age_category ('3'→r18 '2'→sensitive '1'→all_ages) ② claimed
//     wiki age_limit ('r18'→r18 'all'→all_ages) ③ Bangumi nsfw=true→r18.
//     First verdict wins (ownership, not strictest); an explicit all-ages
//     verdict keeps the row 0 and suppresses lower sources.
//
// Logic lives in internal/jobs/releasemeta. Dry-run is the DEFAULT (repo
// convention); pass --apply to write. All DSNs are REQUIRED and never
// defaulted — the rehearsal copy locally (kun_catalog_rehearsal + local
// dlsite/erogamespace/kun_galgame_wiki), the live catalog only in the
// acceptance run. Fill-empty is naturally idempotent: a second pass finds
// nothing left to write. No upsert/refresh loop — dates and ratings are
// non-volatile (contrast: step-62's change-detected upserts); a mirror
// refresh just reruns the same top-up.
//
//	# dry-run: per-lane counters + samples
//	go run ./cmd/backfill-release-meta \
//	    --dsn "host=127.0.0.1 port=5432 user=postgres password=... dbname=kun_catalog_rehearsal sslmode=disable" \
//	    --dlsite-dsn "host=127.0.0.1 port=5432 user=postgres password=... dbname=dlsite sslmode=disable" \
//	    --eg-dsn "host=127.0.0.1 port=5432 user=postgres password=... dbname=erogamespace sslmode=disable" \
//
//	# apply: fill the empty scalars (all lanes)
//	go run ./cmd/backfill-release-meta --apply --dsn "..." --dlsite-dsn "..." --eg-dsn "..."
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
	egDSN := flag.String("eg-dsn", "", "EG mirror DSN (the erogamespace database) — REQUIRED")
	limit := flag.Int("limit", 0, "max candidates per lane (0 = all)")
	offset := flag.Int("offset", 0, "skip this many candidates per lane (for chunking)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env") // allow running from the repo root

	// config drives only logging here; the DBs are reached exclusively via flags.
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
}
