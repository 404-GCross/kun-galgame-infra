package model

import (
	"time"
)

type Site struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"size:50;not null" json:"name"`
	Domain      string    `gorm:"size:255;uniqueIndex;not null" json:"domain"`
	Description string    `gorm:"type:text;default:''" json:"description"`
	CreatedAt   time.Time `json:"created_at"`

	CreatedByUserID *uint `gorm:"index" json:"created_by_user_id,omitempty"`

	OAuthClients []OAuthClient `gorm:"foreignKey:SiteID" json:"oauth_clients,omitempty"`
}

func (Site) TableName() string {
	return "sites"
}
