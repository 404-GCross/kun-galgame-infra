// merge-dup-submissions drives an EXPLICIT work merge list through the full
// merge protocol (propose → approve → execute), for the wave-170 finding: user
// submissions filed during the cover-less window duplicating works the
// registry already held.
//
// It differs from cmd/merge-work-dups (step 97) in exactly one dimension: the
// pairs are explicit `source:target` arguments with no worklist-specific
// sanity checks, because here the SOURCE may itself be claimed (an old
// kungal draft absorbed into the fresh live submission, or a fresh duplicate
// absorbed into the established live entry). Direction is the operator's
// decision per pair — the rule used for wave 170 is "the live, established
// side survives".
//
// The cooling-off window is NOT bypassed here: propose mode approves with the
// standard 48h timer, and execute mode refuses early. Releasing the timer
// early is a deliberate ops decision recorded outside this tool.
//
//	go run ./cmd/merge-dup-submissions -mode propose --dsn "$DSN" -actor 2 \
//	    -pair 23020:228616 -pair 228621:228618 [-run]
//	go run ./cmd/merge-dup-submissions -mode execute --dsn "$DSN" -actor 2 \
//	    -pair 23020:228616 [-run]
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"api/internal/infrastructure/database"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"
	"api/internal/platform/catalog/service"
	"api/pkg/logger"

	"gorm.io/gorm"
)

// notePrefix tags every proposal this tool opens so execute mode (and any
// later audit) can scope to exactly this wave.
const notePrefix = "dup-submission(170): "

type pair struct{ src, dst int64 }

type pairFlag []pair

func (p *pairFlag) String() string { return fmt.Sprint(*p) }

func (p *pairFlag) Set(v string) error {
	a, b, ok := strings.Cut(v, ":")
	if !ok {
		return fmt.Errorf("pair %q: want source:target", v)
	}
	src, err := strconv.ParseInt(a, 10, 64)
	if err != nil {
		return fmt.Errorf("pair %q: bad source: %w", v, err)
	}
	dst, err := strconv.ParseInt(b, 10, 64)
	if err != nil {
		return fmt.Errorf("pair %q: bad target: %w", v, err)
	}
	if src == dst {
		return fmt.Errorf("pair %q: source == target", v)
	}
	*p = append(*p, pair{src, dst})
	return nil
}

func main() {
	mode := flag.String("mode", "", "propose | execute")
	dsn := flag.String("dsn", "", "catalog DSN (REQUIRED)")
	actor := flag.Int64("actor", 0, "acting user id (REQUIRED)")
	run := flag.Bool("run", false, "write (default dry)")
	var pairs pairFlag
	flag.Var(&pairs, "pair", "source:target work ids (repeatable, REQUIRED)")
	flag.Parse()

	logger.Init("development")
	if *dsn == "" || *actor == 0 || len(pairs) == 0 || (*mode != "propose" && *mode != "execute") {
		fmt.Fprintln(os.Stderr, "--dsn, -actor, -pair and -mode propose|execute are required")
		os.Exit(2)
	}
	db, err := database.OpenJob(*dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open db:", err)
		os.Exit(1)
	}
	resolve := service.NewResolveService(repository.NewRedirectRepository(db))
	merge := service.NewMergeService(db, resolve,
		repository.NewProposalRepository(db), repository.NewRevisionRepository(db))
	ctx := context.Background()

	failed := 0
	for _, pr := range pairs {
		var err error
		switch *mode {
		case "propose":
			err = propose(ctx, merge, pr, *actor, *run)
		case "execute":
			err = execute(ctx, db, merge, pr, *actor, *run)
		}
		if err != nil {
			failed++
			fmt.Printf("FAIL  %d -> %d: %v\n", pr.src, pr.dst, err)
		}
	}
	fmt.Printf("done: mode=%s pairs=%d failed=%d run=%v\n", *mode, len(pairs), failed, *run)
	if failed > 0 {
		os.Exit(1)
	}
}

// propose opens + approves one pair's proposal. Idempotent: an existing
// open/approved proposal for the pair is reported, not duplicated (the
// partial unique index enforces it; the error is surfaced as a skip).
func propose(ctx context.Context, merge *service.MergeService, pr pair, actor int64, run bool) error {
	note := fmt.Sprintf("%swork %d duplicates work %d", notePrefix, pr.src, pr.dst)
	if !run {
		fmt.Printf("DRY   would propose+approve %d -> %d (%s)\n", pr.src, pr.dst, note)
		return nil
	}
	p, err := merge.ProposeMerge(ctx, model.EntityTypeWork, pr.src, pr.dst, actor, note)
	if err != nil {
		return fmt.Errorf("propose: %w", err)
	}
	if err := merge.ApproveMerge(ctx, p.ID, actor); err != nil {
		return fmt.Errorf("approve %d: %w", p.ID, err)
	}
	fmt.Printf("OK    proposed+approved %d -> %d (proposal %d)\n", pr.src, pr.dst, p.ID)
	return nil
}

// execute runs one pair's APPROVED proposal through ExecuteMerge. The
// proposal is located by (entity_type, pair, status, note tag) so this never
// touches another wave's queue.
func execute(ctx context.Context, db *gorm.DB, merge *service.MergeService, pr pair, actor int64, run bool) error {
	var ids []int64
	err := db.WithContext(ctx).Raw(`
		SELECT id FROM catalog_merge_proposal
		WHERE entity_type = ? AND source_entity_id = ? AND target_entity_id = ?
			AND status = ? AND note LIKE ?`,
		model.EntityTypeWork, pr.src, pr.dst, model.ProposalStatusApproved, notePrefix+"%").Scan(&ids).Error
	if err != nil {
		return err
	}
	if len(ids) != 1 {
		return fmt.Errorf("want exactly 1 approved proposal, found %d", len(ids))
	}
	if !run {
		fmt.Printf("DRY   would execute proposal %d (%d -> %d)\n", ids[0], pr.src, pr.dst)
		return nil
	}
	if err := merge.ExecuteMerge(ctx, ids[0], &actor); err != nil {
		return err
	}
	fmt.Printf("OK    executed proposal %d (%d -> %d)\n", ids[0], pr.src, pr.dst)
	return nil
}
