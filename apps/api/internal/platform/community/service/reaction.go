package service

import (
	"context"

	"api/internal/platform/community/model"
	"api/internal/platform/community/repository"

	"gorm.io/gorm"
)

type ReactionService struct{ db *gorm.DB }

func NewReactionService(db *gorm.DB) *ReactionService { return &ReactionService{db: db} }

func (s *ReactionService) Toggle(ctx context.Context, postID, userID int64, kind int16) (bool, repository.PostContext, error) {
	var added bool
	var pc repository.PostContext
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
