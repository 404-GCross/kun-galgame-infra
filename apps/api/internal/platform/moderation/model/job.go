package model

import (
	"time"

	"gorm.io/datatypes"
)

// ContentType represents the type of content being moderated
type ContentType string

const (
	ContentTypeText  ContentType = "text"
	ContentTypeImage ContentType = "image"
)

// Decision represents the moderation decision
type Decision string

const (
	DecisionPending      Decision = "pending"
	DecisionApproved     Decision = "approved"
	DecisionRejected     Decision = "rejected"
	DecisionManualReview Decision = "manual_review"
)

// Job represents a moderation job
type Job struct {
	ID            uint           `gorm:"primaryKey" json:"id"`
	UUID          string         `gorm:"type:uuid;uniqueIndex;default:gen_random_uuid()" json:"uuid"`
	ContentType   ContentType    `gorm:"size:20;not null" json:"content_type"`
	ContentID     string         `gorm:"size:36;not null;index" json:"content_id"`
	ContentSource string         `gorm:"size:50;not null" json:"content_source"` // comment, content, user_avatar
	Status        string         `gorm:"size:20;default:'pending'" json:"status"` // pending, processing, completed, failed
	Decision      Decision       `gorm:"size:20" json:"decision"`
	Confidence    float64        `gorm:"default:0" json:"confidence"`
	Categories    datatypes.JSON `gorm:"type:jsonb;default:'{}'" json:"categories"`
	Reason        string         `gorm:"type:text;default:''" json:"reason"`
	PolicyVersion string         `gorm:"size:20;default:'v1'" json:"policy_version"`
	ReviewedBy    *string        `gorm:"size:36" json:"reviewed_by,omitempty"`
	ReviewedAt    *time.Time     `json:"reviewed_at,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

// TableName returns the table name for Job
func (Job) TableName() string {
	return "moderation_jobs"
}

// IsPending checks if the job is pending
func (j *Job) IsPending() bool {
	return j.Status == "pending"
}

// NeedsManualReview checks if the job needs manual review
func (j *Job) NeedsManualReview() bool {
	return j.Decision == DecisionManualReview
}
