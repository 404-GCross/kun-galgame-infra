package service

import (
	"context"
	"log/slog"

	"api/internal/platform/community/repository"

	"gorm.io/gorm"
)

// Author-scoped reads and the compliance purge (step 10: the infra face for
// forum's 06a retirement wave — a profile posts tab, batch user stats, and a
// GDPR-style author purge). These hang off PostService because they are all
// community_post operations; the purge additionally clears the author's
// reactions in the same transaction. Every one is SITE-SCOPED at the repository
// layer (the thread join), so a client bound to site A can never read or purge
// site B's authors.

// PurgeResult is the effect of a compliance purge.
type PurgeResult struct {
	PostsPurged      int64
	ReactionsDeleted int64
}

// ListAuthorPosts returns a page of an author's VISIBLE posts on a site with the
// thread render context, newest first (keyset by post id; after = 0 from the
// top). The handler clamps limit and owns the cursor.
func (s *PostService) ListAuthorPosts(site string, authorID, after int64, anchorKind int16, limit int) ([]repository.AuthorPostRow, error) {
	return s.posts.ListAuthorVisiblePosts(site, authorID, after, anchorKind, limit)
}

// AuthorStats returns each requested author's visible-post count on a site,
// keyed by author id (absent = 0 visible posts).
func (s *PostService) AuthorStats(site string, authorIDs []int64) (map[int64]int64, error) {
	return s.posts.CountAuthorVisiblePosts(site, authorIDs)
}

// ResolvePosts returns the VISIBLE posts among the requested ids on a site, each
// with its thread render context — the batch by-id hydration read (step 11: the
// forum like-tab cutover). Site-scoped at the repository layer; the handler owns the
// dedupe and request-order re-projection. Absent = hidden/deleted/cross-site/
// unknown (no existence leak).
func (s *PostService) ResolvePosts(site string, ids []int64) ([]repository.AuthorPostRow, error) {
	return s.posts.ResolveVisiblePosts(site, ids)
}

// PurgeAuthor is the compliance purge: in one transaction it tombstones + scrubs
// every post the author has on the site (any status, post_number preserved —
// invariant 13) and deletes every reaction the author left on the site. It emits
// NO events and calls NO trust callbacks (a purge is an operator/legal action,
// not community activity) and is idempotent — a second run reports {0, 0}. One
// slog audit line records the effect.
func (s *PostService) PurgeAuthor(ctx context.Context, site string, authorID int64) (PurgeResult, error) {
	var res PurgeResult
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		posts, err := repository.PurgeAuthorPostsTx(tx, site, authorID)
		if err != nil {
			return err
		}
		reactions, err := repository.DeleteAuthorReactionsTx(tx, site, authorID)
		if err != nil {
			return err
		}
		res = PurgeResult{PostsPurged: posts, ReactionsDeleted: reactions}
		return nil
	})
	if err != nil {
		return PurgeResult{}, err
	}
	slog.Info("community author purge", "site", site, "author_id", authorID,
		"posts_purged", res.PostsPurged, "reactions_deleted", res.ReactionsDeleted)
	return res, nil
}
