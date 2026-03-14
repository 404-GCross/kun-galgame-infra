package model

import (
	"time"

	"gorm.io/gorm"
)

// Comment represents a comment on content
type Comment struct {
	ID               uint           `gorm:"primaryKey" json:"id"`
	UUID             string         `gorm:"type:uuid;uniqueIndex;default:gen_random_uuid()" json:"uuid"`
	ContentUUID      string         `gorm:"size:36;not null;index" json:"content_uuid"`
	UserUUID         string         `gorm:"size:36;not null;index" json:"user_uuid"`
	ParentID         *uint          `gorm:"index" json:"parent_id,omitempty"`
	Body             string         `gorm:"type:text;not null" json:"body"`
	Status           int            `gorm:"default:1" json:"status"`      // 0: hidden, 1: visible
	ModerationStatus int            `gorm:"default:0" json:"mod_status"` // 0: pending, 1: approved, 2: rejected
	LikeCount        int            `gorm:"default:0" json:"like_count"`
	ReplyCount       int            `gorm:"default:0" json:"reply_count"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
	DeletedAt        gorm.DeletedAt `gorm:"index" json:"-"`

	// Relations
	Replies []Comment `gorm:"foreignKey:ParentID" json:"replies,omitempty"`
}

// TableName returns the table name for Comment
func (Comment) TableName() string {
	return "comments"
}

// IsVisible checks if the comment is visible
func (c *Comment) IsVisible() bool {
	return c.Status == 1
}
