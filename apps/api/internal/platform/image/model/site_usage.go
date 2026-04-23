package model

import "time"

// ImageSiteUsage is the per-site audit table. One row per (hash, site)
// combination. Does NOT influence physical storage or moderation state
// (those are single-valued per hash on the Image row). Purely for
// bookkeeping: "kungal's and moyu's view of who uploaded what when".
type ImageSiteUsage struct {
	ID                  int64     `gorm:"primaryKey" json:"id"`
	Hash                string    `gorm:"type:char(64);not null;uniqueIndex:idx_site_usage_hash_site,priority:1;index" json:"hash"`
	Site                string    `gorm:"size:32;not null;uniqueIndex:idx_site_usage_hash_site,priority:2" json:"site"`
	FirstUploaderSub    string    `gorm:"size:64" json:"first_uploader_sub,omitempty"`
	FirstUploaderClient string    `gorm:"size:64" json:"first_uploader_client,omitempty"`
	FirstUploadedAt     time.Time `gorm:"not null;default:now()" json:"first_uploaded_at"`
	UploadCount         int       `gorm:"not null;default:1" json:"upload_count"`
	LastUploadedAt      time.Time `gorm:"not null;default:now()" json:"last_uploaded_at"`
}

func (ImageSiteUsage) TableName() string {
	return "image_site_usage"
}
