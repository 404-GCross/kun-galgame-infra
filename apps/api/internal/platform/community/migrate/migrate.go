// Package migrate owns the kun_community schema migration: the AutoMigrate
// table list plus the idempotent raw-SQL section for the indexes AutoMigrate
// cannot express (partial-unique and DESC-ordered composites). It is called by
// cmd/migrate-community and by the migrate integration test (which provisions
// its test database with the exact production schema).
package migrate

import (
	"fmt"

	"api/internal/platform/community/model"

	"gorm.io/gorm"
)

// Run applies the full community schema (tables + raw SQL). Idempotent: safe to
// run on every deploy and repeatedly against the same database. This step has
// NO seeds — the per-site default board is planted at letmoe cut-over time.
func Run(db *gorm.DB) error {
	if err := db.AutoMigrate(
		// community_thread first: community_post's thread_id FK and the thread
		// self-reference (merged_into_id) both point at it.
		&model.CommunityThread{},
		&model.CommunityPost{},
		&model.CommunityReaction{},
		&model.CommunityThreadUser{},
		&model.CommunityBoard{},
		&model.CommunityTrust{},
		&model.CommunityFlag{},
		&model.CommunityReviewItem{},
	); err != nil {
		return fmt.Errorf("community automigrate: %w", err)
	}
	return rawSQL(db)
}

// rawSQL is the post-AutoMigrate section: the indexes AutoMigrate cannot
// express (partial predicate / DESC sort). Every statement is idempotent
// (CREATE INDEX IF NOT EXISTS), so this section reruns freely.
func rawSQL(db *gorm.DB) error {
	for _, ix := range []struct{ name, stmt string }{
		// Invariant 4: at most ONE live comments thread per anchor. Partial —
		// deleted threads (status=3) drop out so a fresh comments thread can be
		// opened for the same anchor (the tombstone-rebuild path). topic and
		// feedback (kind 0/2) are unconstrained.
		{"uq_community_thread_anchor_comments", `
			CREATE UNIQUE INDEX IF NOT EXISTS uq_community_thread_anchor_comments
			    ON community_thread(anchor_kind, anchor_id)
			    WHERE kind = 1 AND status <> 3`},
		// Invariant 7 (tenant-first): the in-site thread list, ordered by
		// recency. site leads; last_posted_at DESC serves the newest-first read.
		{"idx_community_thread_site_list", `
			CREATE INDEX IF NOT EXISTS idx_community_thread_site_list
			    ON community_thread(site, kind, last_posted_at DESC)`},
		// Invariant 7 (anchor dimension): per-anchor aggregation across sites
		// (the NextMoe aggregate read). Both the (site, …) and (anchor, …)
		// dimensions are queryable.
		{"idx_community_thread_anchor", `
			CREATE INDEX IF NOT EXISTS idx_community_thread_anchor
			    ON community_thread(anchor_kind, anchor_id, kind)`},
		// User footprint (NextMoe profile): a user's posts newest-first. DESC
		// sort, so it lives here rather than a struct tag.
		{"idx_community_post_author", `
			CREATE INDEX IF NOT EXISTS idx_community_post_author
			    ON community_post(author_id, created_at DESC)`},
	} {
		if err := db.Exec(ix.stmt).Error; err != nil {
			return fmt.Errorf("create index %s: %w", ix.name, err)
		}
	}
	return nil
}
