// backfill-work-ratings fills catalog_work_rating (and the DLsite lane's
// catalog_work_popularity) rows for BODYLESS galgame works from the scored
// sources of the ratings facet (refs/proj/58a + 62):
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
//   - dlsite (step 62): DLsite EXACT RELEASE anchors on bodyless
//     GALGAME-medium works (ASMR out by ruling) × the DLsite mirror's
//     works.info_json (--dlsite-dsn) → rating row: score = rate_average_2dp
//     (native 0-5 star average) / vote_count = rate_count / rank NULL, PLUS
//     popularity rows: dl_count→downloads, wishlist_count→wishlist,
//     review_count→reviews (absent counters never become rows).
//
// Logic lives in internal/jobs/workratings. Dry-run is the DEFAULT (repo
// convention); pass --apply to write. All DSNs are REQUIRED and never
// defaulted — the rehearsal copy locally (kun_catalog_rehearsal + the local
// erogamespace/dlsite mirrors), the live catalog only in the acceptance run.
// Refresh-runnable (step 62 upsert unification): every write is ON CONFLICT
// DO UPDATE with change detection — a re-run after a mirror refresh updates
// rows in place; a re-run against unchanged staging writes zero (rows count as
// unchanged). A claimed work is refused at write time (XOR guard §8.D).
//
//	# dry-run: per-lane counters + samples
//	go run ./cmd/backfill-work-ratings \
//	    --dsn "host=127.0.0.1 port=5432 user=postgres password=... dbname=kun_catalog_rehearsal sslmode=disable" \
//	    --eg-dsn "host=127.0.0.1 port=5432 user=postgres password=... dbname=erogamespace sslmode=disable" \
//	    --dlsite-dsn "host=127.0.0.1 port=5432 user=postgres password=... dbname=dlsite sslmode=disable"
//
//	# apply: write the rating + popularity rows (all three lanes)
//	go run ./cmd/backfill-work-ratings --apply --dsn "..." --eg-dsn "..." --dlsite-dsn "..."
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
	dlsiteDSN := flag.String("dlsite-dsn", "", "DLsite mirror DSN (the dlsite database) — REQUIRED")
	limit := flag.Int("limit", 0, "max candidate works per lane (0 = all)")
	offset := flag.Int("offset", 0, "skip this many candidate works per lane (for chunking)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env") // allow running from the repo root

	// config drives only logging here; the DBs are reached exclusively via flags.
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
		"refused_claimed", st.Refused,
		"errors", st.Errors,
	)
	if !*apply {
		slog.Info("DRY RUN — nothing written; re-run with --apply")
	}
}
