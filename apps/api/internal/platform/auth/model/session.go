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

	// PrevRefreshToken + RotatedAt implement refresh-token rotation with a
	// grace window. On each refresh the just-spent token moves to
	// PrevRefreshToken and RotatedAt = now. A request still presenting
	// PrevRefreshToken within RefreshGraceWindow is a concurrent/retry
	// caller racing the rotation — the norm for confidential SSR proxies
	// (kungal/moyu fire several in-flight requests the moment the
	// access_token expires). It gets a fresh access_token + the CURRENT
	// refresh_token instead of a 401. Presenting it AFTER the window is
	// treated as token reuse (possible theft) → session revoked.
	//
	// Not unique: '' repeats across rows and it transiently equals a
	// sibling's current token; a plain index supports the OR-lookup.
	PrevRefreshToken string     `gorm:"type:text;index;default:''" json:"-"`
	RotatedAt        *time.Time `json:"-"`

	UserAgent string    `gorm:"type:text;default:''" json:"user_agent"`
	IPAddress string    `gorm:"size:45;default:''" json:"ip_address"`
	ExpiresAt time.Time `gorm:"not null" json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`

	// Relations
	User User `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE" json:"-"`
}

// TableName returns the table name for Session
func (Session) TableName() string {
	return "sessions"
}

// RefreshGraceWindow is how long a just-rotated (previous) refresh_token
// stays acceptable. Long enough to absorb concurrent in-flight refreshes
// and a sloppy client that persists the new token a little late; short
// enough that genuine token-replay theft is still caught by reuse
// detection. 2 minutes is comfortably above any real request fan-out.
const RefreshGraceWindow = 2 * time.Minute

// IsExpired checks if the session has expired
func (s *Session) IsExpired() bool {
	return time.Now().After(s.ExpiresAt)
}

// PrevTokenWithinGrace reports whether RotatedAt is set and the previous
// refresh_token is still inside the grace window.
func (s *Session) PrevTokenWithinGrace() bool {
	return s.RotatedAt != nil && time.Since(*s.RotatedAt) <= RefreshGraceWindow
}
