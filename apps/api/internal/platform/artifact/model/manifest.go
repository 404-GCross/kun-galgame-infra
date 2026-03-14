package model

import (
	"time"

	"gorm.io/datatypes"
)

// Manifest represents a game manifest for app/desktop launch
type Manifest struct {
	ID           uint           `gorm:"primaryKey" json:"id"`
	ArtifactID   uint           `gorm:"not null;uniqueIndex" json:"artifact_id"`
	Executable   string         `gorm:"size:500;not null" json:"executable"`
	Arguments    string         `gorm:"size:1000;default:''" json:"arguments"`
	WorkingDir   string         `gorm:"size:500;default:''" json:"working_dir"`
	SavePath     string         `gorm:"size:500;default:''" json:"save_path"`
	Requirements datatypes.JSON `gorm:"type:jsonb;default:'{}'" json:"requirements"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`

	// Relations
	Artifact Artifact `gorm:"foreignKey:ArtifactID;constraint:OnDelete:CASCADE" json:"-"`
}

// TableName returns the table name for Manifest
func (Manifest) TableName() string {
	return "manifests"
}
