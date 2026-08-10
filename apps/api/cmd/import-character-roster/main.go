// import-character-roster lands the work↔character花名册 edges (step 45) that
// the credit wave (step 13) left out. It imports catalog_work_character from
// Bangumi (src_bangumi.subject_character), erogamespace (appearances) and VNDB
// (src_vndb.chars_vns, step 47 — with the per-appearance spoiler level and the
// same-work same-name attach), gated only by an EXACT work anchor (identity is
// settled, so no second audit gate). Missing characters are created as orphan
// entities with self exact anchors (rule:<source>-character-import, the step-13
// rule); no persons. Roster edges are INDEPENDENT of credits — credit rows are
// never touched.
//
// VNDB staging (src_vndb) lives in the catalog DB — load it first with
// cmd/ingest-vndb. Bangumi/EG staging are their own tools.
//
//	go run ./cmd/migrate-catalog                                 # land the schema (spoiler column)
//	go run ./cmd/import-character-roster --source vndb           # dry-run (or all)
//	go run ./cmd/import-character-roster --source vndb --apply   # write (×2 = idempotent)
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
	"gorm.io/gorm"
)

func main() {
	source := flag.String("source", "all", "bangumi | eg | vndb | all")
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

	var egDB *gorm.DB
	if *source == "eg" || *source == "all" {
		egDB = openEG(cfg, *egDSN)
	}

	im := importer.New(catalogDB.DB(), egDB, importer.Options{Source: *source, DryRun: !*apply, Limit: *limit})
	stats, err := im.RunRoster(*source)
	if err != nil {
		slog.Error("roster import failed", "error", err)
		os.Exit(1)
	}
	slog.Info("roster import summary",
		"source", *source,
		"characters_created", stats.CharactersCreated,
		"attached_existing", stats.AttachedExisting,
		"aliases_created", stats.AliasesCreated,
		"edges_written", stats.EdgesWritten,
		"already", stats.Already,
		"skipped_no_work_anchor", stats.SkippedNoWorkAnchor,
		"skipped_no_name", stats.SkippedNoName,
		"skipped_claimed_probable", stats.SkippedClaimedProbable,
		"skipped_retired_exact_squat", stats.SkippedRetiredExactSquat,
		"portrait_candidates", stats.PortraitCandidates,
		"errors", stats.Errors,
	)

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
	db, err := database.OpenJob(dsn)
	if err != nil {
		slog.Error("erogamespace connect", "error", err)
		os.Exit(1)
	}
	return db
}
