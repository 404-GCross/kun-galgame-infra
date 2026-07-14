package service

import (
	"context"

	"api/internal/platform/community/model"
	"api/internal/platform/community/repository"

	"gorm.io/gorm"
)

// ReactionService owns post reactions.
type ReactionService struct{ db *gorm.DB }

func NewReactionService(db *gorm.DB) *ReactionService { return &ReactionService{db: db} }

// Toggle flips a user's reaction on a post: adds it when absent, removes it when
// present. Returns whether the reaction now exists plus the post's context
// (author + thread/anchor) — the reaction flow resolves the post row anyway for
// the trust tallies, and returning that context lets the consuming site fan out
// its like notification (author = recipient, anchor = the jump target) without a
// second round trip (docs/proj/17 decision 2). Idempotent per (post, user, kind).
// Toggling maintains the like tallies on community_trust in the same tx (doc 11
// §6: likes_given/received counted in place by the reaction flow) — the
// reactor's likes_given and the post author's likes_received move together,
// floored at 0 on un-react.
func (s *ReactionService) Toggle(ctx context.Context, postID, userID int64, kind int16) (bool, repository.PostContext, error) {
	var added bool
	var pc repository.PostContext
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Load the post context (author/site/anchor) FIRST so the tenant guard runs
		// before any mutation: a cross-tenant caller is answered as "post not
		// found" and never toggles a reaction on another site's post (ruling 4).
		// The context is invariant under a reaction toggle, so loading it up front
		// is equivalent to the previous post-toggle read — no extra query.
		loaded, found, err := repository.PostContextTx(tx, postID)
		if err != nil {
			return err
		}
		if !found {
			return ErrPostNotFound
		}
		if crossTenantCtx(ctx, loaded.Site, loaded.AnchorKind) {
			return ErrPostNotFound
		}
		pc = loaded
		a, err := repository.ToggleReactionTx(tx, postID, userID, kind)
		if err != nil {
			return err
		}
		added = a
		// Only "like" reactions feed the trust like counters.
		if kind != model.ReactionKindLike {
			return nil
		}
		delta := int32(1)
		if !added {
			delta = -1
		}
		if _, err := repository.GetOrCreateTrustTx(tx, userID); err != nil {
			return err
		}
		if err := repository.AdjustLikesTx(tx, userID, delta, 0); err != nil {
			return err
		}
		if pc.AuthorID != userID {
			if _, err := repository.GetOrCreateTrustTx(tx, pc.AuthorID); err != nil {
				return err
			}
		}
		return repository.AdjustLikesTx(tx, pc.AuthorID, 0, delta)
	})
	return added, pc, err
}
