package model

import (
	"time"

	"gorm.io/datatypes"
)

// UserSiteData represents user-specific data for each site
type UserSiteData struct {
	ID              uint           `gorm:"primaryKey" json:"id"`
	UserID          uint           `gorm:"not null;index" json:"user_id"`
	SiteID          uint           `gorm:"not null;index" json:"site_id"`
	Status          int            `gorm:"default:0" json:"status"`
	DailyCheckIn    int            `gorm:"default:0" json:"daily_check_in"`
	DailyImageCount int            `gorm:"default:0" json:"daily_image_count"`
	Extra           datatypes.JSON `gorm:"type:jsonb;default:'{}'" json:"extra"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`

	// Relations
	User User `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE" json:"-"`
	// Site relation will be defined in site module
}

// TableName returns the table name for UserSiteData
func (UserSiteData) TableName() string {
	return "user_site_data"
}

// Indexes returns the indexes for UserSiteData
func (UserSiteData) Indexes() []string {
	return []string{
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_user_site_data_unique ON user_site_data(user_id, site_id)",
	}
}
