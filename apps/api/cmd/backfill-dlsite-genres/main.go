// backfill-dlsite-genres fills catalog_work_tag rows for BODYLESS galgame
// works from the DLsite OFFICIAL genre taxonomy (refs/proj/68, the tags
// facet's dlsite sibling of 58b): DLsite EXACT RELEASE anchors on bodyless
// GALGAME-medium works (ASMR out by ruling) × the DLsite mirror's
// works.product_json.genres[] (--dlsite-dsn), names localized via wave 67's
// genre_taxonomy table (same mirror DB, locale=zh_CN, resolved by genre id —
// the CURRENT official name, auto-correcting works that embed a since-renamed
// genre). Retired ids (no taxonomy row) fall back to the embedded ja name.
// Claimed works bridge the galgame family's tag layer instead (DLsite genres
// NEVER touch galgame_tag — the vocabulary red line).
//
// Logic lives in internal/jobs/dlsitegenres. Dry-run is the DEFAULT (repo
// convention); pass --apply to write. Both DSNs are REQUIRED and never
// defaulted — the rehearsal copy locally (kun_catalog_rehearsal + the local
// dlsite mirror), the live catalog only in the acceptance run. NOTE: prod's
// dlsite DB must carry genre_taxonomy first (COPY the local 1,626 rows or
// rerun wave 67's fetch-genre-taxonomy there). Idempotent FILL semantics:
// ON CONFLICT (work_id, name, source_id) DO NOTHING — a second --apply writes
// zero; a mirror refresh only adds newly-appeared genres (deliberately NOT
// step 62's refresh upsert — genre sets are quasi-static). A claimed work is
// refused at write time (XOR guard §8.D).
//
//	# dry-run: counters (zh-hit / retired-fallback buckets) + samples
//	go run ./cmd/backfill-dlsite-genres \
//	    --dsn "host=127.0.0.1 port=5432 user=postgres password=... dbname=kun_catalog_rehearsal sslmode=disable" \
//	    --dlsite-dsn "host=127.0.0.1 port=5432 user=postgres password=... dbname=dlsite sslmode=disable"
//
//	# apply: write the tag rows
//	go run ./cmd/backfill-dlsite-genres --apply --dsn "..." --dlsite-dsn "..."
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/jobs/dlsitegenres"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run, counters + samples only)")
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED; the rehearsal copy locally, the live catalog only in the acceptance run")
	dlsiteDSN := flag.String("dlsite-dsn", "", "DLsite mirror DSN (the dlsite database, also hosts genre_taxonomy) — REQUIRED")
	limit := flag.Int("limit", 0, "max candidate works (0 = all)")
	offset := flag.Int("offset", 0, "skip this many candidate works (for chunking)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env") // allow running from the repo root

	// config drives only logging here; the DBs are reached exclusively via flags.
	if cfg, err := config.Load(); err == nil {
		logger.Init(cfg.Server.Env)
	}

	st, err := dlsitegenres.Run(context.Background(), dlsitegenres.Opts{
		Apply:     *apply,
		DSN:       *dsn,
		DlsiteDSN: *dlsiteDSN,
		Limit:     *limit,
		Offset:    *offset,
	})
	if err != nil {
		slog.Error("backfill-dlsite-genres", "error", err)
		os.Exit(1)
	}
	hitRate := 0.0
	if st.ZhHit+st.JaFallback > 0 {
		hitRate = float64(st.ZhHit) / float64(st.ZhHit+st.JaFallback)
	}
	slog.Info("backfill-dlsite-genres summary",
		"apply", *apply,
		"taxonomy_rows", st.TaxonomyRows,
		"candidates", st.Candidates,
		"missing_mirror", st.MissingMirror,
		"no_genres", st.NoGenres,
		"not_array", st.NotArray,
		"zh_hit", st.ZhHit,
		"ja_fallback", st.JaFallback,
		"taxonomy_hit_rate", hitRate,
		"name_blank", st.NameBlank,
		"dup_collapsed", st.DupCollapsed,
		"planned", st.Planned,
		"distinct_names", st.DistinctNames,
		"written", st.Written,
		"conflict", st.Conflict,
		"errors", st.Errors,
	)
	if !*apply {
		slog.Info("DRY RUN — nothing written; re-run with --apply")
	}
}
