package model

import (
	"time"
)

// PasswordReset represents a password reset token
type PasswordReset struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"not null;index" json:"user_id"`
	Token     string    `gorm:"size:64;uniqueIndex;not null" json:"token"`
	ExpiresAt time.Time `gorm:"not null" json:"expires_at"`
	UsedAt    *time.Time `json:"used_at,omitempty"`
	CreatedAt time.Time `json:"created_at"`

	// Relations
	User *User `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

// TableName returns the table name for PasswordReset
func (PasswordReset) TableName() string {
	return "password_resets"
}

// IsExpired checks if the token has expired
func (p *PasswordReset) IsExpired() bool {
	return time.Now().After(p.ExpiresAt)
}

// IsUsed checks if the token has been used
func (p *PasswordReset) IsUsed() bool {
	return p.UsedAt != nil
}

// IsValid checks if the token is valid (not expired and not used)
func (p *PasswordReset) IsValid() bool {
	return !p.IsExpired() && !p.IsUsed()
}
