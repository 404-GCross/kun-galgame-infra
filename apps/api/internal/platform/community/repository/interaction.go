package repository

import (
	"api/internal/platform/community/model"

	"gorm.io/gorm"
)

// TrustRepository reads community_trust.
type TrustRepository struct{ db *gorm.DB }

func NewTrustRepository(db *gorm.DB) *TrustRepository { return &TrustRepository{db: db} }

// GetTrust returns the user's trust row, or nil when none exists yet (which the
// caller treats as TL0 — the sandbox default).
func (r *TrustRepository) GetTrust(userID int64) (*model.CommunityTrust, error) {
	var t model.CommunityTrust
	err := r.db.Where("user_id = ?", userID).First(&t).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// GetOrCreateTrustTx returns the user's trust row, creating a fresh TL0 row
// (level=0, first_posts_held_remaining=2 via the DDL default) when absent. The
// trust ENGINE (promotion metering, starter boosts) is step 04; here the
// sandbox only needs the row to exist so its hold counter can persist.
func GetOrCreateTrustTx(tx *gorm.DB, userID int64) (*model.CommunityTrust, error) {
	var t model.CommunityTrust
	err := tx.Where("user_id = ?", userID).First(&t).Error
	if err == nil {
		return &t, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, err
	}
	t = model.CommunityTrust{UserID: userID, Level: model.TrustLevelNew}
	if err := tx.Create(&t).Error; err != nil {
		// Lost a race with a sibling tx: the row now exists — re-read it.
		if reread := tx.Where("user_id = ?", userID).First(&t).Error; reread == nil {
			return &t, nil
		}
		return nil, err
	}
	return &t, nil
}

// DecrementHoldTx spends one first-post hold (approve_post_count model). The
// WHERE guard makes it a no-op once the counter reaches 0, so it is safe to
// call unconditionally after holding a post.
func DecrementHoldTx(tx *gorm.DB, userID int64) error {
	return tx.Model(&model.CommunityTrust{}).
		Where("user_id = ? AND first_posts_held_remaining > 0", userID).
		UpdateColumn("first_posts_held_remaining", gorm.Expr("first_posts_held_remaining - 1")).Error
}

// ToggleReactionTx flips a reaction: inserts it when absent, deletes it when
// present. Returns true when the reaction now exists (added), false when it was
// removed. Idempotent per (post, user, kind).
func ToggleReactionTx(tx *gorm.DB, postID, userID int64, kind int16) (bool, error) {
	res := tx.Where("post_id = ? AND user_id = ? AND kind = ?", postID, userID, kind).
		Delete(&model.CommunityReaction{})
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected > 0 {
		return false, nil // existed → removed
	}
	if err := tx.Create(&model.CommunityReaction{PostID: postID, UserID: userID, Kind: kind}).Error; err != nil {
		return false, err
	}
	return true, nil
}
