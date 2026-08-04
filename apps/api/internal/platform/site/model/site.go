package model

import (
	"time"
)

// Site represents a registered site/application
type Site struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"size:50;not null" json:"name"`
	Domain      string    `gorm:"size:255;uniqueIndex;not null" json:"domain"`
	Description string    `gorm:"type:text;default:''" json:"description"`
	CreatedAt   time.Time `json:"created_at"`

	// CreatedByUserID is the admin who registered this site. It scopes the
	// console: an admin without oauth.sites.manage_all (ren) sees and edits
	// only their own rows. NULL means "created before ownership stamping" —
	// such a site belongs to nobody and is reachable by ren only, which is the
	// safe default for the first-party sites that predate this column.
	CreatedByUserID *uint `gorm:"index" json:"created_by_user_id,omitempty"`

	// Relations
	OAuthClients []OAuthClient `gorm:"foreignKey:SiteID" json:"oauth_clients,omitempty"`
}

// TableName returns the table name for Site
func (Site) TableName() string {
	return "sites"
}
