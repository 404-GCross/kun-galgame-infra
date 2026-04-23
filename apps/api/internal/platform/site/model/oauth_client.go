package model

import (
	"encoding/json"
	"slices"
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

	// --- Image service extension fields ---
	ImageEnabled          bool           `gorm:"not null;default:false" json:"image_enabled"`
	ImageSiteKey          string         `gorm:"size:32" json:"image_site_key,omitempty"`
	ImageQuotaDaily       int            `gorm:"default:10000" json:"image_quota_daily"`
	ImageQuotaBytesDaily  int64          `gorm:"default:10737418240" json:"image_quota_bytes_daily"`
	ImageMaxFileSize      int64          `gorm:"default:10485760" json:"image_max_file_size"`
	ImageAllowedPresets   datatypes.JSON `gorm:"type:jsonb" json:"image_allowed_presets,omitempty"`

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
	return slices.Contains(uris, uri)
}

// AllowedPresets returns the parsed list of image preset names this client
// can use. Returns nil/empty if the field is not set.
func (c *OAuthClient) AllowedPresets() []string {
	if len(c.ImageAllowedPresets) == 0 {
		return nil
	}
	var presets []string
	if err := json.Unmarshal(c.ImageAllowedPresets, &presets); err != nil {
		return nil
	}
	return presets
}

// IsPresetAllowed checks if the given preset is in AllowedPresets.
func (c *OAuthClient) IsPresetAllowed(preset string) bool {
	return slices.Contains(c.AllowedPresets(), preset)
}
