// enrich-bangumi enriches bid-anchored galgames from the Bangumi Silver
// layer (src_bangumi.subject in kun_catalog): fills empty Chinese name/intro,
// appends missing aliases, and refreshes the galgame_bangumi_meta narrow
// table. Conservative by design — see the bangumienrich package doc.
//
// Dry-run is the DEFAULT (repo convention); pass --apply to write. After an
// applied run, rebuild the search index (invariant 21):
//
//	go run ./cmd/enrich-bangumi                 # dry run, full statistics
//	go run ./cmd/enrich-bangumi --apply         # write
//	go run ./cmd/reindex-search --index=galgames
package main

import (
	"flag"
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
	"api/internal/platform/galgame/bangumienrich"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run, statistics only)")
	limit := flag.Int("limit", 0, "max bid-anchored galgames to process (0 = all)")
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
		slog.Error("wiki db connect", "error", err)
		os.Exit(1)
	}
	defer wikiDB.Close()
	catalogDB, err := database.NewPostgresDB(cfg.CatalogDatabase)
	if err != nil {
		slog.Error("catalog db connect", "error", err)
		os.Exit(1)
	}
	defer catalogDB.Close()

	stats, err := bangumienrich.Run(wikiDB.DB(), catalogDB.DB(), bangumienrich.Options{
		DryRun: !*apply,
		Limit:  *limit,
	})
	if err != nil {
		slog.Error("enrich failed", "error", err)
		os.Exit(1)
	}

	slog.Info("bangumi enrichment summary",
		"matched", stats.Matched,
		"wrong_type", stats.WrongType,
		"missing_in_dump", stats.MissingInDump,
		"name_filled", stats.NameFilled,
		"intro_filled", stats.IntroFilled,
		"aliases_appended", stats.AliasesAppended,
		"meta_upserted", stats.MetaUpserted,
		"unchanged", stats.Unchanged,
	)
	if !*apply {
		slog.Info("DRY RUN — nothing written; re-run with --apply")
	} else {
		slog.Info("applied — run `go run ./cmd/reindex-search --index=galgames` to refresh search")
	}
}
