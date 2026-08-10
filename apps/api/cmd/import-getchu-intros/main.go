package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	"api/internal/jobs/getchuintros"
	"api/internal/jobs/workpop"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry-run forecast only)")
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED")
	getchuDSN := flag.String("getchu-dsn", "", "getchu staging DSN — REQUIRED")
	population := flag.String("population", string(workpop.Published),
		"which works to fill: all|bodyless|claimed|published")
	limit := flag.Int("limit", 0, "max works to process (0 = all)")
	offset := flag.Int("offset", 0, "skip this many works first")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env")
	if cfg, err := config.Load(); err == nil {
		logger.Init(cfg.Server.Env)
	}

	st, err := getchuintros.Run(context.Background(), getchuintros.Opts{
		DSN: *dsn, GetchuDSN: *getchuDSN, Apply: *apply,
		Population: workpop.Population(*population), Limit: *limit, Offset: *offset,
	})
	if st != nil {
		slog.Info("import-getchu-intros done", "apply", *apply, "result", st.String())
	}
	if err != nil {
		slog.Error("import-getchu-intros", "error", err)
		os.Exit(1)
	}
}
