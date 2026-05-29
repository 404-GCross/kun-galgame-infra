package model

import "time"

// Reason values for a moemoepoint adjustment. A small, stable, generic set
// owned by OAuth — specifics are distinguished by SourceApp + Ref, NOT by
// per-app reason enums (see docs/integration/oauth/06-moemoepoint.md §2).
const (
	MoemoepointReasonAdminGrant      = "admin_grant"
	MoemoepointReasonAdminDeduct     = "admin_deduct"
	MoemoepointReasonMigration       = "migration"
	MoemoepointReasonContentApproved = "content_approved"
	MoemoepointReasonContentRemoved  = "content_removed"
	MoemoepointReasonDailyCheckin    = "daily_checkin"
	MoemoepointReasonLiked           = "liked"
	// RegisterGift is the one-off welcome grant on account creation
	// ("鲲给予你的第一份礼物"). OAuth-internal like admin_*/migration — a
	// downstream s2s client must not self-label it.
	MoemoepointReasonRegisterGift = "register_gift"
)

// IsValidMoemoepointReason reports whether r is an accepted reason.
func IsValidMoemoepointReason(r string) bool {
	switch r {
	case MoemoepointReasonAdminGrant, MoemoepointReasonAdminDeduct,
		MoemoepointReasonMigration, MoemoepointReasonContentApproved,
		MoemoepointReasonContentRemoved, MoemoepointReasonDailyCheckin,
		MoemoepointReasonLiked, MoemoepointReasonRegisterGift:
		return true
	}
	return false
}

// MoemoepointLog is the append-only audit trail for every change to a user's
// moemoepoint balance. Rows are NEVER updated or deleted — a correction is a
// new compensating row (negative delta). The mutable balance lives on
// users.moemoepoint and is updated in the SAME transaction as each insert
// here (see service.MoemoepointService.Adjust).
type MoemoepointLog struct {
	ID     int64 `gorm:"primaryKey" json:"id"`
	UserID uint  `gorm:"not null;index" json:"user_id"`
	// Delta is signed and non-zero (+ award / − deduct).
	Delta  int    `gorm:"not null" json:"delta"`
	Reason string `gorm:"size:40;not null;index" json:"reason"`
	// SourceApp is the originating site, derived server-side from the
	// authenticated OAuth client (not trusted from the request body).
	SourceApp string `gorm:"size:32;not null" json:"source_app"`
	// Ref is the free-form triggering entity, e.g. "galgame:1207" (optional).
	Ref string `gorm:"size:80;default:''" json:"ref"`
	// ActorUserID is who caused it: 0 = system, else admin/user id.
	ActorUserID uint `gorm:"not null;default:0" json:"actor_user_id"`
	// IdempotencyKey dedupes retries / cron replays — unique.
	IdempotencyKey string    `gorm:"size:128;not null;uniqueIndex" json:"-"`
	Note           string    `gorm:"size:255;default:''" json:"note"`
	CreatedAt      time.Time `json:"created_at"`
}

// TableName returns the table name for MoemoepointLog.
func (MoemoepointLog) TableName() string {
	return "moemoepoint_log"
}
