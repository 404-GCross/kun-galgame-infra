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
// present. Returns true when the reaction now exists. Idempotent per
// (post, user, kind). Toggling maintains the like tallies on community_trust in
// the same tx (doc 11 §6: likes_given/received counted in place by the reaction
// flow) — the reactor's likes_given and the post author's likes_received move
// together, floored at 0 on un-react.
func (s *ReactionService) Toggle(ctx context.Context, postID, userID int64, kind int16) (bool, error) {
	var added bool
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		a, err := repository.ToggleReactionTx(tx, postID, userID, kind)
		if err != nil {
			return err
		}
		added = a
		// Only "like" reactions feed the trust like counters.
		if kind != model.ReactionKindLike {
			return nil
		}
		pc, found, err := repository.PostContextTx(tx, postID)
		if err != nil {
			return err
		}
		if !found {
			return ErrPostNotFound
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
	return added, err
}
