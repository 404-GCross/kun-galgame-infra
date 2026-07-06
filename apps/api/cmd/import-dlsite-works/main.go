// import-dlsite-works registers the DLsite voice/ASMR catalogue into catalog as
// UNCLAIMED works (R2, site=NULL — asmr has no product yet) + 1:1 releases
// carrying the workno SKU anchor (R3/R5), circle labels, and tier-0 orphan
// creator credits. Voice/ASMR only; zero claiming, zero cross-source matching,
// zero galgame-graph interaction.
//
//	go run ./cmd/migrate-catalog                        # land the DLsite role map
//	go run ./cmd/import-dlsite-works                    # dry-run
//	go run ./cmd/import-dlsite-works --apply            # write (×2 = idempotent)
package main

import (
	"flag"
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
	"api/internal/platform/catalog/importer"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func main() {
	apply := flag.Bool("apply", false, "write (default: dry run — plan counts only)")
	limit := flag.Int("limit", 0, "cap works processed (0 = all)")
	dlsiteDSN := flag.String("dlsite-dsn", "", "DLsite staging DSN (default: dlsite db on the catalog server)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env")
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	catalogDB, err := database.NewPostgresDB(cfg.CatalogDatabase)
	if err != nil {
		slog.Error("catalog db connect", "error", err)
		os.Exit(1)
	}
	defer catalogDB.Close()

	dsn := *dlsiteDSN
	if dsn == "" {
		dlCfg := cfg.CatalogDatabase
		dlCfg.DBName = "dlsite"
		dsn = dlCfg.DSN()
	}
	dlsiteDB, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		slog.Error("dlsite db connect", "error", err)
		os.Exit(1)
	}
	if sqlDB, err := dlsiteDB.DB(); err == nil {
		defer sqlDB.Close()
	}

	im := importer.New(catalogDB.DB(), nil, importer.Options{DryRun: !*apply, Limit: *limit})
	st, err := im.RunDLsite(dlsiteDB)
	if err != nil {
		slog.Error("dlsite import failed", "error", err)
		os.Exit(1)
	}
	slog.Info("dlsite import summary",
		"works_created", st.WorksCreated, "releases", st.ReleasesCreated, "titles", st.TitlesCreated,
		"labels", st.LabelsCreated, "names", st.NamesCreated, "credits", st.CreditsWritten,
		"stubs", st.Stubs, "skipped_info_only", st.SkippedInfoOnly,
		"skipped_unmapped_role", st.SkippedUnmappedRole, "already", st.Already, "errors", st.Errors,
	)
	if !*apply {
		slog.Info("DRY RUN — nothing written; re-run with --apply")
	}
}
