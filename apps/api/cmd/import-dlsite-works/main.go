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
	dlsiteDB, err := database.OpenJob(dsn)
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
		"edges_considered", st.EdgesConsidered, "edges_written", st.EdgesWritten,
	)
	if !*apply {
		slog.Info("DRY RUN — nothing written; re-run with --apply")
	}
}
