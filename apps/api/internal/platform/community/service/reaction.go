package service

import (
	"context"

	"api/internal/platform/community/repository"

	"gorm.io/gorm"
)

// ReactionService owns post reactions.
type ReactionService struct{ db *gorm.DB }

func NewReactionService(db *gorm.DB) *ReactionService { return &ReactionService{db: db} }

// Toggle flips a user's reaction on a post: adds it when absent, removes it when
// present. Returns true when the reaction now exists. Idempotent per
// (post, user, kind). Reaction tallies live on community_trust (likes_received)
// and are maintained by the trust engine (step 04), not here.
func (s *ReactionService) Toggle(ctx context.Context, postID, userID int64, kind int16) (bool, error) {
	var added bool
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		a, err := repository.ToggleReactionTx(tx, postID, userID, kind)
		added = a
		return err
	})
	return added, err
}
