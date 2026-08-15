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
	if st.Errors > 0 {
		os.Exit(1)
	}
}
