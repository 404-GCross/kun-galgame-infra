// import-vndb-releases projects VNDB's release layer into catalog_release as
// ADDITIVE rows (step 76) — the SKU layer's first real data. For every work
// holding an EXACT VNDB work anchor (source 2) it writes ONE row per (work,
// vndb release) pair: rtype/patch → ReleaseKind, the release's original-language
// title, the first vndb platform code (the full set in Extra), a fuzzy date
// gated to [1950, now+3], and minage/freeware/official/languages/vndb_id in the
// governed Extra jsonb. It NEVER touches an existing row — the 1:1 stubs host
// the DLsite workno anchors — and adds no columns/tables. Single-vn releases get
// an exact release anchor; multi-vn releases stay anchor-free (Extra.vndb_id
// keeps them back-referenceable). Idempotent: the (work_id, vndb_id) resume
// index makes a second pass write nothing — and it counts a soft-deleted holder
// as claimed (skipped_retired), because a retired row still answers to its vndb
// id and still squats its exact anchor slot.
//
// Logic lives in internal/platform/catalog/importer (RunReleases). src_vndb
// staging shares the catalog DB — load it first with cmd/ingest-vndb. Dry-run is
// the DEFAULT; pass --apply to write. --dsn targets the rehearsal copy locally
// (the live catalog only in the acceptance run); it falls back to the configured
// CatalogDatabase when omitted.
//
//	go run ./cmd/import-vndb-releases --dsn "host=localhost ... dbname=kun_catalog_rehearsal sslmode=disable"           # dry-run
//	go run ./cmd/import-vndb-releases --apply --dsn "..."                                                               # write (×2 = idempotent)
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
	dsn := flag.String("dsn", "", "catalog DSN (also hosts src_vndb); default: configured CatalogDatabase — pass the rehearsal copy locally")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env") // allow running from the repo root
	cfg, cfgErr := config.Load()
	if cfgErr == nil {
		logger.Init(cfg.Server.Env)
	}

	var db *gorm.DB
	switch {
	case *dsn != "":
		g, err := gorm.Open(postgres.Open(*dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
		if err != nil {
			slog.Error("catalog db connect (--dsn)", "error", err)
			os.Exit(1)
		}
		if sqlDB, err := g.DB(); err == nil {
			defer sqlDB.Close()
		}
		db = g
	case cfgErr == nil:
		catalogDB, err := database.NewPostgresDB(cfg.CatalogDatabase)
		if err != nil {
			slog.Error("catalog db connect", "error", err)
			os.Exit(1)
		}
		defer catalogDB.Close()
		db = catalogDB.DB()
	default:
		slog.Error("no --dsn given and config.Load failed", "error", cfgErr)
		os.Exit(1)
	}

	im := importer.New(db, nil, importer.Options{DryRun: !*apply, Limit: *limit})
	st, err := im.RunReleases()
	if err != nil {
		slog.Error("vndb releases import failed", "error", err)
		os.Exit(1)
	}
	slog.Info("import-vndb-releases summary",
		"apply", *apply,
		"in_gate_pairs", st.InGatePairs,
		"planned", st.Planned,
		"releases_written", st.ReleasesWritten,
		"anchors_written", st.AnchorsWritten,
		"multi_vn_unanchored", st.MultiVNUnanchored,
		"skipped_existing", st.SkippedExisting,
		"skipped_retired", st.SkippedRetired,
		"kind_default", st.KindDefault,
		"kind_trial", st.KindTrial,
		"kind_patch", st.KindPatch,
		"no_date", st.NoDate,
		"no_title", st.NoTitle,
		"errors", st.Errors,
	)
	if !*apply {
		slog.Info("DRY RUN — nothing written; re-run with --apply")
	}
}
