// build-tag-canonical builds the DETERMINISTIC slice of the tag canonical layer
// (refs/proj/74, design refs/proj/70): it extracts the three per-source tag
// vocabularies (vndb/bangumi/dlsite — all read from catalog_work_tag grouped
// by source since the c170b6ab repoint), mechanically prefilters bangumi junk
// (date/disc/number regexes + maker/label collisions — reported, never written),
// folds the survivors on NFKC+casefold+trim, and mints ONE catalog_tag +
// per-source catalog_tag_source_map rows for every norm spanning ≥2 distinct
// sources (tier=core; hand-pinned meta = kind=meta). Single-source names get NO
// row this wave. The ORIGINAL layers are read-only — not one row is touched.
//
// Logic lives in internal/jobs/tagcanon. Dry-run is the DEFAULT (repo
// convention); pass --apply to write. The DSN is REQUIRED and never defaulted —
// all three vocabularies live in catalog_work_tag of ONE database (prod:
// kun_catalog). Idempotent: ON CONFLICT DO NOTHING on both tables — a second
// --apply writes zero.
//
//	# dry-run: vocab sizes, junk digest, group count, per-source absorption,
//	#          single-source usage distribution
//	go run ./cmd/build-tag-canonical \
//	    --dsn "host=localhost port=5432 user=postgres password=... dbname=kun_catalog_rehearsal sslmode=disable"
//
//	# small-sample apply (first 5 groups), then full apply
//	go run ./cmd/build-tag-canonical --apply --limit 5 --dsn "..."
//	go run ./cmd/build-tag-canonical --apply --dsn "..."
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/jobs/tagcanon"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run, digest only)")
	dsn := flag.String("dsn", "", "catalog DSN (holds all three tag vocabularies) — REQUIRED; the rehearsal copy locally, the live catalog only in the acceptance run")
	limit := flag.Int("limit", 0, "max groups to write (0 = all; >0 = small-sample apply, Norm-sorted)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env") // allow running from the repo root
	if cfg, err := config.Load(); err == nil {
		logger.Init(cfg.Server.Env)
	}

	st, err := tagcanon.Run(context.Background(), tagcanon.Opts{Apply: *apply, DSN: *dsn, Limit: *limit})
	if err != nil {
		slog.Error("build-tag-canonical", "error", err)
		os.Exit(1)
	}
	slog.Info("build-tag-canonical summary",
		"apply", *apply, "limit", *limit,
		"vndb_names", st.VndbNames, "bangumi_names", st.BangumiNames, "dlsite_names", st.DlsiteNames,
		"bangumi_junk", st.BangumiJunk, "groups", st.Groups, "meta_groups", st.MetaGroups,
		"tri_source", st.TriSource, "planned_maps", st.PlannedMaps,
		"tags_created", st.TagsCreated, "tags_conflict", st.TagsConflict,
		"maps_created", st.MapsCreated, "maps_conflict", st.MapsConflict, "errors", st.Errors)
	if !*apply {
		slog.Info("DRY RUN — nothing written; re-run with --apply")
	}
}
