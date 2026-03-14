package model

import (
	"time"
)

// Tag represents a game tag/category
type Tag struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Name      string    `gorm:"size:50;uniqueIndex;not null" json:"name"`
	Slug      string    `gorm:"size:50;uniqueIndex;not null" json:"slug"`
	Color     string    `gorm:"size:7;default:'#000000'" json:"color"`
	CreatedAt time.Time `json:"created_at"`

	// Relations
	Games []Game `gorm:"many2many:game_tags;" json:"-"`
}

// TableName returns the table name for Tag
func (Tag) TableName() string {
	return "tags"
}
