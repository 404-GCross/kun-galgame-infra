package model

import (
	"encoding/json"
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

// IsActive checks if the OAuth client is active
func (c *OAuthClient) IsActive() bool {
	return true // All clients are active by default
}

// HasRedirectURI checks if the given redirect URI is allowed
func (c *OAuthClient) HasRedirectURI(uri string) bool {
	var uris []string
	if err := json.Unmarshal(c.RedirectURIs, &uris); err != nil {
		return false
	}
	for _, allowed := range uris {
		if allowed == uri {
			return true
		}
	}
	return false
}
