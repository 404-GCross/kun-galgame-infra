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
	source := flag.String("source", "all", "bgm | vndb | eg | all")
	apply := flag.Bool("run", false, "write (default: dry run — plan counts only)")
	limit := flag.Int("limit", 0, "cap edges written by the vndb lane (0 = all; rehearsal small-sample aid; the bgm lane always writes all)")
	flag.Parse()

	switch *source {
	case "bgm", "vndb", "eg", "all":
	default:
		slog.Error("unknown --source (want bgm|vndb|eg|all)", "source", *source)
		os.Exit(1)
	}

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

	var egPool *gorm.DB
	if *source == "eg" || *source == "all" {
		egCfg := cfg.CatalogDatabase
		egCfg.DBName = "erogamespace"
		egConn, err := database.NewPostgresDB(egCfg)
		if err != nil {
			slog.Error("erogamespace db connect", "error", err)
			os.Exit(1)
		}
		defer egConn.Close()
		egPool = egConn.DB()
	}

	im := importer.New(catalogDB.DB(), egPool, importer.Options{DryRun: !*apply, Limit: *limit})

	if *source == "bgm" || *source == "all" {
		st, err := im.RunBangumiRelations()
		if err != nil {
			slog.Error("bangumi relations wave failed", "error", err)
			os.Exit(1)
		}
		slog.Info("bangumi relations wave summary",
			"total_rows", st.TotalRows, "mapped", st.Mapped, "edges", st.Edges,
			"edges_written", st.EdgesWritten, "already_in_db", st.AlreadyInDB,
			"inverse_folded", st.InverseFolded, "skipped_other", st.SkippedOther,
			"skipped_unanchored", st.SkippedUnanchored, "skipped_self", st.SkippedSelf,
		)
	}
	if *source == "vndb" || *source == "all" {
		st, err := im.RunVNDBRelations()
		if err != nil {
			slog.Error("vndb relations wave failed", "error", err)
			os.Exit(1)
		}
		slog.Info("vndb relations wave summary",
			"total_rows", st.TotalRows, "mapped", st.Mapped, "edges", st.Edges,
			"edges_written", st.EdgesWritten, "already_in_db", st.AlreadyInDB,
			"inverse_folded", st.InverseFolded, "skipped_unmapped", st.SkippedUnmapped,
			"skipped_unanchored", st.SkippedUnanchored, "skipped_self", st.SkippedSelf,
		)
	}
	if *source == "eg" || *source == "all" {
		st, err := im.RunEGRelations()
		if err != nil {
			slog.Error("eg relations wave failed", "error", err)
			os.Exit(1)
		}
		slog.Info("eg relations wave summary",
			"total_rows", st.TotalRows, "mapped", st.Mapped, "edges", st.Edges,
			"edges_written", st.EdgesWritten, "already_in_db", st.AlreadyInDB,
			"folded", st.Folded, "skipped_by_design", st.SkippedByDesign,
			"skipped_unmapped", st.SkippedUnmapped, "skipped_unanchored", st.SkippedUnanchored,
			"skipped_self", st.SkippedSelf,
		)
	}
	if !*apply {
		slog.Info("DRY RUN — nothing written; re-run with --run")
	}
}
