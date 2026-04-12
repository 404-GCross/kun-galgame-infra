package model

import (
	"time"

	"gorm.io/datatypes"
)

// GalgamePR represents a pull request (edit request) for a galgame
type GalgamePR struct {
	ID            int             `gorm:"primaryKey;autoIncrement" json:"id"`
	Status        int             `gorm:"default:0" json:"status"` // 0=pending, 1=merged, 2=declined
	Index         int             `gorm:"default:0" json:"index"`
	Note          string          `gorm:"size:1007;default:''" json:"note"`
	CompletedTime *time.Time      `gorm:"column:completed_time" json:"completed_time,omitempty"`
	OldData       *datatypes.JSON `gorm:"type:jsonb" json:"old_data,omitempty"`
	NewData       *datatypes.JSON `gorm:"type:jsonb" json:"new_data,omitempty"`
	UserID        int             `gorm:"column:user_id;not null;index" json:"user_id"`
	GalgameID     int             `gorm:"column:galgame_id;not null;index" json:"galgame_id"`
	Created       time.Time       `gorm:"autoCreateTime" json:"created"`
	Updated       time.Time       `gorm:"autoUpdateTime" json:"updated"`
}

func (GalgamePR) TableName() string { return "galgame_pr" }

// GalgameHistory represents an edit history entry
type GalgameHistory struct {
	ID        int       `gorm:"primaryKey;autoIncrement" json:"id"`
	Action    string    `gorm:"default:''" json:"action"` // created, updated, merged, declined
	Type      string    `gorm:"default:''" json:"type"`
	Content   string    `gorm:"size:1007;default:''" json:"content"`
	GalgameID int       `gorm:"column:galgame_id;not null;index" json:"galgame_id"`
	UserID    int       `gorm:"column:user_id;not null;index" json:"user_id"`
	Created   time.Time `gorm:"autoCreateTime" json:"created"`
	Updated   time.Time `gorm:"autoUpdateTime" json:"updated"`
}

func (GalgameHistory) TableName() string { return "galgame_history" }
