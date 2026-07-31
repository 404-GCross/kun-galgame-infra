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
// What is NOT here yet, and why the hook is the place for it: a single-work
// search reindex and a claim-projection refresh. The catalog works index is
// rebuilt by a daily cron today — the public spec states that freshness
// contract in as many words — and claim_state is moved by the projector, not by
// a field edit. Both become write-throughs with the N2 lifecycle wave, and this
// is the one seam they hang off, instead of four endpoints each remembering.
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
