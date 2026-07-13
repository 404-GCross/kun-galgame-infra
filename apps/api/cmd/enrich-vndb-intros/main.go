// enrich-vndb-intros backfills the ENGLISH intro of vndb-anchored galgames from
// VNDB's own description (kana /vn `description`, BBCode), FILLING ONLY WHEN
// intro_en_us is empty — any existing English intro, from any source, is never
// touched. Every description is normalized through intronorm (VNDB BBCode → wiki
// Markdown) before it lands; logic lives in internal/platform/galgame/vndbintros.
//
// Dry-run is the DEFAULT (repo convention for backfills); pass --apply to write.
// intro_en_us is a Meili-indexed field, so after an applied run rebuild the
// index:
//
//	go run ./cmd/enrich-vndb-intros                 # dry run, full statistics
//	go run ./cmd/enrich-vndb-intros --limit 200 --apply
//	go run ./cmd/enrich-vndb-intros --apply         # write all
//	go run ./cmd/reindex-search --index=galgames    # surface new intros in search
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"

	"api/internal/infrastructure/database"
	"api/internal/platform/galgame/vndb"
	"api/internal/platform/galgame/vndbintros"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run, statistics only)")
	limit := flag.Int("limit", 0, "max candidate galgames to process (0 = all)")
	gap := flag.Duration("gap", 2*time.Second, "min delay between VNDB API calls")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env") // allow running from the repo root

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	wikiDB, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		slog.Error("wiki db connect", "error", err, "dbname", cfg.GalgameDatabase.DBName)
		os.Exit(1)
	}
	defer wikiDB.Close()

	vc := vndb.New(*gap)
	stats, err := vndbintros.Run(context.Background(), wikiDB.DB(), vc.FetchVNDescriptionsBatch, vndbintros.Options{
		DryRun: !*apply,
		Limit:  *limit,
	})
	if err != nil {
		slog.Error("enrich-vndb-intros failed", "error", err)
		os.Exit(1)
	}

	slog.Info("vndb intro enrichment summary",
		"candidates", stats.Candidates,
		"fetched", stats.Fetched,
		"filled", stats.Filled,
		"no_description", stats.NoDescription,
		"skipped_empty_after_norm", stats.SkippedEmptyAfterNorm,
		"api_missing", stats.APIMissing,
		"failed", stats.Failed,
	)
	if !*apply {
		slog.Info("DRY RUN — nothing written; re-run with --apply")
	} else {
		slog.Info("applied — run `go run ./cmd/reindex-search --index=galgames` to refresh search")
	}
}
