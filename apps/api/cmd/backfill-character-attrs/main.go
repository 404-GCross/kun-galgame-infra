// backfill-character-attrs projects the typical-set character attributes
// (birthday month/day, blood type, height/weight, BWH, cup, gender) plus the
// Bangumi long-tail extra onto catalog_character, from the in-DB staging
// schemas (field PR C2, refs/proj/81). Two lanes: vndb (src_vndb.chars typed
// columns) and bgm (src_bangumi.character.infobox_parsed). src_vndb / src_bangumi
// are schemas INSIDE the catalog DB, so ONE --dsn covers both sides (no API, no
// bytes). VNDB runs first (typed columns win survivorship); Bangumi fills gaps.
//
// Overwrite discipline (refs/proj/81): an empty column is written; a non-empty
// column is rewritten only when its latest field_provenance writer is a pipeline
// source of equal-or-lower priority (idempotent re-parse, or vndb overriding
// bgm) — a human edit is never touched. The bgm extra namespace is replaced
// wholesale each run. Idempotent: a second --apply writes zero.
//
// Dry-run is the DEFAULT (repo convention); pass --apply to write. --dsn is
// REQUIRED and never defaulted — the rehearsal copy locally
// (kun_catalog_rehearsal), the live catalog only in the acceptance run.
//
//	# dry-run: per-lane counters + samples
//	go run ./cmd/backfill-character-attrs \
//	    --dsn "host=127.0.0.1 port=5432 user=postgres password=... dbname=kun_catalog_rehearsal sslmode=disable"
//
//	# apply one lane
//	go run ./cmd/backfill-character-attrs --apply --only vndb \
//	    --dsn "host=127.0.0.1 port=5432 user=postgres password=... dbname=kun_catalog_rehearsal sslmode=disable"
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/jobs/charattrs"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run, counters + samples only)")
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED; the rehearsal copy locally (kun_catalog_rehearsal), the live catalog only in the acceptance run")
	limit := flag.Int("limit", 0, "max candidate characters per lane (0 = all)")
	offset := flag.Int("offset", 0, "skip this many candidate characters per lane (for chunking)")
	only := flag.String("only", "", "restrict to one lane: vndb | bgm (default: both)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env") // allow running from the repo root

	if cfg, err := config.Load(); err == nil {
		logger.Init(cfg.Server.Env)
	}

	st, err := charattrs.Run(context.Background(), charattrs.Opts{
		Apply:  *apply,
		DSN:    *dsn,
		Limit:  *limit,
		Offset: *offset,
		Only:   *only,
	})
	if err != nil {
		slog.Error("backfill-character-attrs", "error", err)
		os.Exit(1)
	}
	slog.Info("backfill-character-attrs done", "apply", *apply,
		"vndb_candidates", st.VNDB.Candidates, "vndb_rows_updated", st.VNDB.RowsUpdated,
		"vndb_gender", st.VNDB.GenderWritten,
		"bgm_candidates", st.Bangumi.Candidates, "bgm_rows_updated", st.Bangumi.RowsUpdated,
		"bgm_gender", st.Bangumi.GenderWritten, "bgm_extra_rows", st.Bangumi.ExtraRows,
		"out_of_range", st.VNDB.OutOfRange+st.Bangumi.OutOfRange,
		"errors", st.VNDB.Errors+st.Bangumi.Errors,
	)
}
