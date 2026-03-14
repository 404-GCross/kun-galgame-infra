package model

import (
	"time"

	"gorm.io/gorm"
)

// User represents the core user identity
type User struct {
	ID          uint           `gorm:"primaryKey" json:"id"`
	UUID        string         `gorm:"type:uuid;uniqueIndex;default:gen_random_uuid()" json:"uuid"`
	Name        string         `gorm:"size:17;uniqueIndex;not null" json:"name"`
	Email       string         `gorm:"size:255;uniqueIndex;not null" json:"email"`
	Password    *string        `gorm:"size:255" json:"-"`
	Avatar      string         `gorm:"size:255;default:''" json:"avatar"`
	Bio         string         `gorm:"size:107;default:''" json:"bio"`
	Moemoepoint int            `gorm:"default:0" json:"moemoepoint"`
	Status      int            `gorm:"default:0" json:"status"` // 0: normal, 1: banned
	IP          string         `gorm:"size:45;default:''" json:"-"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`

	// Relations
	SiteData      []UserSiteData `gorm:"foreignKey:UserID" json:"site_data,omitempty"`
	Sessions      []Session      `gorm:"foreignKey:UserID" json:"-"`
	OAuthAccounts []OAuthAccount `gorm:"foreignKey:UserID" json:"oauth_accounts,omitempty"`
	Followers     []UserFollow   `gorm:"foreignKey:FollowingID" json:"-"`
	Following     []UserFollow   `gorm:"foreignKey:FollowerID" json:"-"`
}

// TableName returns the table name for User
func (User) TableName() string {
	return "users"
}

// IsPasswordSet checks if the user has a password set
func (u *User) IsPasswordSet() bool {
	return u.Password != nil && *u.Password != ""
}

// IsBanned checks if the user is banned
func (u *User) IsBanned() bool {
	return u.Status == 1
}
