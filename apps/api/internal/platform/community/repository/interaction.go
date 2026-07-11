package repository

import (
	"time"

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

// GetTrustTx reads a trust row inside a tx (nil when absent).
func GetTrustTx(tx *gorm.DB, userID int64) (*model.CommunityTrust, error) {
	var t model.CommunityTrust
	err := tx.Where("user_id = ?", userID).First(&t).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// MeteringDelta is one activity receipt's increments to the reading-behavior
// counters. The columns are nullable (doc 11 §4.3 sketch), so every increment
// COALESCEs NULL to 0.
type MeteringDelta struct {
	TopicsEntered int32
	PostsRead     int32
	ReadTimeS     int32
	DaysVisited   int32
}

// ApplyMeteringTx adds a receipt's deltas to the trust counters.
func ApplyMeteringTx(tx *gorm.DB, userID int64, d MeteringDelta) error {
	return tx.Model(&model.CommunityTrust{}).Where("user_id = ?", userID).Updates(map[string]any{
		"topics_entered": gorm.Expr("COALESCE(topics_entered, 0) + ?", d.TopicsEntered),
		"posts_read":     gorm.Expr("COALESCE(posts_read, 0) + ?", d.PostsRead),
		"read_time_s":    gorm.Expr("COALESCE(read_time_s, 0) + ?", d.ReadTimeS),
		"days_visited":   gorm.Expr("COALESCE(days_visited, 0) + ?", d.DaysVisited),
		"updated_at":     time.Now(),
	}).Error
}

// AdjustLikesTx nudges a user's like counters (given/received), flooring at 0 so
// an un-like never drives a counter negative. Called by the reaction flow.
func AdjustLikesTx(tx *gorm.DB, userID int64, given, received int32) error {
	return tx.Model(&model.CommunityTrust{}).Where("user_id = ?", userID).Updates(map[string]any{
		"likes_given":    gorm.Expr("GREATEST(COALESCE(likes_given, 0) + ?, 0)", given),
		"likes_received": gorm.Expr("GREATEST(COALESCE(likes_received, 0) + ?, 0)", received),
		"updated_at":     time.Now(),
	}).Error
}

// SetLevelTx sets a user's trust level (promotion/demotion).
func SetLevelTx(tx *gorm.DB, userID int64, level int16) error {
	return tx.Model(&model.CommunityTrust{}).Where("user_id = ?", userID).
		Updates(map[string]any{"level": level, "updated_at": time.Now()}).Error
}

// SetBoostTx records the starter-boost origin (granted_boost).
func SetBoostTx(tx *gorm.DB, userID int64, boost int16) error {
	return tx.Model(&model.CommunityTrust{}).Where("user_id = ?", userID).
		Updates(map[string]any{"granted_boost": boost, "updated_at": time.Now()}).Error
}

// IncFlagAccuracyTx backfills a reporter's historical accuracy after a queue
// decision: flags_agreed++ (report upheld) or flags_disagreed++ (report rejected).
func IncFlagAccuracyTx(tx *gorm.DB, userID int64, agreed bool) error {
	col := "flags_disagreed"
	if agreed {
		col = "flags_agreed"
	}
	return tx.Model(&model.CommunityTrust{}).Where("user_id = ?", userID).
		Update(col, gorm.Expr("COALESCE("+col+", 0) + 1")).Error
}

// DecrementHoldTx spends one first-post hold (approve_post_count model). The
// WHERE guard makes it a no-op once the counter reaches 0, so it is safe to
// call unconditionally after holding a post.
func DecrementHoldTx(tx *gorm.DB, userID int64) error {
	return tx.Model(&model.CommunityTrust{}).
		Where("user_id = ? AND first_posts_held_remaining > 0", userID).
		UpdateColumn("first_posts_held_remaining", gorm.Expr("first_posts_held_remaining - 1")).Error
}

// ClearHoldsTx zeroes the first-post hold counter. Used by the staff starter
// boost (docs/proj/17 decision 4): staff content is never held for review, so
// their remaining holds are consumed outright when the boost lands.
func ClearHoldsTx(tx *gorm.DB, userID int64) error {
	return tx.Model(&model.CommunityTrust{}).Where("user_id = ?", userID).
		UpdateColumn("first_posts_held_remaining", 0).Error
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
