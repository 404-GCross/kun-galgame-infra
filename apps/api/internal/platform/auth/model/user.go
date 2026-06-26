package model

import (
	"strings"
	"time"

	siteModel "api/internal/platform/site/model"

	"gorm.io/gorm"
)

// NormalizeEmail canonicalizes an email for storage and lookup: trimmed and
// lowercased. Email is case-insensitive in practice (every major provider), so
// "Kun@kungal.com" and "kun@kungal.com" are the same account. New rows store the
// normalized form; lookups normalize the input AND query LOWER(email) (backed by
// a functional index) so legacy mixed-case rows still match.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// User represents the core user identity
type User struct {
	ID          uint           `gorm:"primaryKey" json:"id"`
	UUID        string         `gorm:"type:uuid;uniqueIndex;default:gen_random_uuid()" json:"uuid"`
	Name        string         `gorm:"size:17;uniqueIndex;not null" json:"name"`
	Email       string         `gorm:"size:255;uniqueIndex;not null" json:"email"`
	Password       *string `gorm:"size:255" json:"-"`
	KungalPassword *string `gorm:"size:255" json:"-"` // Legacy bcrypt hash, removed after migration period
	MoyuPassword   *string `gorm:"size:255" json:"-"` // Legacy argon2id "salt_hex:hash_hex", removed after migration period
	// Avatar is the legacy URL string. Kept as permanent fallback for users
	// whose avatars predate image_service (kungal/moyu legacy WebP, exempt
	// from migration per docs/image_service/04-migration-plan.md).
	Avatar string `gorm:"size:255;default:''" json:"avatar"`

	// AvatarImageHash, when set, points to an image_service-managed image.
	// New uploads / avatar changes write here; the frontend resolveAvatarUrl
	// prefers this over Avatar. Old users keep this NULL forever, falling
	// back to Avatar — that's expected, not a bug.
	AvatarImageHash *string `gorm:"size:64;index" json:"avatar_image_hash,omitempty"`

	Bio         string         `gorm:"size:107;default:''" json:"bio"`
	Moemoepoint int            `gorm:"default:0" json:"moemoepoint"`
	// Status: 0 normal, 1 banned. NOTE: migrated moyu/kungal accounts may
	// carry other legacy values (e.g. 2) with source-specific meanings —
	// IsBanned() deliberately checks `== 1` only, so we never repurpose or
	// lock out those rows by accident.
	Status      int            `gorm:"default:0" json:"status"`
	// AnonymizedAt marks an irreversible PII scrub (severe spam / abuse).
	// Set together with Status=1; nil for everyone else. Kept as a distinct
	// column rather than a status value so it can't collide with migrated
	// legacy statuses. See IsAnonymized().
	AnonymizedAt *time.Time `gorm:"index" json:"anonymized_at,omitempty"`
	// OriginalEmail preserves the pre-scrub email when a user is anonymized,
	// for later lookup (备查). nil for everyone else. NOT unique (a fresh user
	// may re-register a since-freed address, then itself be anonymized) and
	// json:"-" so this retained PII never leaks through any model serialization
	// — read it from the DB directly. NOTE: keeping it means anonymize is no
	// longer a full PII scrub for the email field; this is a deliberate trade.
	OriginalEmail *string `gorm:"size:255" json:"-"`
	IP          string         `gorm:"size:45;default:''" json:"-"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`

	// Relations
	SiteData      []UserSiteData   `gorm:"foreignKey:UserID" json:"site_data,omitempty"`
	Sessions      []Session        `gorm:"foreignKey:UserID" json:"-"`
	OAuthAccounts []OAuthAccount   `gorm:"foreignKey:UserID" json:"oauth_accounts,omitempty"`
	Followers     []UserFollow     `gorm:"foreignKey:FollowingID" json:"-"`
	Following     []UserFollow     `gorm:"foreignKey:FollowerID" json:"-"`
	Roles         []siteModel.Role `gorm:"many2many:user_roles;" json:"roles,omitempty"`
}

// RoleNames returns the user's role names as a string slice
func (u *User) RoleNames() []string {
	names := make([]string, len(u.Roles))
	for i, r := range u.Roles {
		names[i] = r.Name
	}
	return names
}

// TableName returns the table name for User
func (User) TableName() string {
	return "users"
}

// IsPasswordSet checks if the user has a password set
func (u *User) IsPasswordSet() bool {
	return u.Password != nil && *u.Password != ""
}

// HasLegacyPassword checks if the user has any legacy password that can be verified
func (u *User) HasLegacyPassword() bool {
	return (u.KungalPassword != nil && *u.KungalPassword != "") ||
		(u.MoyuPassword != nil && *u.MoyuPassword != "")
}

// IsBanned reports whether the user is locked out (blocks login, token
// refresh, and authenticated requests). Checks `Status == 1` only —
// anonymized users are set to Status=1 too, so they're covered, while
// migrated legacy statuses (e.g. 2) are left untouched.
func (u *User) IsBanned() bool {
	return u.Status == 1
}

// IsAnonymized reports whether the user's PII has been scrubbed
// (irreversible). Distinguishes a recoverable ban from a terminal
// anonymization for UI + unban-guard purposes.
func (u *User) IsAnonymized() bool {
	return u.AnonymizedAt != nil
}
