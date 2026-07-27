// catalog-dedup-batch clears the stock cross-source entity duplication that
// step 45-48 knowingly deferred: the same character (Bangumi "冬月十夜" vs
// EG/VNDB "冬月 十夜") and the same credit name (双葉まりか imported from two
// sources) landing as two rows, which the letmoe read face then shows twice on
// one game page. It is the entity-layer twin of cmd/label-candidate-batch and
// drives the SAME service path as an admin click (ProposeMerge → ApproveMerge →
// [48h cooling] → ExecuteMerge). No schema, no bypass of the cooling window.
//
// Dedup classes, each detected with a deliberately narrow, safe signal:
//
//	character        (step 49) same WORK (roster edge or voice credit) +
//	                 whitespace-folded same name + NO import source that split
//	                 ≥2 of the bucket's characters. Cross-work same-name and
//	                 same-source collisions (15 "Student" rows a source itself
//	                 distinguished) are excluded.
//	credit_name      (step 49) same non-null PERSON + whitespace-folded same
//	                 name.
//	orphan-creditname (step 50) same WORK (voice credit) + whitespace-folded
//	                 same name among ORPHAN names (person_id NULL), with the
//	                 same source-distinctness guard. This is what step 49's
//	                 person-anchored credit_name class deliberately left alone.
//
// Merge direction is fixed by a deterministic survivor rule (portrait-bearing /
// richest first), so the id live consumers keep is stable.
//
// A fourth mode, -mode cleanup (step 50 class B), is NOT a merge: it DELETEs the
// redundant empty-role voice credits (same work+name+role holding both a
// character-bearing and a character-null credit — EG's staff 声優 + appearance
// 出演 double list). Run it AFTER the orphan merges execute.
//
//	go run ./cmd/catalog-dedup-batch -actor 1                                             # dry: counts + samples (all classes)
//	go run ./cmd/catalog-dedup-batch -actor 1 -mode propose -class both -run              # step 49 propose+approve (48h clock)
//	go run ./cmd/catalog-dedup-batch -actor 1 -mode propose -class orphan-creditname -run # step 50 orphan propose+approve
//	go run ./cmd/catalog-dedup-batch -actor 1 -mode execute -class orphan-creditname -limit 1 -run  # execute one cooled (canary)
//	go run ./cmd/catalog-dedup-batch -actor 1 -mode execute -class orphan-creditname -run # execute all cooled step-50
//	go run ./cmd/catalog-dedup-batch -actor 1 -mode cleanup                               # class B dry: count + samples
//	go run ./cmd/catalog-dedup-batch -actor 1 -mode cleanup -run                          # class B delete
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

// Each proposal is tagged with its wave so -mode execute (and any later audit)
// addresses exactly one wave. Step 49's classes (character + person-anchored
// credit_name) carry the step-49 tag; step 50's orphan voice-name class carries
// step-50. "step-49" is not a substring of "step-50", so the LIKE filters never
// cross.
const (
	waveTag49 = "rule:catalog-dedup step-49"
	waveTag50 = "rule:catalog-dedup step-50"
	// waveTag98 tags the QA-undermerge clearing wave (refs/proj/98): the
	// re-run character class, the all-roles orphan class and the new mixed
	// class all carry it, so -mode execute addresses exactly this wave (the
	// 49/50 tags stay on their fully-executed historical proposals).
	waveTag98 = "rule:catalog-dedup step-98"
)

// noteTagFor returns the wave note tag for a class (used on both the write side,
// per group, and the execute side, derived from -class). Step 98 onward every
// NEW proposal carries waveTag98 — the historical 49/50 tags stay on their
// fully-executed proposals and are never written again.
func noteTagFor(class string) string {
	_ = classOrphanCreditName // all classes now tag the current wave
	return waveTag98
}

func main() {
	actor := flag.Int64("actor", 0, "operator user id recorded as proposer/approver/executor (required)")
	mode := flag.String("mode", "detect", "detect | propose | execute | cleanup")
	class := flag.String("class", "both", "propose/execute scope: character | credit_name | both (step 49) | orphan-creditname (all roles since step 98) | mixed-creditname (step 98)")
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
	merge, resolve := mergeService(db)

	switch *mode {
	case "detect":
		err = runDetect(db, os.Stdout)
	case "propose":
		err = runPropose(ctx, db, os.Stdout, merge, *actor, *class, *limit, *run)
	case "execute":
		err = runExecute(ctx, db, os.Stdout, merge, resolve, *actor, noteTagFor(*class), *limit, *run)
	case "cleanup":
		err = runCleanup(db, os.Stdout, *run)
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
func mergeService(db *gorm.DB) (*service.MergeService, *service.ResolveService) {
	resolve := service.NewResolveService(repository.NewRedirectRepository(db))
	return service.NewMergeService(db, resolve,
		repository.NewProposalRepository(db), repository.NewRevisionRepository(db)), resolve
}
