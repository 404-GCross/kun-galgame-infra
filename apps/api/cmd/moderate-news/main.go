package main

import (
	"context"
	"encoding/json"
	"flag"
	"log/slog"
	"os"
	"time"

	"api/internal/jobs/newsmoderate"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	apply := flag.Bool("apply", false, "write verdicts to kun_news (default: grade and report, write nothing)")
	limit := flag.Int("limit", 50, "maximum items to grade in one pass")
	gap := flag.Duration("gap", 200*time.Millisecond, "delay between items")
	dsn := flag.String("dsn", "", "kun_news DSN override (default: KUN_NEWS_PG_*)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env")
	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	stats, err := newsmoderate.Run(context.Background(), cfg, newsmoderate.Opts{
		Apply: *apply, Limit: *limit, Gap: *gap, DSN: *dsn,
	})
	b, _ := json.MarshalIndent(stats, "", "  ")
	os.Stdout.Write(append(b, '\n'))
	if err != nil {
		slog.Error("moderate-news", "error", err)
		os.Exit(1)
	}
	if stats.Failed > 0 {
		os.Exit(1)
	}
}
