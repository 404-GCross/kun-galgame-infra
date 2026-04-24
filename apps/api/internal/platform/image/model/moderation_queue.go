package model

import "time"

// ModerationQueue entries are inserted after a successful upload and
// consumed by the async moderation worker. Rows are deleted after the
// moderation provider returns a decision (the decision is written to
// Image.ReviewStatus + Image.ReviewLabels).
//
// Use SELECT ... FOR UPDATE SKIP LOCKED to safely consume from multiple
// workers (Postgres-native work queue pattern).
type ModerationQueue struct {
	ID         int64     `gorm:"primaryKey" json:"id"`
	Hash       string    `gorm:"type:char(64);not null;index" json:"hash"`
	Site       string    `gorm:"size:32;not null" json:"site"`
	Attempts   int       `gorm:"not null;default:0" json:"attempts"`
	LastError  string    `gorm:"type:text" json:"last_error,omitempty"`
	EnqueuedAt time.Time `gorm:"not null;default:now();index" json:"enqueued_at"`
	PickupAt   *time.Time `gorm:"index" json:"pickup_at,omitempty"`
}

func (ModerationQueue) TableName() string {
	return "image_moderation_queue"
}
