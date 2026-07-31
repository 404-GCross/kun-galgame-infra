// catalog-credit-split detaches import-era contamination from
// catalog_credit_name rows: the wrong source anchors come off, the inferred
// person link is cleared, and an action=split revision records the pre-split
// state (refs/proj/156-p2b-adjudication.md). Logic lives in
// internal/jobs/creditsplit.
//
// Input is the adjudicated worklist — one JSON object per line:
//
//	{"credit_name_id":18996,"detach_sources":["eg"],"reason":"bare surname 井上"}
//
// --mode migrate is the wave-163 follow-up that pays off the debt a split
// leaves (see internal/jobs/creditsplit/migrate.go): it pre-mints the
// credit_name the next import would mint for the detached source id and moves
// that source's credit rows onto it, so the staff member never appears twice on
// a work page. Its worklist is one line per detached anchor:
//
//	{"credit_name_id":18996,"source":"vndb","external_id":"39609","name":"井上",
//	 "lang":"ja","matched_by":"rule:vndb-staff-import","reason":"wave 156b split"}
//
// Dry-run is the DEFAULT and --dsn is REQUIRED, never defaulted: the rehearsal
// copy locally (kun_catalog_rehearsal), the live catalog only in the acceptance
// run. A second --apply writes nothing.
//
//	go run ./cmd/catalog-credit-split --worklist w.jsonl --receipts r.jsonl \
//	    --dsn "host=localhost port=5432 user=postgres dbname=kun_catalog_rehearsal sslmode=disable"
//	go run ./cmd/catalog-credit-split --apply --actor 1 --worklist w.jsonl --receipts r.jsonl --dsn ...
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"

	"api/internal/jobs/creditsplit"
	"api/pkg/logger"

	"flag"
)

func main() {
	apply := flag.Bool("apply", false, "write changes (default: dry run, accounting only)")
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED")
	worklist := flag.String("worklist", "", "adjudicated split worklist JSONL — REQUIRED")
	receipts := flag.String("receipts", "", "write the per-row receipts (what was removed) here")
	actor := flag.Int64("actor", 0, "operator user id recorded on the split revisions (0 = none)")
	limit := flag.Int("limit", 0, "process at most this many rows (0 = all) — the canary knob")
	mode := flag.String("mode", "split", `"split" (detach wrong anchors) or "migrate" (wave 163: re-mint the detached id and move its credits)`)
	flag.Parse()

	logger.Init("development")
	var actorID *int64
	if *actor > 0 {
		actorID = actor
	}
	opts := creditsplit.Opts{
		Apply:        *apply,
		DSN:          *dsn,
		WorklistPath: *worklist,
		ReceiptsPath: *receipts,
		ActorID:      actorID,
		Limit:        *limit,
	}
	switch *mode {
	case "split":
	case "migrate":
		runMigrate(opts, *receipts)
		return
	default:
		slog.Error("catalog-credit-split: unknown --mode (want split or migrate)", "mode", *mode)
		os.Exit(1)
	}
	st, err := creditsplit.Run(context.Background(), creditsplit.Opts{
		Apply:        *apply,
		DSN:          *dsn,
		WorklistPath: *worklist,
		ReceiptsPath: *receipts,
		ActorID:      actorID,
		Limit:        *limit,
	})
	if err != nil {
		slog.Error("catalog-credit-split", "error", err)
		os.Exit(1)
	}
	if err := creditsplit.WriteReceipts(*receipts, st.Receipts); err != nil {
		slog.Error("catalog-credit-split: write receipts", "error", err)
		os.Exit(1)
	}
	for _, r := range st.Refusals {
		slog.Warn("credit-split refused", "credit_name", r.CreditNameID, "reason", r.Reason)
	}
	out, _ := json.Marshal(map[string]any{
		"apply": *apply, "rows": st.Rows, "skipped": st.Skipped, "refused": st.Refused,
		"would_drop_anchor": st.WouldDropAnchor, "would_unlink": st.WouldUnlink,
		"anchors_dropped": st.AnchorsDropped, "persons_unlinked": st.PersonsUnlinked,
		"revisions": st.Revisions,
	})
	os.Stdout.Write(append(out, '\n'))
}

func runMigrate(opts creditsplit.Opts, receipts string) {
	st, err := creditsplit.RunMigrate(context.Background(), opts)
	if err != nil {
		slog.Error("catalog-credit-split migrate", "error", err)
		os.Exit(1)
	}
	if err := creditsplit.WriteMigrateReceipts(receipts, st.Receipts); err != nil {
		slog.Error("catalog-credit-split migrate: write receipts", "error", err)
		os.Exit(1)
	}
	for _, r := range st.Refusals {
		slog.Warn("credit-migrate refused", "credit_name", r.CreditNameID, "reason", r.Reason)
	}
	out, _ := json.Marshal(map[string]any{
		"mode": "migrate", "apply": opts.Apply, "rows": st.Rows, "skipped": st.Skipped,
		"refused": st.Refused, "would_mint": st.WouldMint, "would_move_credits": st.WouldMoveCredz,
		"minted": st.Minted, "reused": st.Reused, "credits_moved": st.CreditsMoved,
		"revisions": st.Revisions,
	})
	os.Stdout.Write(append(out, '\n'))
}
