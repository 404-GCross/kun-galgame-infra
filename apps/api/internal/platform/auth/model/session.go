package model

import (
	"time"
)

// Session represents a user login session
type Session struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	UserID       uint      `gorm:"not null;index" json:"user_id"`
	SessionToken string    `gorm:"size:255;uniqueIndex;not null" json:"-"`
	RefreshToken string    `gorm:"size:255;uniqueIndex;not null" json:"-"`
	UserAgent    string    `gorm:"type:text;default:''" json:"user_agent"`
	IPAddress    string    `gorm:"size:45;default:''" json:"ip_address"`
	ExpiresAt    time.Time `gorm:"not null" json:"expires_at"`
	CreatedAt    time.Time `json:"created_at"`

	// Relations
	User User `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE" json:"-"`
}

// TableName returns the table name for Session
func (Session) TableName() string {
	return "sessions"
}

// IsExpired checks if the session has expired
func (s *Session) IsExpired() bool {
	return time.Now().After(s.ExpiresAt)
}
