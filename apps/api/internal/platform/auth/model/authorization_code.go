package model

import (
	"time"
)

// AuthorizationCode represents an OAuth authorization code
type AuthorizationCode struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	Code         string    `gorm:"size:64;uniqueIndex;not null" json:"code"`
	ClientID     string    `gorm:"size:50;not null;index" json:"client_id"`
	UserID       uint      `gorm:"not null;index" json:"user_id"`
	RedirectURI  string    `gorm:"size:255;not null" json:"redirect_uri"`
	Scope        string    `gorm:"size:255" json:"scope"`
	CodeChallenge string   `gorm:"size:128" json:"code_challenge"` // PKCE
	CodeChallengeMethod string `gorm:"size:10" json:"code_challenge_method"` // S256 or plain
	ExpiresAt    time.Time `gorm:"not null" json:"expires_at"`
	UsedAt       *time.Time `json:"used_at,omitempty"`
	CreatedAt    time.Time `json:"created_at"`

	// Relations
	User *User `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

// TableName returns the table name for AuthorizationCode
func (AuthorizationCode) TableName() string {
	return "authorization_codes"
}

// IsExpired checks if the code has expired
func (c *AuthorizationCode) IsExpired() bool {
	return time.Now().After(c.ExpiresAt)
}

// IsUsed checks if the code has been used
func (c *AuthorizationCode) IsUsed() bool {
	return c.UsedAt != nil
}

// IsValid checks if the code is valid (not expired and not used)
func (c *AuthorizationCode) IsValid() bool {
	return !c.IsExpired() && !c.IsUsed()
}
