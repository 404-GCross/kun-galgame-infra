// enrich-eg-scores fills the galgame_eg_meta narrow table from the EG
// (ErogameScape) mirror, resolved through the catalog EG exact work anchor: for
// every EG exact work-ref (source=n, entity_type=work, link_kind=exact) whose
// catalog_work is a claimed galgame_wiki row, take the EG median + votecount and
// upsert one row keyed on the wiki galgame id. The join is the ONLY legal path
// (exact anchor, no name-side matching); it never touches release-level DLsite
// anchors. A target galgame absent from the local wiki is skipped (FK guard +
// snapshot-drift resilience), counted as skipped_no_galgame (0 in production).
//
// Dry-run is the DEFAULT (enrich family convention); pass --apply to write. No
// reindex afterwards — this narrow table is source-owned and unindexed.
//
//	go run ./cmd/enrich-eg-scores --eg-dsn '...'            # dry run, statistics
//	go run ./cmd/enrich-eg-scores --eg-dsn '...' --apply    # write (×2 = idempotent)
//
// The EG staging DSN enters via --eg-dsn (default: erogamespace db on the
// catalog server, mirroring cmd/import-eg-dlsite-releases); --catalog-dsn
// overrides the catalog DB; the wiki write side is cfg.GalgameDatabase.
package main

import (
	"flag"
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// openStaging opens a read-side staging DB: --dsn override wins, else the
// catalog server's config with the db name swapped (mirrors
// cmd/import-eg-dlsite-releases). An empty dbName keeps base.DBName.
func openStaging(base config.DatabaseConfig, override, dbName string) (*gorm.DB, error) {
	dsn := override
	if dsn == "" {
		cfg := base
		if dbName != "" {
			cfg.DBName = dbName
		}
		dsn = cfg.DSN()
	}
	return gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
}

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run, statistics only)")
	limit := flag.Int("limit", 0, "max target galgames to process (0 = all)")
	catalogDSN := flag.String("catalog-dsn", "", "catalog DSN override (default: cfg.CatalogDatabase)")
	egDSN := flag.String("eg-dsn", "", "erogamespace staging DSN (default: erogamespace db on the catalog server)")
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

	catalogDB, err := openStaging(cfg.CatalogDatabase, *catalogDSN, "")
	if err != nil {
		slog.Error("catalog db connect", "error", err)
		os.Exit(1)
	}
	if s, err := catalogDB.DB(); err == nil {
		defer s.Close()
	}

	egDB, err := openStaging(cfg.CatalogDatabase, *egDSN, "erogamespace")
	if err != nil {
		slog.Error("erogamespace db connect", "error", err)
		os.Exit(1)
	}
	if s, err := egDB.DB(); err == nil {
		defer s.Close()
	}

	stats, err := Run(wikiDB.DB(), catalogDB, egDB, Options{Apply: *apply, Limit: *limit})
	if err != nil {
		slog.Error("enrich failed", "error", err)
		os.Exit(1)
	}

	slog.Info("eg score enrichment summary",
		"anchors", stats.Anchors,
		"multi_anchor", stats.MultiAnchor,
		"matched", stats.Matched,
		"written", stats.Written,
		"missing_in_mirror", stats.MissingInMirror,
		"skipped_no_galgame", stats.SkippedNoGalgame,
	)
	if !*apply {
		slog.Info("DRY RUN — nothing written; re-run with --apply")
	}
}
