// reconcile-doujin-bangumi anchors BODYLESS galgame-doujin catalog works to
// Bangumi type=4 (game) subjects by normalized-title match, writing graded
// catalog_external_ref rows (BGM 竖封轨 Phase 1, refs/proj/56a). It ONLY writes
// anchors — no images, no schema changes, no enrichment.
//
// A work that matches exactly one subject, where that subject is matched by
// exactly one work (bidirectional-unique), is anchored: exact
// (rule:bgm-title-year) when a release year corroborates within ±1, else
// probable (rule:bgm-title-only, a confirm-bucket assertion). Works already
// carrying a Bangumi anchor and every ambiguous match are skipped. Logic lives
// in internal/jobs/doujinbangumi.
//
// Dry-run is the DEFAULT (repo convention); pass --apply to write. --dsn is
// REQUIRED and never defaulted — the rehearsal copy locally
// (kun_catalog_rehearsal), the live catalog only in the acceptance run.
// Idempotent: a second --apply writes zero.
//
//	# dry-run: report the exact/probable tier plan + samples, write nothing
//	go run ./cmd/reconcile-doujin-bangumi \
//	    --dsn "host=127.0.0.1 port=5432 user=postgres password=... dbname=kun_catalog_rehearsal sslmode=disable"
//
//	# apply: write exact + probable anchors
//	go run ./cmd/reconcile-doujin-bangumi --apply \
//	    --dsn "host=127.0.0.1 port=5432 user=postgres password=... dbname=kun_catalog_rehearsal sslmode=disable"
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/jobs/doujinbangumi"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run, tier plan + samples only)")
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED; the rehearsal copy locally (kun_catalog_rehearsal), the live catalog only in the acceptance run")
	limit := flag.Int("limit", 0, "max candidate works to process for writing (0 = all); debugging aid")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env") // allow running from the repo root

	// config drives only logging here; the DB is reached exclusively via --dsn.
	if cfg, err := config.Load(); err == nil {
		logger.Init(cfg.Server.Env)
	}

	st, err := doujinbangumi.Run(context.Background(), doujinbangumi.Opts{
		Apply: *apply,
		DSN:   *dsn,
		Limit: *limit,
	})
	if err != nil {
		slog.Error("reconcile-doujin-bangumi", "error", err)
		os.Exit(1)
	}
	slog.Info("reconcile-doujin-bangumi done",
		"apply", *apply,
		"candidate_works", st.CandidateWorks,
		"type4_subjects", st.Type4Subjects,
		"already_anchored", st.AlreadyAnchored,
		"matched", st.Matched,
		"ambiguous_work", st.AmbiguousWork,
		"ambiguous_subject", st.AmbiguousSubject,
		"exact", st.Exact,
		"probable", st.Probable,
		"exact_written", st.ExactWritten,
		"probable_written", st.ProbableWritten,
		"already", st.Already,
	)
}
