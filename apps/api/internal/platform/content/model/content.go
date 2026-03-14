package model

import (
	"time"

	"gorm.io/gorm"
)

// Content represents a content item (article, post, etc.)
type Content struct {
	ID               uint           `gorm:"primaryKey" json:"id"`
	UUID             string         `gorm:"type:uuid;uniqueIndex;default:gen_random_uuid()" json:"uuid"`
	SiteID           uint           `gorm:"not null;index" json:"site_id"`
	UserUUID         string         `gorm:"size:36;not null;index" json:"user_uuid"`
	Title            string         `gorm:"size:255;not null" json:"title"`
	Body             string         `gorm:"type:text" json:"body"`
	Status           int            `gorm:"default:0" json:"status"`      // 0: draft, 1: published, 2: pending
	ModerationStatus int            `gorm:"default:0" json:"mod_status"` // 0: pending, 1: approved, 2: rejected
	ViewCount        int            `gorm:"default:0" json:"view_count"`
	LikeCount        int            `gorm:"default:0" json:"like_count"`
	CommentCount     int            `gorm:"default:0" json:"comment_count"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
	DeletedAt        gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName returns the table name for Content
func (Content) TableName() string {
	return "contents"
}

// IsPublished checks if the content is published
func (c *Content) IsPublished() bool {
	return c.Status == 1
}

// IsPendingModeration checks if the content is pending moderation
func (c *Content) IsPendingModeration() bool {
	return c.ModerationStatus == 0
}
