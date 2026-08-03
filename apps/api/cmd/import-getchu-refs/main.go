// import-getchu-refs writes the catalog's Getchu anchors from VNDB's own
// curated external links (refs/proj/167 §1). No title matching, no probable
// tier: VNDB records the getchu id on a release we have already anchored EXACT
// to VNDB, so the new ref is exactly as strong as the one it rides on.
//
// Prerequisites: `ingest-vndb --only extlinks` and `--only releases_extlinks`
// must have staged those two dump tables, and catalog_source must carry the
// getchu row (seed).
//
// Dry-run is the DEFAULT; pass --apply to write.
//
//	go run ./cmd/import-getchu-refs
//	go run ./cmd/import-getchu-refs --apply
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
	"api/internal/jobs/getchurefs"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry-run forecast only)")
	limit := flag.Int("limit", 0, "max candidate releases to process (0 = all)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env")

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	db, err := database.NewPostgresDB(cfg.CatalogDatabase)
	if err != nil {
		slog.Error("catalog db connect", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	st, err := getchurefs.Run(context.Background(), db.DB(), getchurefs.Opts{Apply: *apply, Limit: *limit})
	if err != nil {
		slog.Error("import-getchu-refs", "error", err)
		os.Exit(1)
	}
	slog.Info("import-getchu-refs done",
		"apply", *apply, "candidates", st.Candidates, "planned", st.Planned,
		"written", st.Written, "conflict", st.Conflict, "errors", st.Errors)
}
