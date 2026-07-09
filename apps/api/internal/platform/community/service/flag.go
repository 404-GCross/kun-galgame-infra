package service

import (
	"context"

	"api/internal/platform/community/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// FlagService owns report submission (the embed capability, invariant 11). Only
// SUBMISSION lives here: reputation weighting, auto-hide-on-threshold, and the
// centralized review queue are the trust engine's job (step 04). The weight is
// a placeholder base value that step 04 recomputes from the reporter's trust.
type FlagService struct{ db *gorm.DB }

func NewFlagService(db *gorm.DB) *FlagService { return &FlagService{db: db} }

const baseFlagWeight float32 = 1.0

// Submit records a report on a post. Idempotent per (post, flagger): a repeat
// report by the same user is a no-op (ON CONFLICT DO NOTHING on the unique).
func (s *FlagService) Submit(ctx context.Context, postID, flaggerID int64, reason *int16, note *string) error {
	flag := model.CommunityFlag{
		PostID: postID, FlaggerID: flaggerID, Reason: reason, Note: note,
		Weight: baseFlagWeight, Status: model.FlagStatusPending,
	}
	return s.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&flag).Error
}
