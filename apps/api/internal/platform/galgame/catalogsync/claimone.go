// claimone.go — the WRITE-PATH claim lane (wave 146).
//
// The failure it closes: a galgame published on the wiki had no catalog
// identity until the 03:20 reconcile-galgame-claims job ran, so for up to 24h
// GET /v1/catalog/works/{id} and every anchor lookup answered 404 for a work
// the wiki was already serving (galgame 64729 was the reported instance). The
// nightly job stays exactly as it is and becomes the RECONCILIATION SAFETY NET:
// it still owns the backlog, the drafts, and anything a write path dropped.
//
// No registration logic lives here either. ClaimOne runs the SAME Reconciler
// claim phase the nightly job runs, narrowed to one wiki id
// (Options.WritePathIDs), so the two lanes cannot drift: anchor grading, the
// negative-knowledge gate, adoption, the galgame.catalog_work_id write-back and
// the claim_state projection are all the code in claim.go.
//
// Two structural facts a caller must respect:
//
//   - The galgame pool and the catalog pool are two pools over one database, so
//     this CANNOT join the product write transaction. It runs after that commit,
//     is idempotent, and its failure must never fail the publication — hence
//     Hook, which swallows the error into a warning and leaves the row to the
//     nightly net.
//   - Search freshness is a separate concern (the daily reindex): an anchor
//     makes a work reachable by id and by lookup immediately, which is what the
//     404 was about.
package catalogsync

import (
	"context"
	"log/slog"

	"gorm.io/gorm"
)

// ClaimOne registers ONE wiki galgame into the catalog work registry and
// re-projects its claim_state, returning the same stats shape the bulk phase
// reports. Idempotent: calling it on an already-claimed, already-projected row
// writes nothing.
//
// Rows the write-path lane deliberately does nothing for (see loadGames): an
// unclaimed galgame that is not published. Those are the nightly job's, so a
// withdrawn submission never leaves an orphan registry row behind.
func ClaimOne(ctx context.Context, wiki, catalog *gorm.DB, galgameID int64) (ClaimStats, error) {
	r := New(wiki, catalog, nil, Options{
		Phase:        PhaseClaim,
		WritePathIDs: []int64{galgameID},
	})
	stats, err := r.Run(ctx, PhaseClaim)
	return stats.Claim, err
}

// Hook adapts ClaimOne to the fire-and-log shape the galgame write paths take
// their post-commit side effects in (the same posture as the search
// write-through): it never returns an error, because a registry hiccup must not
// turn a landed publication into a failed request. A dropped claim is picked up
// by the nightly reconcile within 24h — the pre-146 latency, as the floor
// instead of the norm — and the warning is what tells an operator it happened.
func Hook(wiki, catalog *gorm.DB) func(ctx context.Context, galgameID int64) {
	return func(ctx context.Context, galgameID int64) {
		stats, err := ClaimOne(ctx, wiki, catalog, galgameID)
		if err != nil {
			slog.Warn("catalogsync: write-path claim failed (nightly reconcile will retry)",
				"galgame_id", galgameID, "err", err)
			return
		}
		if stats.ClaimedNew > 0 || stats.ClaimStateWritten > 0 {
			slog.Info("catalogsync: write-path claim",
				"galgame_id", galgameID, "claimed_new", stats.ClaimedNew,
				"claim_state_written", stats.ClaimStateWritten)
		}
	}
}
