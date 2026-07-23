// backfill-bgm-work-meta fills two Bangumi-side facets for galgame works in one
// pass (refs/proj/71, ledger T3; T2 = refs/proj/70 §3/§8, 88):
//
//   - meta_tags → catalog_work_tag rows with Count=0 (Bangumi's MODERATED
//     official tags; count=0 distinguishes them from the voted folksonomy
//     rows). Fill semantics: ON CONFLICT DO NOTHING — a same-name folksonomy
//     row keeps its votes.
//   - favorite shelves ({wish, done, doing, on_hold, dropped} — "done" is the
//     dump's key for Bangumi's collect shelf) → catalog_work_popularity rows
//     under the PopularityMetricBgm* vocabulary (10-14). Upsert semantics
//     (change-detected DO UPDATE): favorites are volatile, a dump-refresh
//     re-run heals values.
//
// Candidates: galgame works (claimed OR bodyless — T2) with an EXACT Bangumi
// work anchor (matched_by unrestricted — the 66/69 ruling). The claim policy is
// PER FIELD: meta_tags (Field A) admits claimed works — a catalog-native source
// lane merged with the wiki bridge at the read face; favorite shelves (Field B)
// still refuse claimed works (popularity's XOR is a future T2b domain).
// src_bangumi is a schema INSIDE the catalog DB, so ONE --dsn covers the whole
// run.
//
// Logic lives in internal/jobs/bgmworkmeta. Dry-run is the DEFAULT (repo
// convention); pass --apply to write. The DSN is REQUIRED and never defaulted
// — the rehearsal copy locally, the live catalog only in the acceptance run.
// Idempotent: a second --apply writes zero (meta tags all-conflict, favorite
// shelves all-unchanged). Field B refuses a claimed favorite at write time (the
// retained per-field XOR guard §8.D).
//
//	# dry-run: per-field counters + samples
//	go run ./cmd/backfill-bgm-work-meta \
//	    --dsn "host=127.0.0.1 port=5432 user=postgres password=... dbname=kun_catalog_rehearsal sslmode=disable"
//
//	# apply: write both fields
//	go run ./cmd/backfill-bgm-work-meta --apply --dsn "..."
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

	_ = godotenv.Load("apps/api/.env") // allow running from the repo root

	// config drives only logging here; the DB is reached exclusively via --dsn.
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
		"refused_claimed", st.Refused,
		"errors", st.Errors,
	)
	if !*apply {
		slog.Info("DRY RUN — nothing written; re-run with --apply")
	}
}
