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
	apply := flag.Bool("apply", false, "write (default: dry run — plan counts only)")
	limit := flag.Int("limit", 0, "cap works processed (0 = all)")
	dsn := flag.String("dsn", "", "catalog DSN (also hosts src_vndb); default: configured CatalogDatabase — pass the rehearsal copy locally")
	staleOut := flag.String("stale-anchors-out", "", "TSV path for the stale-anchor review worklist (r-ids whose exact anchor sits under a work upstream no longer maps them to); empty = don't write")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env")
	cfg, cfgErr := config.Load()
	if cfgErr == nil {
		logger.Init(cfg.Server.Env)
	}

	var db *gorm.DB
	switch {
	case *dsn != "":
		g, err := database.OpenJob(*dsn)
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

	im := importer.New(db, nil, importer.Options{DryRun: !*apply, Limit: *limit, StaleAnchorsOut: *staleOut})
	st, err := im.RunReleases()
	if err != nil {
		slog.Error("vndb releases import failed", "error", err)
		os.Exit(1)
	}
	slog.Info("import-vndb-releases summary",
		"apply", *apply,
		"in_gate_pairs", st.InGatePairs,
		"planned", st.Planned,
		"probable_backfilled", st.ProbableBackfilled,
		"releases_written", st.ReleasesWritten,
		"anchors_written", st.AnchorsWritten,
		"probable_refs_written", st.ProbableRefsWritten,
		"multi_vn_unanchored", st.MultiVNUnanchored,
		"anchor_held_by_other", st.AnchorHeldByOther,
		"stale_anchor_holders", st.StaleAnchorHolders,
		"anchor_race_lost", st.AnchorRaceLost,
		"batch_failures", st.BatchFailures,
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
