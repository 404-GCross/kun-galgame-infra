// Command import-work-intro backfills catalog_work_intro rows for BODYLESS
// works from an upstream description source (step 52 media-aggregation pilot).
// This wave's only source is VNDB vn.description (staged by cmd/ingest-vndb):
// a bodyless galgame work holding an EXACT VNDB work anchor gets its English
// blurb copied into a native intro row attributed to the vndb source. CLAIMED
// works are never touched — their intro is bridged at read time (bridge-not-
// copy). Logic + discipline live in internal/platform/catalog/introimport.
//
// Dry-run is the DEFAULT (repo convention for backfills); pass --apply to write.
// Idempotent: a second applied run inserts nothing (ON CONFLICT DO NOTHING).
//
//	go run ./cmd/import-work-intro                 # dry run, full statistics
//	go run ./cmd/import-work-intro --apply         # write missing intros
//	go run ./cmd/import-work-intro --limit 100 --apply
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
	"api/internal/platform/catalog/introimport"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run, statistics only)")
	limit := flag.Int("limit", 0, "max candidate works to consider (0 = all)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env") // allow running from the repo root

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	db, err := database.NewPostgresDB(cfg.CatalogDatabase)
	if err != nil {
		slog.Error("catalog db connect", "error", err, "dbname", cfg.CatalogDatabase.DBName)
		os.Exit(1)
	}
	defer db.Close()

	st, err := introimport.Run(context.Background(), db.DB(), introimport.Options{DryRun: !*apply, Limit: *limit})
	if err != nil {
		slog.Error("backfill failed", "error", err)
		os.Exit(1)
	}
	slog.Info("work-intro backfill",
		"apply", *apply,
		"total_bodyless", st.TotalBodyless,
		"with_vndb_anchor", st.WithVNDBAnchor,
		"skipped_no_anchor", st.SkippedNoAnchor,
		"skipped_empty_desc", st.SkippedEmptyDesc,
		"already", st.Already,
		"intros_written", st.IntrosWritten,
		"works_covered", st.WorksCovered,
	)
}
