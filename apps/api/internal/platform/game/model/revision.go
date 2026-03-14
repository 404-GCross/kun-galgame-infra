package model

import (
	"time"

	"gorm.io/datatypes"
)

// Revision represents a game revision/edit history
type Revision struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	GameID    uint           `gorm:"not null;index" json:"game_id"`
	UserUUID  string         `gorm:"size:36;not null" json:"user_uuid"`
	Changes   datatypes.JSON `gorm:"type:jsonb;not null" json:"changes"`
	Message   string         `gorm:"size:500;default:''" json:"message"`
	CreatedAt time.Time      `json:"created_at"`

	// Relations
	Game Game `gorm:"foreignKey:GameID;constraint:OnDelete:CASCADE" json:"-"`
}

// TableName returns the table name for Revision
func (Revision) TableName() string {
	return "revisions"
}
