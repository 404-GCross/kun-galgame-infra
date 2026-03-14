package model

import (
	"time"

	"gorm.io/datatypes"
)

// OAuthClient represents an OAuth client for a site
type OAuthClient struct {
	ID           string         `gorm:"size:50;primaryKey" json:"id"`
	SiteID       *uint          `gorm:"index" json:"site_id,omitempty"`
	Name         string         `gorm:"size:100;not null" json:"name"`
	Secret       string         `gorm:"size:255;not null" json:"-"`
	RedirectURIs datatypes.JSON `gorm:"type:jsonb;not null" json:"redirect_uris"`
	Grants       datatypes.JSON `gorm:"type:jsonb;not null" json:"grants"`
	CreatedAt    time.Time      `json:"created_at"`

	// Relations
	Site *Site `gorm:"foreignKey:SiteID" json:"site,omitempty"`
}

// TableName returns the table name for OAuthClient
func (OAuthClient) TableName() string {
	return "oauth_clients"
}
