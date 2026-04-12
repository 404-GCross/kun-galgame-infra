package model

import "time"

// GalgameLink represents an external link for a galgame
type GalgameLink struct {
	ID        int       `gorm:"primaryKey;autoIncrement" json:"id"`
	Name      string    `gorm:"size:107;default:''" json:"name"`
	Link      string    `gorm:"size:233;default:''" json:"link"`
	GalgameID int       `gorm:"column:galgame_id;not null;index" json:"galgame_id"`
	UserID    int       `gorm:"column:user_id;not null;index" json:"user_id"`
	Created   time.Time `gorm:"autoCreateTime" json:"created"`
	Updated   time.Time `gorm:"autoUpdateTime" json:"updated"`
}

func (GalgameLink) TableName() string { return "galgame_link" }
