package model

import (
	"time"

	"gorm.io/datatypes"
)

// Result represents a moderation result from a provider
type Result struct {
	ID         uint           `gorm:"primaryKey" json:"id"`
	JobID      uint           `gorm:"not null;index" json:"job_id"`
	Provider   string         `gorm:"size:50;not null" json:"provider"` // openai, anthropic, local
	Decision   Decision       `gorm:"size:20;not null" json:"decision"`
	Confidence float64        `gorm:"not null" json:"confidence"`
	Categories datatypes.JSON `gorm:"type:jsonb;default:'{}'" json:"categories"`
	RawOutput  datatypes.JSON `gorm:"type:jsonb;default:'{}'" json:"raw_output"`
	Latency    int            `gorm:"default:0" json:"latency"` // in milliseconds
	CreatedAt  time.Time      `json:"created_at"`

	// Relations
	Job Job `gorm:"foreignKey:JobID;constraint:OnDelete:CASCADE" json:"-"`
}

// TableName returns the table name for Result
func (Result) TableName() string {
	return "moderation_results"
}
