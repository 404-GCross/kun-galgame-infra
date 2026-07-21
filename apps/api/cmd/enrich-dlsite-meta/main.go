// enrich-dlsite-meta fills the galgame_dlsite_meta narrow table from the DLsite
// mirror, resolved through the catalog DLsite exact RELEASE anchor (workno
// anchors are SKU-natured, doc 17 R3): for every DLsite exact release-ref whose
// catalog_work is a claimed galgame_wiki GALGAME-medium row, take the mirror's
// info_json rating + popularity counters and upsert one row keyed on the wiki
// galgame id. The join is the ONLY legal path (exact anchor, no name-side
// matching). Game-domain only by construction (medium filter — the ASMR family
// carries DLsite anchors too and is out of scope by ruling). A target galgame
// absent from the local wiki is skipped (FK guard + snapshot-drift resilience).
//
// The values are VOLATILE (sales/wishlist counters move daily), so the upsert
// is CHANGE-DETECTED: a re-run after a mirror refresh updates rows in place and
// a re-run against an unchanged mirror writes zero (rows count as unchanged).
// That makes this tool the claimed half of the refresh loop: kun-dlsite-api
// `refresh` updates the mirror → re-run this tool → values follow.
//
// Dry-run is the DEFAULT (enrich family convention); pass --apply to write.
// No reindex afterwards — this narrow table is source-owned and unindexed.
//
//	go run ./cmd/enrich-dlsite-meta --dlsite-dsn '...'            # dry run, statistics
//	go run ./cmd/enrich-dlsite-meta --dlsite-dsn '...' --apply    # write (×2 = no-op)
//
// --dlsite-dsn is REQUIRED and never defaulted (the 55/57 explicit-DSN
// discipline the 58a backfill adopted); --catalog-dsn / --wiki-dsn override the
// catalog read side and the wiki write side (default: cfg.CatalogDatabase /
// cfg.GalgameDatabase — point BOTH at kun_catalog_rehearsal for a drill).
package main

import (
	"flag"
	"log/slog"
	"os"

	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// openDB opens one side; override wins over the config default.
func openDB(override, fallback string) (*gorm.DB, error) {
	dsn := override
	if dsn == "" {
		dsn = fallback
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		return nil, err
	}
	return db, nil
}

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run, statistics only)")
	limit := flag.Int("limit", 0, "max target galgames to process (0 = all)")
	catalogDSN := flag.String("catalog-dsn", "", "catalog DSN override (default: cfg.CatalogDatabase)")
	wikiDSN := flag.String("wiki-dsn", "", "wiki write-side DSN override (default: cfg.GalgameDatabase)")
	dlsiteDSN := flag.String("dlsite-dsn", "", "DLsite mirror DSN (the dlsite staging database) — REQUIRED")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env") // allow running from the repo root

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	if *dlsiteDSN == "" {
		slog.Error("--dlsite-dsn is required (DLsite staging DB — reads works.info_json); refusing to guess")
		os.Exit(1)
	}

	wikiDB, err := openDB(*wikiDSN, cfg.GalgameDatabase.DSN())
	if err != nil {
		slog.Error("wiki db connect", "error", err)
		os.Exit(1)
	}
	catalogDB, err := openDB(*catalogDSN, cfg.CatalogDatabase.DSN())
	if err != nil {
		slog.Error("catalog db connect", "error", err)
		os.Exit(1)
	}
	dlsiteDB, err := openDB(*dlsiteDSN, "")
	if err != nil {
		slog.Error("dlsite mirror db connect", "error", err)
		os.Exit(1)
	}
	for _, db := range []*gorm.DB{wikiDB, catalogDB, dlsiteDB} {
		if s, e := db.DB(); e == nil {
			defer s.Close()
		}
	}

	stats, err := Run(wikiDB, catalogDB, dlsiteDB, Options{Apply: *apply, Limit: *limit})
	if err != nil {
		slog.Error("enrich failed", "error", err)
		os.Exit(1)
	}

	slog.Info("dlsite meta enrichment summary",
		"anchors", stats.Anchors,
		"multi_anchor", stats.MultiAnchor,
		"matched", stats.Matched,
		"written", stats.Written,
		"unchanged", stats.Unchanged,
		"missing_in_mirror", stats.MissingInMirror,
		"skipped_no_galgame", stats.SkippedNoGalgame,
	)
	if !*apply {
		slog.Info("DRY RUN — nothing written; re-run with --apply")
	}
}
