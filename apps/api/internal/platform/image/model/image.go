package model

import (
	"encoding/json"
	"slices"
	"time"

	"gorm.io/datatypes"
)

const (
	ReviewPending  int16 = 0
	ReviewApproved int16 = 1
	ReviewRejected int16 = 2
	ReviewManual   int16 = 3
)

type Image struct {
	ID         int64  `gorm:"primaryKey" json:"id"`
	Hash       string `gorm:"type:char(64);uniqueIndex;not null" json:"hash"`
	StorageKey string `gorm:"size:512;not null" json:"storage_key"`
	MIME       string `gorm:"size:32;not null" json:"mime"`
	Ext        string `gorm:"size:8;not null" json:"ext"`

	Width     int   `gorm:"not null" json:"width"`
	Height    int   `gorm:"not null" json:"height"`
	SizeBytes int64 `gorm:"not null" json:"size_bytes"`

	Thumbhash string `gorm:"size:64;not null;default:''" json:"thumbhash,omitempty"`

	Variants datatypes.JSON `gorm:"type:jsonb;not null;default:'[]'" json:"variants"`

	OriginMIME string `gorm:"size:32" json:"origin_mime,omitempty"`
	OriginSize int64  `gorm:"default:0" json:"origin_size,omitempty"`

	ReviewStatus int16          `gorm:"not null;default:1" json:"review_status"`
	ReviewLabels datatypes.JSON `gorm:"type:jsonb" json:"review_labels,omitempty"`
	ReviewedAt   *time.Time     `json:"reviewed_at,omitempty"`

	FirstUploaderSub    string `gorm:"size:64" json:"first_uploader_sub,omitempty"`
	FirstUploaderClient string `gorm:"size:64" json:"first_uploader_client,omitempty"`
	FirstUploaderIP     string `gorm:"size:64" json:"first_uploader_ip,omitempty"`

	LastReferencedAt time.Time  `gorm:"not null;default:now();index" json:"last_referenced_at"`
	CreatedAt        time.Time  `gorm:"not null;default:now()" json:"created_at"`
	DeletedAt        *time.Time `gorm:"index" json:"deleted_at,omitempty"`
}

func (Image) TableName() string {
	return "images"
}

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

func (i *Image) HasVariant(name string) bool {
	return slices.Contains(i.VariantList(), name)
}

func (i *Image) SetVariants(variants []string) {
	b, _ := json.Marshal(variants)
	i.Variants = datatypes.JSON(b)
}
