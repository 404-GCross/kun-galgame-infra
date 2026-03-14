package model

import (
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Artifact represents an uploaded file/resource
type Artifact struct {
	ID          uint           `gorm:"primaryKey" json:"id"`
	UUID        string         `gorm:"type:uuid;uniqueIndex;default:gen_random_uuid()" json:"uuid"`
	UserUUID    string         `gorm:"size:36;not null;index" json:"user_uuid"`
	GameUUID    *string        `gorm:"size:36;index" json:"game_uuid,omitempty"`
	Name        string         `gorm:"size:255;not null" json:"name"`
	Description string         `gorm:"type:text;default:''" json:"description"`
	FileKey     string         `gorm:"size:500;not null" json:"file_key"`
	FileSize    int64          `gorm:"not null" json:"file_size"`
	MimeType    string         `gorm:"size:100;default:''" json:"mime_type"`
	Checksum    string         `gorm:"size:64;default:''" json:"checksum"`
	Status      int            `gorm:"default:0" json:"status"` // 0: processing, 1: ready, 2: failed
	Metadata    datatypes.JSON `gorm:"type:jsonb;default:'{}'" json:"metadata"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName returns the table name for Artifact
func (Artifact) TableName() string {
	return "artifacts"
}

// IsReady checks if the artifact is ready for download
func (a *Artifact) IsReady() bool {
	return a.Status == 1
}

// IsProcessing checks if the artifact is being processed
func (a *Artifact) IsProcessing() bool {
	return a.Status == 0
}
