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

	// Relations
	OAuthClients []OAuthClient `gorm:"foreignKey:SiteID" json:"oauth_clients,omitempty"`
}

// TableName returns the table name for Site
func (Site) TableName() string {
	return "sites"
}
