package model

import (
	"encoding/json"
	"slices"
	"time"

	"gorm.io/datatypes"
)

// Review status enum. Stored as SMALLINT.
const (
	ReviewPending  int16 = 0
	ReviewApproved int16 = 1
	ReviewRejected int16 = 2
	ReviewManual   int16 = 3
)

// Image is the core record per unique image hash. Single row per hash across
// all sites (UNIQUE(hash)). Physical storage, metadata, and moderation state
// all live here. Site-level audit goes in ImageSiteUsage.
type Image struct {
	ID         int64  `gorm:"primaryKey" json:"id"`
	Hash       string `gorm:"type:char(64);uniqueIndex;not null" json:"hash"`
	StorageKey string `gorm:"size:512;not null" json:"storage_key"`
	MIME       string `gorm:"size:32;not null" json:"mime"`
	Ext        string `gorm:"size:8;not null" json:"ext"`

	Width     int   `gorm:"not null" json:"width"`
	Height    int   `gorm:"not null" json:"height"`
	SizeBytes int64 `gorm:"not null" json:"size_bytes"`

	// Variants stores a JSONB array of variant names already generated
	// (e.g. ["100","256"]). Checked on upload to avoid regenerating.
	Variants datatypes.JSON `gorm:"type:jsonb;not null;default:'[]'" json:"variants"`

	// Original (pre-transcode) format metadata. Informational only.
	OriginMIME string `gorm:"size:32" json:"origin_mime,omitempty"`
	OriginSize int64  `gorm:"default:0" json:"origin_size,omitempty"`

	// Moderation. V1 defaults to ReviewApproved; V3 will flip default and
	// wire up the async worker.
	ReviewStatus int16          `gorm:"not null;default:1" json:"review_status"`
	ReviewLabels datatypes.JSON `gorm:"type:jsonb" json:"review_labels,omitempty"`
	ReviewedAt   *time.Time     `json:"reviewed_at,omitempty"`

	// First uploader audit (not ownership). Cross-site dedup means this is
	// the first uploader globally, NOT "the owner".
	FirstUploaderSub    string `gorm:"size:64" json:"first_uploader_sub,omitempty"`
	FirstUploaderClient string `gorm:"size:64" json:"first_uploader_client,omitempty"`
	FirstUploaderIP     string `gorm:"size:64" json:"first_uploader_ip,omitempty"`

	// TTL machinery. last_referenced_at is refreshed on upload and on
	// POST /image/reference-ping by the calling service.
	LastReferencedAt time.Time  `gorm:"not null;default:now();index" json:"last_referenced_at"`
	CreatedAt        time.Time  `gorm:"not null;default:now()" json:"created_at"`
	DeletedAt        *time.Time `gorm:"index" json:"deleted_at,omitempty"`
}

// TableName is explicit to avoid surprises from GORM's pluralizer.
func (Image) TableName() string {
	return "images"
}

// VariantList decodes the Variants JSONB column to []string.
func (i *Image) VariantList() []string {
	if len(i.Variants) == 0 {
		return nil
	}
	var out []string
	if err := json.Unmarshal(i.Variants, &out); err != nil {
		return nil
	}
	return out
}

// HasVariant returns true if the given variant name is already generated.
func (i *Image) HasVariant(name string) bool {
	return slices.Contains(i.VariantList(), name)
}

// SetVariants encodes the given slice into Variants JSONB column.
func (i *Image) SetVariants(variants []string) {
	b, _ := json.Marshal(variants)
	i.Variants = datatypes.JSON(b)
}
