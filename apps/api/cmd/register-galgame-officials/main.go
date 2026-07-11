// register-galgame-officials registers every wiki galgame_official (the galgame
// domain's brand/会社 layer) into the catalog Label registry as a first-party
// label, and builds Brand attribution edges (catalog_work_label kind=Brand) for
// every already-claimed galgame. Doc 10 §11 step 1 (C-04).
//
// Register, don't assert: the wiki officials carry no external identity, so each
// unmapped official is minted as a fresh label (with an action=imported
// revision) — never auto-equated to an existing one. Name-norm collisions with
// pre-wave labels (Bangumi/DLsite already minted Key, TYPE-MOON … as company/
// circle labels) are surfaced as pending catalog_match_candidate rows for human
// review; the merge machine converges any true duplicates.
//
// Idempotency key = galgame_official.catalog_label_id IS NULL. Dry-run is the
// DEFAULT (repo convention); pass --apply to write. --catalog-dsn overrides the
// catalog DB — MANDATORY for staging: cfg.CatalogDatabase defaults to the LIVE
// kun_catalog, so always point --catalog-dsn at the rehearsal copy.
//
//	go run ./cmd/register-galgame-officials --catalog-dsn '...'            # dry run
//	go run ./cmd/register-galgame-officials --catalog-dsn '...' --apply    # write (×2 = idempotent)
package main

import (
	"flag"
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
	"api/internal/platform/galgame/catalogsync"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// openCatalog opens the catalog DB: --catalog-dsn override wins, else
// cfg.CatalogDatabase (the LIVE db — always override for staging).
func openCatalog(base config.DatabaseConfig, override string) (*gorm.DB, error) {
	dsn := override
	if dsn == "" {
		dsn = base.DSN()
	}
	return gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
}

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run, statistics only)")
	limit := flag.Int("limit", 0, "max unmapped officials to mint (0 = all); debugging aid")
	catalogDSN := flag.String("catalog-dsn", "", "catalog DSN override (default: cfg.CatalogDatabase — the LIVE db; ALWAYS pass rehearsal for staging)")
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

	catalogDB, err := openCatalog(cfg.CatalogDatabase, *catalogDSN)
	if err != nil {
		slog.Error("catalog db connect", "error", err)
		os.Exit(1)
	}
	if s, err := catalogDB.DB(); err == nil {
		defer s.Close()
	}

	reg := catalogsync.NewOfficialRegistrar(wikiDB.DB(), catalogDB, !*apply, *limit)
	stats, err := reg.Run()
	if err != nil {
		slog.Error("register officials failed", "error", err)
		os.Exit(1)
	}

	slog.Info("official→label registration summary",
		"officials", stats.Officials,
		"minted", stats.Minted,
		"already", stats.Already,
		"aliases_written", stats.AliasesWritten,
		"name_candidates", stats.NameCandidates,
		"edges_written", stats.EdgesWritten,
		"edges_already", stats.EdgesAlready,
		"edges_skipped_unclaimed", stats.EdgesSkippedUnclaimed,
	)
	if !*apply {
		slog.Info("DRY RUN — nothing written; re-run with --apply")
	}
}
