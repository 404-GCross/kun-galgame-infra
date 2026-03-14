package model

import (
	"time"
)

// UserFollow represents a follow relationship between users
type UserFollow struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	FollowerID  uint      `gorm:"not null;index" json:"follower_id"`
	FollowingID uint      `gorm:"not null;index" json:"following_id"`
	CreatedAt   time.Time `json:"created_at"`

	// Relations
	Follower  User `gorm:"foreignKey:FollowerID;constraint:OnDelete:CASCADE" json:"-"`
	Following User `gorm:"foreignKey:FollowingID;constraint:OnDelete:CASCADE" json:"-"`
}

// TableName returns the table name for UserFollow
func (UserFollow) TableName() string {
	return "user_follows"
}

// Indexes returns the indexes for UserFollow
func (UserFollow) Indexes() []string {
	return []string{
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_user_follows_unique ON user_follows(follower_id, following_id)",
	}
}
