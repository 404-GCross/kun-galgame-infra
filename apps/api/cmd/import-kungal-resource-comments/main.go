package main

import (
	"flag"
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
	"api/pkg/config"
	"api/pkg/logger"

	"gorm.io/gorm"
)

func main() {
	sourceDSN := flag.String("source-dsn", "", "forum (kungal) source DSN; env fallback KUNGAL_SOURCE_DSN")
	targetDSN := flag.String("target-dsn", "", "kun_community target DSN; env fallback KUN_COMMUNITY_DSN, then KUN_COMMUNITY_* config")
	site := flag.String("site", "kungal", "community site/tenant for the imported threads")
	apply := flag.Bool("apply", false, "perform the writes; default is a dry-run report only")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	srcDSN := firstNonEmpty(*sourceDSN, os.Getenv("KUNGAL_SOURCE_DSN"))
	if srcDSN == "" {
		slog.Error("--source-dsn (or KUNGAL_SOURCE_DSN) is required")
		flag.Usage()
		os.Exit(1)
	}
	tgtDSN := firstNonEmpty(*targetDSN, os.Getenv("KUN_COMMUNITY_DSN"), cfg.CommunityDatabase.DSN())

	src, err := openDB(srcDSN)
	if err != nil {
		slog.Error("failed to connect to source (forum) database", "error", err)
		os.Exit(1)
	}
	tgt, err := openDB(tgtDSN)
	if err != nil {
		slog.Error("failed to connect to target (community) database", "error", err)
		os.Exit(1)
	}

	slog.Info("connected", "sources", "rating/website/toolset", "target_db", cfg.CommunityDatabase.DBName,
		"site", *site, "apply", *apply)
	if !*apply {
		slog.Info("DRY RUN — no changes will be written (pass --apply to import)")
	}

	rep, err := run(src, tgt, *site, *apply)
	if err != nil {
		slog.Error("import failed", "error", err)
		os.Exit(1)
	}
	rep.print(os.Stdout, *apply)
}

func openDB(dsn string) (*gorm.DB, error) {
	return database.OpenJob(dsn)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
