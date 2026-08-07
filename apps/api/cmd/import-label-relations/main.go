// import-label-relations builds the label↔label corporate-structure graph
// (wave 186) from VNDB's producers_relations dump into catalog_label_relation:
// parent/subsidiary, imprint, spin-off and succession edges, stored MIRRORED
// exactly as the upstream publishes them so no read face ever inverts one.
//
// It is a FULL REBUILD of the vndb lane in one transaction — delete this
// source's rows, insert the whole graph — so it is idempotent and needs no
// reaper: run it as often as ingest-vndb refreshes the dump or
// reconcile-org-labels grows the label anchors. Only edges whose BOTH endpoints
// carry an exact vndb label anchor are written; the rest are counted as
// skipped_unanchored and come in on a later pass.
//
// ⚠️ The direction reading of an upstream row is PROVISIONAL and is pinned
// empirically at ops time (the Key / VisualArt's pair). The single flip point is
// internal/jobs/labelrelations/relationmap.go — read its banner first.
//
// Dry-run is the DEFAULT (repo convention); pass --apply to write. --dsn is
// required, falling back to CATALOG_DSN in the environment.
//
//	# dry run: the counters only, nothing written
//	go run ./cmd/import-label-relations --dsn "$DSN"
//
//	# the real build (×2 = byte-identical, it is a rebuild)
//	go run ./cmd/import-label-relations --dsn "$DSN" --apply
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/jobs/labelrelations"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run, counters only)")
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED (also hosts src_vndb); falls back to $CATALOG_DSN")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env") // allow running from the repo root

	// config drives logging only; the catalog DB is reached exclusively via --dsn.
	if cfg, err := config.Load(); err == nil {
		logger.Init(cfg.Server.Env)
	}
	if *dsn == "" {
		*dsn = os.Getenv("CATALOG_DSN")
	}

	st, err := labelrelations.Run(context.Background(), labelrelations.Opts{Apply: *apply, DSN: *dsn})
	if err != nil {
		slog.Error("import-label-relations", "error", err)
		os.Exit(1)
	}
	slog.Info("import-label-relations done", "apply", *apply,
		"edges_total", st.EdgesTotal, "both_anchored", st.BothAnchored,
		"written", st.Written, "skipped_unanchored", st.SkippedUnanchored,
		"skipped_unknown_relation", st.SkippedUnknownRelation,
		"skipped_self", st.SkippedSelf, "deleted", st.Deleted)
	if st.SkippedUnknownRelation > 0 {
		slog.Warn("unmapped relation codes seen — VNDB extended its vocabulary; update relationmap.go",
			"rows", st.SkippedUnknownRelation)
	}
	if !*apply {
		slog.Info("DRY RUN — nothing written; re-run with --apply")
	}
}
