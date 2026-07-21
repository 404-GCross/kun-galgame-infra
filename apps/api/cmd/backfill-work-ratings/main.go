// backfill-work-ratings fills catalog_work_rating rows for BODYLESS galgame
// works from the two scored sources of the ratings facet (refs/proj/58a):
//
//   - bangumi: step-56a EXACT anchors (rule:bgm-title-year) ×
//     src_bangumi.subject score>0 → score (native 0-10) / rank (NULL when
//     Bangumi says 0 = unranked) / vote_count (summed score_details buckets —
//     the dump has no total field). src_bangumi is a schema INSIDE the catalog
//     DB, so ONE --dsn covers the whole lane.
//   - erogamespace: EG EXACT work anchors on bodyless works (the
//     cmd/enrich-eg-scores anchor query inverted from claimed to bodyless) ×
//     the EG mirror's games (--eg-dsn) → score = median (native 0-100) /
//     vote_count = count2 / rank NULL.
//
// Logic lives in internal/jobs/workratings. Dry-run is the DEFAULT (repo
// convention); pass --apply to write. Both DSNs are REQUIRED and never
// defaulted — the rehearsal copy locally (kun_catalog_rehearsal + the local
// erogamespace mirror), the live catalog only in the acceptance run.
// Idempotent: ON CONFLICT (work_id, source_id) DO NOTHING — a second --apply
// writes zero. A claimed work is refused at write time (XOR guard §8.D).
//
//	# dry-run: per-lane counters + samples
//	go run ./cmd/backfill-work-ratings \
//	    --dsn "host=127.0.0.1 port=5432 user=postgres password=... dbname=kun_catalog_rehearsal sslmode=disable" \
//	    --eg-dsn "host=127.0.0.1 port=5432 user=postgres password=... dbname=erogamespace sslmode=disable"
//
//	# apply: write the rating rows (bangumi + erogamespace lanes)
//	go run ./cmd/backfill-work-ratings --apply --dsn "..." --eg-dsn "..."
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
	egDSN := flag.String("eg-dsn", "", "EG mirror DSN (the erogamespace database) — REQUIRED")
	limit := flag.Int("limit", 0, "max candidate works per lane (0 = all)")
	offset := flag.Int("offset", 0, "skip this many candidate works per lane (for chunking)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env") // allow running from the repo root

	// config drives only logging here; the DBs are reached exclusively via flags.
	if cfg, err := config.Load(); err == nil {
		logger.Init(cfg.Server.Env)
	}

	st, err := workratings.Run(context.Background(), workratings.Opts{
		Apply:  *apply,
		DSN:    *dsn,
		EGDSN:  *egDSN,
		Limit:  *limit,
		Offset: *offset,
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
		"bgm_conflict", st.BgmConflict,
		"eg_candidates", st.EgCandidates,
		"eg_multi_anchor", st.EgMultiAnchor,
		"eg_missing_mirror", st.EgMissingMirror,
		"eg_no_median", st.EgNoMedian,
		"eg_planned", st.EgPlanned,
		"eg_written", st.EgWritten,
		"eg_conflict", st.EgConflict,
		"refused_claimed", st.Refused,
		"errors", st.Errors,
	)
	if !*apply {
		slog.Info("DRY RUN — nothing written; re-run with --apply")
	}
}
