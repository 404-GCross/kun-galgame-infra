// label-candidate-batch clears the label name-collision review backlog left
// by the wiki officials registration (step 35): every pending entity_type=3
// candidate pairs an existing catalog label with a freshly-imported wiki
// official label whose display name norm-collides with it.
//
// Four modes, all driving the SAME service path as an admin click
// (DecideCandidate → ProposeMerge → ApproveMerge → ExecuteMerge):
//
//	-mode mechanical   pairs sharing ≥1 catalog work are the same brand by
//	                   construction (both labels are attached to the same
//	                   work) → accept + approve. Dry-run by default.
//	-mode export       dossier JSONL for the non-mechanical remainder (both
//	                   sides' aliases, refs, org, work samples) — the input
//	                   for LLM/human adjudication.
//	-mode receipts     apply an adjudicated dossier (verdict merge/keep/
//	                   unsure) — merge accepts+approves, keep rejects,
//	                   unsure stays pending.
//	-mode execute      execute this batch's approved proposals once the 48h
//	                   cooling window has passed. -limit 1 first (canary).
//
// Merge direction is FIXED policy, not a per-pair judgment: the wiki-import
// label (note rule:galgame-official-import) is absorbed into the pre-existing
// label, so ids referenced by live consumers stay stable and the import's
// edges/refs rehang onto the survivor.
//
//	go run ./cmd/label-candidate-batch -actor 1                      # dry
//	go run ./cmd/label-candidate-batch -actor 1 -run                 # accept+approve
//	go run ./cmd/label-candidate-batch -actor 1 -mode export -out d.jsonl
//	go run ./cmd/label-candidate-batch -actor 1 -mode receipts -receipts r.tsv -run
//	go run ./cmd/label-candidate-batch -actor 1 -mode execute -limit 1 -run
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

// noteTag marks every proposal/decision this batch writes, so -mode execute
// (and any later audit) can address exactly this wave and nothing else.
const noteTag = "rule:label-official-collision step-44"

func main() {
	actor := flag.Int64("actor", 0, "operator user id recorded as decider/approver/executor (required)")
	mode := flag.String("mode", "mechanical", "mechanical | export | receipts | execute")
	run := flag.Bool("run", false, "write (default: dry-run preview)")
	out := flag.String("out", "", "export mode: dossier JSONL path (required for export)")
	receipts := flag.String("receipts", "", "receipts mode: adjudicated TSV path (required for receipts)")
	limit := flag.Int("limit", 0, "execute mode: max proposals to execute this run (0 = all cooled)")
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

	switch *mode {
	case "mechanical":
		err = runMechanical(ctx, db, os.Stdout, *actor, *run)
	case "export":
		if *out == "" {
			fmt.Fprintln(os.Stderr, "-out <jsonl> is required for export")
			os.Exit(2)
		}
		err = runExport(db, *out)
	case "receipts":
		if *receipts == "" {
			fmt.Fprintln(os.Stderr, "-receipts <tsv> is required for receipts")
			os.Exit(2)
		}
		err = runReceipts(ctx, db, os.Stdout, *receipts, *actor, *run)
	case "execute":
		err = runExecute(ctx, db, os.Stdout, *actor, *limit, *run)
	default:
		fmt.Fprintf(os.Stderr, "unknown mode %q\n", *mode)
		os.Exit(2)
	}
	if err != nil {
		slog.Error("label-candidate-batch failed", "mode", *mode, "error", err)
		os.Exit(1)
	}
}

// services builds the review-queue + merge services exactly as cmd/catalog
// does, so every action is byte-identical to an admin UI click.
func services(db *gorm.DB) (*service.AdminQueueService, *service.MergeService) {
	resolve := service.NewResolveService(repository.NewRedirectRepository(db))
	merge := service.NewMergeService(db, resolve,
		repository.NewProposalRepository(db), repository.NewRevisionRepository(db))
	return service.NewAdminQueueService(db, merge), merge
}
