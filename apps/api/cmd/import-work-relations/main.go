// import-work-relations lands work↔work relation edges from the Bangumi
// game-domain subject relations (source 3) and the VNDB vn↔vn relations
// (source 2) as catalog_work_relation edges (doc 77 REL1). An edge is written
// only when BOTH endpoints carry an exact work anchor for the lane's source.
// Inverse pairs fold to one directed edge; symmetric types normalize a<b; a
// pair asserted by both sources converges on one edge (ON CONFLICT DO NOTHING),
// so existing edges are never modified.
//
//	go run ./cmd/import-work-relations --source all         # dry-run (default)
//	go run ./cmd/import-work-relations --source vndb --run  # write the vndb lane
//	go run ./cmd/import-work-relations --source bgm  --run  # (re-)write the bgm lane
//
// Single connection: both src_bangumi.subject_relation and src_vndb.vn_relations
// live in the catalog DB.
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
	source := flag.String("source", "all", "bgm | vndb | all")
	apply := flag.Bool("run", false, "write (default: dry run — plan counts only)")
	limit := flag.Int("limit", 0, "cap edges written by the vndb lane (0 = all; rehearsal small-sample aid; the bgm lane always writes all)")
	flag.Parse()

	switch *source {
	case "bgm", "vndb", "all":
	default:
		slog.Error("unknown --source (want bgm|vndb|all)", "source", *source)
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

	im := importer.New(catalogDB.DB(), nil, importer.Options{DryRun: !*apply, Limit: *limit})

	// Bangumi first, then VNDB — so the VNDB lane reads the Bangumi lane's
	// freshly-written edges and reports overlapping pairs as already-in-db
	// (cross-source convergence), never a duplicate insert.
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
	if !*apply {
		slog.Info("DRY RUN — nothing written; re-run with --run")
	}
}
