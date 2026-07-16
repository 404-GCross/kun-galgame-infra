// catalog-dedup-batch clears the stock cross-source entity duplication that
// step 45-48 knowingly deferred: the same character (Bangumi "冬月十夜" vs
// EG/VNDB "冬月 十夜") and the same credit name (双葉まりか imported from two
// sources) landing as two rows, which the letmoe read face then shows twice on
// one game page. It is the entity-layer twin of cmd/label-candidate-batch and
// drives the SAME service path as an admin click (ProposeMerge → ApproveMerge →
// [48h cooling] → ExecuteMerge). No schema, no bypass of the cooling window.
//
// Two dedup classes, each detected with a deliberately narrow, safe signal:
//
//	character    same WORK (roster edge or voice credit) + whitespace-folded
//	             same name + NO import source that split ≥2 of the bucket's
//	             characters. Cross-work same-name and same-source collisions
//	             (15 "Student" rows a source itself distinguished) are excluded.
//	credit_name  same non-null PERSON + whitespace-folded same name. Orphan
//	             names (no person anchor) are never name-merged.
//
// Merge direction is fixed by a deterministic survivor rule (portrait-bearing /
// richest first), so the id live consumers keep is stable.
//
//	go run ./cmd/catalog-dedup-batch -actor 1                                  # dry: counts + samples
//	go run ./cmd/catalog-dedup-batch -actor 1 -mode propose -class both -run   # propose+approve (48h clock)
//	go run ./cmd/catalog-dedup-batch -actor 1 -mode propose -class character -limit 200 -run  # canary
//	go run ./cmd/catalog-dedup-batch -actor 1 -mode execute -limit 1 -run      # execute one cooled (canary)
//	go run ./cmd/catalog-dedup-batch -actor 1 -mode execute -run               # execute all cooled
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
	"api/internal/platform/catalog/repository"
	"api/internal/platform/catalog/service"
	"api/pkg/config"
	"api/pkg/logger"

	"github.com/joho/godotenv"
	"gorm.io/gorm"
)

// noteTag marks every proposal this batch writes, so -mode execute (and any
// later audit) addresses exactly this wave and nothing else.
const noteTag = "rule:catalog-dedup step-49"

func main() {
	actor := flag.Int64("actor", 0, "operator user id recorded as proposer/approver/executor (required)")
	mode := flag.String("mode", "detect", "detect | propose | execute")
	class := flag.String("class", "both", "propose scope: character | credit_name | both")
	run := flag.Bool("run", false, "write (default: dry-run preview)")
	limit := flag.Int("limit", 0, "propose: max GROUPS this run; execute: max proposals this run (0 = all)")
	flag.Parse()

	if *actor <= 0 {
		fmt.Fprintln(os.Stderr, "-actor <user-id> is required")
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
	db := catalogDB.DB()
	ctx := context.Background()
	merge := mergeService(db)

	switch *mode {
	case "detect":
		err = runDetect(db, os.Stdout)
	case "propose":
		err = runPropose(ctx, db, os.Stdout, merge, *actor, noteTag, *class, *limit, *run)
	case "execute":
		err = runExecute(ctx, db, os.Stdout, merge, *actor, noteTag, *limit, *run)
	default:
		fmt.Fprintf(os.Stderr, "unknown mode %q\n", *mode)
		os.Exit(2)
	}
	if err != nil {
		slog.Error("catalog-dedup-batch failed", "mode", *mode, "error", err)
		os.Exit(1)
	}
}

// mergeService builds the merge service exactly as cmd/catalog does, so every
// action is byte-identical to an admin UI click.
func mergeService(db *gorm.DB) *service.MergeService {
	resolve := service.NewResolveService(repository.NewRedirectRepository(db))
	return service.NewMergeService(db, resolve,
		repository.NewProposalRepository(db), repository.NewRevisionRepository(db))
}
