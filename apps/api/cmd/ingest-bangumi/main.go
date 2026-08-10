package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"api/internal/infrastructure/database"
	"api/internal/platform/catalog/srcbangumi"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	dumpDir := flag.String("dump-dir", "refs/bangumi-dump", "directory containing the extracted *.jsonlines dump files")
	only := flag.String("only", "", "ingest a single file (e.g. subject, subject-relations)")
	flag.Parse()

	_ = godotenv.Load("apps/api/.env")

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	dir, err := resolveDumpDir(*dumpDir)
	if err != nil {
		slog.Error("resolve dump dir", "error", err)
		os.Exit(1)
	}

	db, err := database.NewPostgresDB(cfg.CatalogDatabase)
	if err != nil {
		slog.Error("catalog db connect", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// The Silver schema is tool-owned (fully rebuildable staging) — it is
	// deliberately NOT part of cmd/migrate-catalog's Gold migration order.
	if err := srcbangumi.EnsureSchema(db.DB()); err != nil {
		slog.Error("ensure src_bangumi schema", "error", err)
		os.Exit(1)
	}

	slog.Info("ingesting bangumi dump", "dir", dir, "only", *only, "parser", srcbangumi.ParserVersion)
	report, err := srcbangumi.Run(db.DB(), dir, *only)
	if err != nil {
		slog.Error("ingest failed", "error", err)
		os.Exit(1)
	}
	for _, name := range srcbangumi.Files {
		if fr, ok := report.PerFile[name]; ok {
			slog.Info("ingested", "file", name, "rows", fr.Rows,
				"parse_errors", fr.ParseErrors, "ms", fr.DurationMS)
		}
	}
	slog.Info("bangumi ingest completed", "total", report.Duration.String())
}

func resolveDumpDir(dir string) (string, error) {
	for _, candidate := range []string{dir, filepath.Join("..", "..", dir)} {
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("dump directory not found: %s (tried as-is and ../../)", dir)
}
