package model

import (
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Game represents a game entry
type Game struct {
	ID          uint           `gorm:"primaryKey" json:"id"`
	UUID        string         `gorm:"type:uuid;uniqueIndex;default:gen_random_uuid()" json:"uuid"`
	Name        string         `gorm:"size:255;not null" json:"name"`
	Aliases     datatypes.JSON `gorm:"type:jsonb;default:'[]'" json:"aliases"`
	Description string         `gorm:"type:text" json:"description"`
	CoverImage  string         `gorm:"size:500;default:''" json:"cover_image"`
	ReleaseDate *time.Time     `json:"release_date,omitempty"`
	Developer   string         `gorm:"size:255;default:''" json:"developer"`
	Publisher   string         `gorm:"size:255;default:''" json:"publisher"`
	Status      int            `gorm:"default:0" json:"status"` // 0: draft, 1: published
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`

	// Relations
	Tags      []Tag      `gorm:"many2many:game_tags;" json:"tags,omitempty"`
	Revisions []Revision `gorm:"foreignKey:GameID" json:"revisions,omitempty"`
}

// TableName returns the table name for Game
func (Game) TableName() string {
	return "games"
}
