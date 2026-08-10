package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

func main() {
	apply := flag.Bool("run", false, "write links (default: dry-run preview only)")
	actor := flag.Int64("actor", 0, "ren superadmin user id recorded as the linker (required)")
	ruleSet := flag.String("rule-set", ruleSetShared, "shared = A1/A2 over shared-handle candidates (default); alias = A3/A4 over alias_declared candidates")
	export := flag.String("export", "", "write the T2 review worklist (alias_declared pending not cleared by A3/A4) to this TSV, then exit")
	receipts := flag.String("receipts", "", "apply a filled worklist TSV (decision: link/reject/skip) instead of the rule pass")
	flag.Parse()

	if *actor <= 0 {
		fmt.Fprintln(os.Stderr, "--actor <ren-user-id> is required (recorded as the person-link actor)")
		os.Exit(2)
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
	db := catalogDB.DB()
	ctx := context.Background()

	switch {
	case *receipts != "":
		if _, err = runReceipts(ctx, db, os.Stdout, *receipts, *actor, *apply); err != nil {
			slog.Error("receipts apply failed", "error", err)
			os.Exit(1)
		}
	case *export != "":
		if err = runExport(db, *export); err != nil {
			slog.Error("export failed", "error", err)
			os.Exit(1)
		}
	default:
		if _, err = run(ctx, db, os.Stdout, *actor, *apply, *ruleSet); err != nil {
			slog.Error("person-link-batch failed", "error", err)
			os.Exit(1)
		}
	}
}
