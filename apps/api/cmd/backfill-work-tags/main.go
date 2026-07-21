// backfill-work-tags fills catalog_work_tag rows for BODYLESS galgame works
// from the Bangumi folksonomy (refs/proj/58b): step-56a EXACT anchors
// (rule:bgm-title-year) × src_bangumi.subject.tags ([{name, count}] jsonb) →
// one row per tag, VERBATIM — no vocabulary mapping, no threshold (count=1
// rows are stored too; consumers filter when rendering). Content tags NEVER
// touch catalog_label (the attribution-vocabulary red line). src_bangumi is a
// schema INSIDE the catalog DB, so ONE --dsn covers the whole run.
//
// Logic lives in internal/jobs/worktags. Dry-run is the DEFAULT (repo
// convention); pass --apply to write. The DSN is REQUIRED and never defaulted
// — the rehearsal copy locally, the live catalog only in the acceptance run.
// Idempotent: ON CONFLICT (work_id, name, source_id) DO NOTHING — a second
// --apply writes zero. A claimed work is refused at write time (XOR guard
// §8.D).
//
//	# dry-run: counters + top-frequency tag names + samples
//	go run ./cmd/backfill-work-tags \
//	    --dsn "host=127.0.0.1 port=5432 user=postgres password=... dbname=kun_catalog_rehearsal sslmode=disable"
//
//	# apply: write the tag rows
//	go run ./cmd/backfill-work-tags --apply --dsn "..."
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/jobs/worktags"
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

	st, err := worktags.Run(context.Background(), worktags.Opts{
		Apply:  *apply,
		DSN:    *dsn,
		Limit:  *limit,
		Offset: *offset,
	})
	if err != nil {
		slog.Error("backfill-work-tags", "error", err)
		os.Exit(1)
	}
	slog.Info("backfill-work-tags summary",
		"apply", *apply,
		"candidates", st.Candidates,
		"no_tags", st.NoTags,
		"not_array", st.NotArray,
		"name_blank", st.NameBlank,
		"dup_collapsed", st.DupCollapsed,
		"planned", st.Planned,
		"distinct_names", st.DistinctNames,
		"written", st.Written,
		"conflict", st.Conflict,
		"refused_claimed", st.Refused,
		"errors", st.Errors,
	)
	if !*apply {
		slog.Info("DRY RUN — nothing written; re-run with --apply")
	}
}
