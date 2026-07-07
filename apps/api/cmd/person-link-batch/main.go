// person-link-batch auto-clears the mechanically-decidable part of the L2
// shared-handle person-link backlog (doc 17 §8 / step 22 orphan doctrine): a
// candidate whose two names are literally identical (rule A1) or where one
// side's parenthetical alias list names the other (rule A2) is linked through
// the SAME path the admin bucket uses — DecideCandidate(accept), step 22's
// three-state person link. No LLM, no fuzzy matching: the mechanical rules
// cover the "literally the same" cases; the fuzzy remainder (true pen names /
// circle-member pairs) is exactly what a human must judge, so it is left in the
// admin review queue untouched.
//
// Every link is fully reversible (DetachName), unlike the L1/L3 negative
// knowledge — so an over-broad match is a one-click undo, not permanent
// damage. needs_manual (a name already belonging to a different person) is
// recorded, never force-merged.
//
//	go run ./cmd/person-link-batch --actor <ren-user-id>          # dry-run (default)
//	go run ./cmd/person-link-batch --actor <ren-user-id> --run    # link
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
	flag.Parse()

	if *actor <= 0 {
		fmt.Fprintln(os.Stderr, "--actor <ren-user-id> is required (recorded as the person-link actor)")
		os.Exit(2)
	}

	_ = godotenv.Load("apps/api/.env") // allow running from the repo root

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

	if _, err := run(context.Background(), catalogDB.DB(), os.Stdout, *actor, *apply); err != nil {
		slog.Error("person-link-batch failed", "error", err)
		os.Exit(1)
	}
}
