package model

import (
	"time"

	"gorm.io/datatypes"
)

const (
	CreatorAppPending  = "pending"
	CreatorAppApproved = "approved"
	CreatorAppDeclined = "declined"
)

type CreatorApplication struct {
	ID            uint           `gorm:"primaryKey" json:"id"`
	UserID        uint           `gorm:"not null;index" json:"user_id"`
	Source        string         `gorm:"size:16;not null" json:"source"`
	Status        string         `gorm:"size:16;not null;default:'pending'" json:"status"`
	Evidence      datatypes.JSON `gorm:"type:jsonb" json:"evidence,omitempty"`
	Message       string         `gorm:"type:text;default:''" json:"message"`
	ReviewerID    *uint          `gorm:"column:reviewer_id" json:"reviewer_id,omitempty"`
	ReviewedAt    *time.Time     `json:"reviewed_at,omitempty"`
	DeclineReason string         `gorm:"type:text;default:''" json:"decline_reason"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

func (CreatorApplication) TableName() string { return "creator_applications" }
