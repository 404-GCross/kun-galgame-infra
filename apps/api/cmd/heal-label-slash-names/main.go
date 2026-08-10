// heal-label-slash-names renames the EIGHT labels a human adjudicated as true
// brand-family monsters out of the 127 live labels whose display_name is a
// `/`-joined LIST of brands (refs/proj/175; the read-only survey that produced
// the list is cmd/report-label-slash-names).
//
// The slash strings come in verbatim from the DLsite maker field: one maker
// page may front a whole brand family, so label 5960's display_name is
//
//	インターハート / Candy Soft / ぐみそふと / はちみつそふと / REAL / ...
//
// which is not a name any read face can show. The heal is deliberately NOT a
// rule — the adjudication was per label, and the 119 labels not listed here
// stay exactly as they are. The 9th adjudicated case (labels 11871 + 12775,
// the same 「アーカムプロダクツ / チーム暗黒媒体」 imported twice) is a MERGE,
// not a rename, and goes through cmd/catalog-dedup-batch -worklist instead:
// this tool never deletes or merges anything.
//
// Per case the tool does three things, all inside one transaction:
//
//  1. display_name  → the adjudicated canonical name.
//  2. the FULL original slash string → catalog_label_alias kind=2
//     (AliasKindSearchHint, findability only, never displayed) so a user who
//     searches the old blob still lands on the label.
//  3. every '/'-separated SEGMENT other than the canonical → kind=1
//     (AliasKindSpellingVariant): the sibling brands are real names of this
//     label's family, and dropping them would lose search reach the monster
//     string accidentally provided.
//
// Nothing is ever marked is_primary_for_locale — the canonical display_name is
// the primary, and a second primary for one locale is a contradiction.
//
// DRIFT GUARD. Each case pins the display_name the human actually adjudicated.
// If the live row no longer carries that exact string the case is SKIPPED with
// a loud log and the run continues: a row that changed since the adjudication
// has not been adjudicated, and healing it would apply a verdict to a different
// fact. This is also what makes the tool idempotent — after a successful apply
// the display_name is the canonical (no slash), so the guard skips every case
// on a second run and nothing is written twice.
//
// The DLsite importer is create-only for labels, so a healed name cannot be
// revived by the next import wave.
//
//	go run ./cmd/heal-label-slash-names --dsn "..."            # dry run (default)
//	go run ./cmd/heal-label-slash-names --dsn "..." --apply    # write
//
// AFTER --apply the Meilisearch labels index must be rebuilt — see the note on
// reindexNote below.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
)

// reindexNote is printed at the end of an --apply run. catalog_labels documents
// are built ONLY by cmd/reindex-catalog (reindexLabels) — there is no
// incremental write-through for label documents anywhere in the service — so a
// renamed label keeps its old name in search until that full sweep runs.
const reindexNote = "search: run `reindex-catalog --index=catalog_labels` (full sweep; there is no incremental label indexer)"

func main() {
	dsn := flag.String("dsn", "", "catalog DSN — REQUIRED, never inferred from the environment")
	apply := flag.Bool("apply", false, "write (default: dry-run preview)")
	flag.Parse()

	if *dsn == "" {
		slog.Error("heal-label-slash-names", "error", "--dsn is required")
		os.Exit(1)
	}
	if err := run(*dsn, *apply); err != nil {
		slog.Error("heal-label-slash-names", "error", err)
		os.Exit(1)
	}
}

func run(dsn string, apply bool) error {
	db, err := database.OpenJob(dsn)
	if err != nil {
		return fmt.Errorf("connect catalog db: %w", err)
	}
	if sqlDB, e := db.DB(); e == nil {
		defer sqlDB.Close()
	}

	var healed, skipped int
	for _, c := range healCases {
		res, err := applyCase(db, c, apply, os.Stdout)
		if err != nil {
			return fmt.Errorf("label %d: %w", c.LabelID, err)
		}
		if res.skipped {
			skipped++
			continue
		}
		healed++
	}
	verb := "would heal"
	if apply {
		verb = "healed"
	}
	fmt.Fprintf(os.Stdout, "\n[heal-label-slash-names] %s=%d skipped=%d (of %d adjudicated cases)\n",
		verb, healed, skipped, len(healCases))
	if apply && healed > 0 {
		fmt.Fprintf(os.Stdout, "[heal-label-slash-names] %s\n", reindexNote)
	}
	return nil
}
