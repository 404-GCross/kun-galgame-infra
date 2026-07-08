// import-bangumi-relations lands the Bangumi game-domain subject relations as
// catalog work↔work edges (step 30): for every relation whose BOTH subjects
// carry an exact bangumi work anchor, write one directed catalog_work_relation
// (inverse pairs folded, symmetric types normalized a<b). Cross-media edges are
// excluded by construction (the other end is unregistered → unanchored).
//
//	go run ./cmd/import-bangumi-relations           # dry-run (default)
//	go run ./cmd/import-bangumi-relations --run      # write (×2 = idempotent)
//
// Single connection: src_bangumi.subject_relation lives in the catalog DB.
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
	apply := flag.Bool("run", false, "write (default: dry run — plan counts only)")
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

	im := importer.New(catalogDB.DB(), nil, importer.Options{DryRun: !*apply})
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
	if !*apply {
		slog.Info("DRY RUN — nothing written; re-run with --run")
	}
}
