package editspec

import (
	"context"

	"api/internal/platform/catalog/repository"
	"api/internal/platform/editing"

	"gorm.io/gorm"
)

// buildWorkOnMerge builds catalog.work's post-commit hook — the gap wave 149's
// survey recorded: catalog.work was the one registered type WITHOUT an OnMerge,
// so a catalog-native edit landed in the tables and nothing downstream noticed.
//
// It fires on the single write path (direct edit, reviewer merge, revert
// alike), outside the merge transaction, best-effort by the engine's contract —
// so it can never undo a landed merge, and a miss is recoverable.
//
// It TOUCHES the work row, which is the effect that must exist rather than be
// nice to have: the public changes feed walks catalog_work on an
// (updated_at, id) keyset, and most of the fields registered in wave 154 write
// CHILD tables only — an intro, a tag edge, a cover. Without the touch the feed
// is blind to exactly the edits this face exists to make, and every downstream
// mirror keeps serving the pre-edit state until some unrelated write happens to
// move the watermark.
//
// The two write-throughs wave 154 left for N2, and what became of them:
//
//   - CLAIM PROJECTION: resolved, and it belongs nowhere near this hook. Wave
//     155 moved claim_state onto semantic lifecycle actions that write the
//     column and its event row in one transaction, and the field matrix has no
//     `status` key by design (03 定案 §2) — so no field edit can move a claim,
//     and there is nothing here to refresh.
//   - SINGLE-WORK REINDEX: NOT built, deliberately. The catalog works index is
//     produced by cmd/reindex-catalog as a whole-corpus batch — it preloads a
//     popularity signal, the source map, every title, the facet arrays and the
//     intro buckets before emitting a document — and there is no single-document
//     upsert path to reuse. Writing one here would mean a second, divergent
//     definition of what a works document contains, which is the failure the
//     wave-155 task book forbids ("no self-made index write path"). The index
//     therefore stays on its daily cron, which is the freshness contract the
//     public spec already states, and a same-day search reflection needs a
//     single-document builder extracted from the batch first — a wave of its
//     own, on the search package rather than on this hook.
//
// Deliberately NOT here: contributor credit. galgame.game records contributors
// in its OnMerge because galgame_contributor is its own table with 61% of the
// credit predating the revision log. The catalog has no such table — its
// catalog_credit is authorship credit ("who performed a role in this work"),
// a completely different statement from "who edited this page" — and inventing
// an edit-credit table here would be modelling a product feature in the
// registry. Contribution history is edit_revision, which is already complete.
func buildWorkOnMerge(db *gorm.DB) func(context.Context, editing.MergeEvent) error {
	return func(ctx context.Context, ev editing.MergeEvent) error {
		return repository.TouchWorks(ctx, db, []int64{ev.EntityID})
	}
}
