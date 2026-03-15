package model

import (
	"time"

	siteModel "api/internal/platform/site/model"

	"gorm.io/gorm"
)

// User represents the core user identity
type User struct {
	ID          uint           `gorm:"primaryKey" json:"id"`
	UUID        string         `gorm:"type:uuid;uniqueIndex;default:gen_random_uuid()" json:"uuid"`
	Name        string         `gorm:"size:17;uniqueIndex;not null" json:"name"`
	Email       string         `gorm:"size:255;uniqueIndex;not null" json:"email"`
	Password       *string `gorm:"size:255" json:"-"`
	KungalPassword *string `gorm:"size:255" json:"-"` // Legacy bcrypt hash, removed after migration period
	MoyuPassword   *string `gorm:"size:255" json:"-"` // Legacy argon2id "salt_hex:hash_hex", removed after migration period
	Avatar      string         `gorm:"size:255;default:''" json:"avatar"`
	Bio         string         `gorm:"size:107;default:''" json:"bio"`
	Moemoepoint int            `gorm:"default:0" json:"moemoepoint"`
	Status      int            `gorm:"default:0" json:"status"` // 0: normal, 1: banned
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

// IsBanned checks if the user is banned
func (u *User) IsBanned() bool {
	return u.Status == 1
}
