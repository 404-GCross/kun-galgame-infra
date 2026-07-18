package editspec

import (
	"context"

	"api/internal/platform/editing"
	"api/internal/platform/galgame/model"

	"gorm.io/gorm"
)

// buildOnMerge builds galgame.game's post-commit OnMerge hook (charter pillar 1
// — the single write path: direct edit, reviewer merge, and revert all fire
// it). It performs the two side effects the old per-handler write paths owned
// but the engine merge path (kungal BFF → catalog edit face → engine) bypassed:
//
//   - contributor recording — the merged revision's actor (∪ amender) join the
//     galgame's contributor credit. This retires the scattered, forget-prone
//     ensureContributor: every merge path, including the BFF and a future /v1
//     writer, records contributors for free (the exact E3a/E3b gap). The
//     galgame_contributor TABLE is retained rather than reduced to a pure
//     edit_revision projection: 61% of the existing contributor credit predates
//     the revision log (original-wiki history the log cannot reconstruct — see
//     the E3b-tail reconciliation), so the table stays the source of truth and
//     this keeps it current going forward.
//   - search write-through — reindex the galgame in Meilisearch so an engine-
//     path edit is searchable immediately (the 沙耶断章 vs 咲夜叙 staleness class).
//
// reindex is the injected Meilisearch write-through (search.Hook.Galgame at the
// assembly point; nil in tests / when search is unconfigured). db is the
// galgame pool. Both effects are best-effort (the engine fires this OUTSIDE the
// merge transaction and only warns on error), so neither can undo a landed
// merge; a miss is recoverable (reindex-search / cmd/reconcile-contributors).
func buildOnMerge(db *gorm.DB, reindex func(entityID int)) func(context.Context, editing.MergeEvent) error {
	return func(ctx context.Context, ev editing.MergeEvent) error {
		uids := []int64{ev.ActorUID}
		if ev.AmenderUID != nil {
			uids = append(uids, *ev.AmenderUID)
		}
		// Record contributors first, then reindex regardless of its outcome —
		// the two effects are independent, and a contributor error must not
		// suppress the (more user-visible) search refresh.
		err := recordContributors(ctx, db, ev.EntityID, uids...)
		if reindex != nil {
			reindex(int(ev.EntityID))
		}
		return err
	}
}

// recordContributors idempotently credits uids as contributors of galgame gid,
// skipping non-positive / system uids. Mirrors the retired ensureContributor's
// check-then-insert — galgame_contributor carries no unique index and holds no
// duplicate (galgame, user) rows.
func recordContributors(ctx context.Context, db *gorm.DB, gid int64, uids ...int64) error {
	for _, uid := range uids {
		if uid <= 0 {
			continue
		}
		var n int64
		if err := db.WithContext(ctx).Model(&model.GalgameContributor{}).
			Where("galgame_id = ? AND user_id = ?", gid, uid).Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		if err := db.WithContext(ctx).Create(&model.GalgameContributor{
			GalgameID: int(gid), UserID: int(uid),
		}).Error; err != nil {
			return err
		}
	}
	return nil
}
