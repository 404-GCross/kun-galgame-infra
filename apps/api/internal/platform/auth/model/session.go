package model

import (
	"time"
)

// Session represents a user login session.
//
// ClientID identifies which OAuth client (or "" for the legacy /auth/login
// path used by the admin UI) the session belongs to. Binding sessions to
// clients lets us:
//   - prevent a leaked public-client refresh_token from being used by
//     a different client (refresh checks session.ClientID == request client_id)
//   - selectively revoke sessions per client without nuking unrelated
//     sessions of the same user
type Session struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	UserID       uint      `gorm:"not null;index" json:"user_id"`
	ClientID     string    `gorm:"size:50;index;default:''" json:"client_id"`
	// Scope is the OAuth scope granted at code-exchange time. Persisted
	// here so refresh can re-issue access_tokens carrying the SAME scope
	// — without this, a refreshed token would silently lose its scope
	// claim and /oauth/userinfo would treat it as "all fields" (privacy
	// regression: `openid`-only token would upgrade to email+profile
	// access after one refresh).
	Scope        string    `gorm:"type:text;default:''" json:"scope"`
	SessionToken string    `gorm:"type:text;uniqueIndex;not null" json:"-"`
	RefreshToken string    `gorm:"type:text;uniqueIndex;not null" json:"-"`
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
