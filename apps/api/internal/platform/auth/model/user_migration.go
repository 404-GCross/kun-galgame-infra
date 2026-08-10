package model

import (
	"time"
)

type UserMigration struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	UserID       uint      `gorm:"not null;index" json:"user_id"`
	UserUUID     string    `gorm:"type:uuid;not null;index" json:"user_uuid"`
	SourceDB     string    `gorm:"size:50;not null;index" json:"source_db"`
	SourceUserID uint      `gorm:"not null" json:"source_user_id"`
	SourceEmail  string    `gorm:"size:255;not null" json:"source_email"`
	MergedFrom   *string   `gorm:"size:50" json:"merged_from,omitempty"`
	CreatedAt    time.Time `json:"created_at"`

	User *User `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

func (UserMigration) TableName() string {
	return "user_migrations"
}
