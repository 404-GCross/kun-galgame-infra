package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
	"api/internal/platform/catalog/introimport"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run, statistics only)")
	limit := flag.Int("limit", 0, "max candidate works to consider (0 = all)")
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
		slog.Error("catalog db connect", "error", err, "dbname", cfg.CatalogDatabase.DBName)
		os.Exit(1)
	}
	defer db.Close()

	st, err := introimport.Run(context.Background(), db.DB(), introimport.Options{DryRun: !*apply, Limit: *limit})
	if err != nil {
		slog.Error("backfill failed", "error", err)
		os.Exit(1)
	}
	slog.Info("work-intro backfill",
		"apply", *apply,
		"total_bodyless", st.TotalBodyless,
		"with_vndb_anchor", st.WithVNDBAnchor,
		"skipped_no_anchor", st.SkippedNoAnchor,
		"skipped_empty_desc", st.SkippedEmptyDesc,
		"already", st.Already,
		"intros_written", st.IntrosWritten,
		"works_covered", st.WorksCovered,
	)
}
