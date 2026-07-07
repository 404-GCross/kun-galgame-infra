// import-entity-aliases mines the idle source-side alias data of
// already-imported catalog entities into two capabilities (doc 17 B-track,
// step 25), both zero-LLM:
//
//	Leg A (--hints)      — ingest bangumi/EG aliases as kind=search_hint rows
//	                       (search-only, never displayed); reindex makes entity
//	                       search match any community alias. Zero identity claim.
//	Leg B (--candidates) — a source alias that folds to another source's whole
//	                       credit_name name becomes an alias_declared match
//	                       candidate (always pending — NEVER auto-linked).
//
// With no leg flag, both run (leg A then leg B). Dry-run is the default.
//
//	go run ./cmd/import-entity-aliases            # dry, both legs
//	go run ./cmd/import-entity-aliases --run      # write
//	go run ./cmd/import-entity-aliases --hints --run
//
// Both legs read everything from kun_catalog: bangumi aliases from src_bangumi,
// EG/DLsite aliases from the catalog credit_name name parentheses (step 12/24
// established the EG betumei column is unusable), and every target name from
// catalog credit_names — so no EG/DLsite staging connection is needed.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
)

// Catalog source ids (catalog_source seed) — the two alias-bearing origin
// sources (DLsite=4 credit_names appear only as leg-B match targets, read from
// the DB by source_id, so no constant is needed).
const (
	sourceBangumi int16 = 3
	sourceEG      int16 = 5
)

func main() {
	apply := flag.Bool("run", false, "write changes (default: dry-run preview)")
	hints := flag.Bool("hints", false, "run leg A only (search-hint ingestion)")
	candidates := flag.Bool("candidates", false, "run leg B only (alias_declared candidates)")
	flag.Parse()

	// No leg flag → both.
	runA, runB := *hints, *candidates
	if !runA && !runB {
		runA, runB = true, true
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

	if runA {
		if _, err := runHints(db, os.Stdout, *apply); err != nil {
			slog.Error("leg A failed", "error", err)
			os.Exit(1)
		}
	}
	if runB {
		if _, err := runCandidates(db, os.Stdout, *apply); err != nil {
			slog.Error("leg B failed", "error", err)
			os.Exit(1)
		}
	}

	if *apply && runA {
		fmt.Fprintln(os.Stdout, "note: run `go run ./cmd/reindex-catalog` to project the new search hints into Meilisearch.")
	} else if !*apply {
		fmt.Fprintln(os.Stdout, "DRY-RUN — nothing written; re-run with --run.")
	}
}
