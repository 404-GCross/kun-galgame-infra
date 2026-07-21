// import-galgame-credits lands the first entity-layer data — persons (as
// orphan credit names), characters and credit edges — from Bangumi and
// erogamespace into the catalog Gold tables, identity-gated
// (Bangumi = every exact Bangumi work anchor, step-69 widened from the
// step-12 bid-audit pass layer; EG = eg-vndb-rosetta). Orphan-name-first,
// self-anchored, whole-source-rollbackable; no person rows, no auto-merge.
//
//	go run ./cmd/migrate-catalog                              # land the EG role map
//	go run ./cmd/import-galgame-credits --source all          # dry-run
//	go run ./cmd/import-galgame-credits --source all --apply  # write (×2 = idempotent)
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
	source := flag.String("source", "all", "bangumi | eg | all")
	apply := flag.Bool("apply", false, "write (default: dry run — plan counts only)")
	limit := flag.Int("limit", 0, "cap works processed per wave (0 = all)")
	egDSN := flag.String("eg-dsn", "", "erogamespace staging DSN (default: erogamespace on the catalog server)")
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

	// EG is needed for the eg/all waves and for the shared-handle candidates.
	var egDB *gorm.DB
	if *source == "eg" || *source == "all" {
		egDB = openEG(cfg, *egDSN)
	}

	im := importer.New(catalogDB.DB(), egDB, importer.Options{Source: *source, DryRun: !*apply, Limit: *limit})
	stats, err := im.Run(*source)
	if err != nil {
		slog.Error("import failed", "error", err)
		os.Exit(1)
	}
	slog.Info("import summary",
		"source", *source,
		"names_created", stats.NamesCreated,
		"labels_created", stats.LabelsCreated,
		"characters_created", stats.CharactersCreated,
		"credits_written", stats.CreditsWritten,
		"skipped_unmapped_role", stats.SkippedUnmappedRole,
		"skipped_gate", stats.SkippedGate,
		"already", stats.Already,
		"errors", stats.Errors,
	)

	// Cross-source candidates need both waves imported + the EG connection.
	if *source == "all" && egDB != nil {
		cs, err := im.RunCandidates()
		if err != nil {
			slog.Error("candidates failed", "error", err)
			os.Exit(1)
		}
		slog.Info("shared-handle candidates",
			"twitter_handles", cs.Twitter, "pixiv_handles", cs.Pixiv,
			"written", cs.Written, "already", cs.Already, "ambiguous_skipped", cs.AmbiguousSkipped)
	}

	if !*apply {
		slog.Info("DRY RUN — nothing written; re-run with --apply")
	}
}

func openEG(cfg *config.Config, dsn string) *gorm.DB {
	if dsn == "" {
		egCfg := cfg.CatalogDatabase
		egCfg.DBName = "erogamespace"
		dsn = egCfg.DSN()
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		slog.Error("erogamespace connect", "error", err)
		os.Exit(1)
	}
	return db
}
