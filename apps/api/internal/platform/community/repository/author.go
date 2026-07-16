package repository

import (
	"api/internal/platform/community/model"

	"gorm.io/gorm"
)

// AuthorPostRow is one of an author's posts joined with its thread's render
// context (title + anchor). The post columns scan into the embedded model; the
// three thread columns are aliased (thread_*) so they never clash with the
// identically-named community_post columns (id / status / content_rating) the
// join also selects.
type AuthorPostRow struct {
	model.CommunityPost
	ThreadTitle      *string `gorm:"column:thread_title"`
	ThreadAnchorKind int16   `gorm:"column:thread_anchor_kind"`
	ThreadAnchorID   string  `gorm:"column:thread_anchor_id"`
}

// ListAuthorVisiblePosts returns up to limit of an author's VISIBLE posts on a
// site, newest first (post id DESC — the keyset), after the id cursor (0 = from
// the top). anchorKind < 0 lists all kinds; 0-4 narrows to one anchor kind. The
// read is SITE-SCOPED through the thread join, so a caller only ever sees its own
// tenant's posts (a cross-tenant author yields an empty page, never a leak).
func (r *PostRepository) ListAuthorVisiblePosts(site string, authorID, after int64, anchorKind int16, limit int) ([]AuthorPostRow, error) {
	q := r.db.Model(&model.CommunityPost{}).
		Select("community_post.*, community_thread.title AS thread_title, "+
			"community_thread.anchor_kind AS thread_anchor_kind, community_thread.anchor_id AS thread_anchor_id").
		Joins("JOIN community_thread ON community_thread.id = community_post.thread_id").
		Where("community_post.author_id = ? AND community_thread.site = ? AND community_post.status = ?",
			authorID, site, model.PostStatusVisible)
	if after > 0 {
		q = q.Where("community_post.id < ?", after)
	}
	if anchorKind >= 0 {
		q = q.Where("community_thread.anchor_kind = ?", anchorKind)
	}
	var rows []AuthorPostRow
	err := q.Order("community_post.id DESC").Limit(limit).Scan(&rows).Error
	return rows, err
}

// ResolveVisiblePosts returns the VISIBLE posts among the requested ids on a
// site, each joined with its thread's render context (title + anchor) — the
// batch by-id hydration read. The result is UNORDERED (the handler re-orders it
// into request order); a post that is hidden/deleted, belongs to another site,
// or does not exist is simply absent (no existence leak). SITE-SCOPED through the
// thread join, so a caller only ever resolves posts in its own tenant.
func (r *PostRepository) ResolveVisiblePosts(site string, ids []int64) ([]AuthorPostRow, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var rows []AuthorPostRow
	err := r.db.Model(&model.CommunityPost{}).
		Select("community_post.*, community_thread.title AS thread_title, "+
			"community_thread.anchor_kind AS thread_anchor_kind, community_thread.anchor_id AS thread_anchor_id").
		Joins("JOIN community_thread ON community_thread.id = community_post.thread_id").
		Where("community_post.id IN ? AND community_thread.site = ? AND community_post.status = ?",
			ids, site, model.PostStatusVisible).
		Scan(&rows).Error
	return rows, err
}

// CountAuthorVisiblePosts returns, for each requested author, the number of
// VISIBLE posts they have on a site — the batch profile-stats read. Authors with
// no visible posts (or unknown to the site) are simply absent from the map; the
// handler fills them in as 0. SITE-SCOPED through the thread join.
func (r *PostRepository) CountAuthorVisiblePosts(site string, authorIDs []int64) (map[int64]int64, error) {
	out := make(map[int64]int64, len(authorIDs))
	if len(authorIDs) == 0 {
		return out, nil
	}
	type countRow struct {
		AuthorID int64 `gorm:"column:author_id"`
		N        int64 `gorm:"column:n"`
	}
	var rows []countRow
	err := r.db.Model(&model.CommunityPost{}).
		Select("community_post.author_id AS author_id, COUNT(*) AS n").
		Joins("JOIN community_thread ON community_thread.id = community_post.thread_id").
		Where("community_post.author_id IN ? AND community_thread.site = ? AND community_post.status = ?",
			authorIDs, site, model.PostStatusVisible).
		Group("community_post.author_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.AuthorID] = row.N
	}
	return out, nil
}

// PurgeAuthorPostsTx tombstones and scrubs every post an author has on a site:
// status -> deleted and content_raw/content_html emptied, while the post_number
// is PRESERVED (invariant 13: the thread numbering never collapses). Only posts
// not ALREADY in this terminal state are touched — so a self-deleted post (which
// keeps its content) is still scrubbed, and a second purge affects 0 rows
// (idempotent). SITE-SCOPED through the thread join. Returns the rows affected.
func PurgeAuthorPostsTx(tx *gorm.DB, site string, authorID int64) (int64, error) {
	res := tx.Exec(`
		UPDATE community_post AS p
		   SET status = ?, content_raw = '', content_html = ''
		  FROM community_thread AS t
		 WHERE p.thread_id = t.id
		   AND t.site = ?
		   AND p.author_id = ?
		   AND (p.status <> ? OR p.content_raw <> '' OR p.content_html <> '')`,
		model.PostStatusDeleted, site, authorID, model.PostStatusDeleted)
	return res.RowsAffected, res.Error
}

// DeleteAuthorReactionsTx removes every reaction an author (as the reactor) left
// on posts belonging to a site — the author's like footprint. SITE-SCOPED through
// the post -> thread join, so a caller only clears the footprint on its OWN
// tenant. Idempotent: a second run finds nothing to delete. Returns rows deleted.
func DeleteAuthorReactionsTx(tx *gorm.DB, site string, authorID int64) (int64, error) {
	res := tx.Exec(`
		DELETE FROM community_reaction AS r
		 USING community_post AS p, community_thread AS t
		 WHERE r.post_id = p.id
		   AND p.thread_id = t.id
		   AND t.site = ?
		   AND r.user_id = ?`,
		site, authorID)
	return res.RowsAffected, res.Error
}
