package model

import (
	"time"
)

// UserMigration tracks migrated users from source databases
type UserMigration struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	UserID       uint      `gorm:"not null;index" json:"user_id"`
	UserUUID     string    `gorm:"type:uuid;not null;index" json:"user_uuid"`
	SourceDB     string    `gorm:"size:50;not null;index" json:"source_db"` // "kungal" or "moyu"
	SourceUserID uint      `gorm:"not null" json:"source_user_id"`
	SourceEmail  string    `gorm:"size:255;not null" json:"source_email"`
	MergedFrom   *string   `gorm:"size:50" json:"merged_from,omitempty"` // If merged, which DB was secondary
	CreatedAt    time.Time `json:"created_at"`

	// Relations
	User *User `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

// TableName returns the table name for UserMigration
func (UserMigration) TableName() string {
	return "user_migrations"
}
