package model

import "time"

type TrustSitePolicy struct {
	Site string `gorm:"primaryKey;column:site" json:"site"`

	ScanMode *int16 `gorm:"column:scan_mode" json:"scan_mode"`

	SampleRate *float64 `gorm:"column:sample_rate" json:"sample_rate"`

	FlagThreshold *float32 `gorm:"type:real;column:flag_threshold" json:"flag_threshold"`

	AggregateThreshold *float32 `gorm:"type:real;column:aggregate_threshold" json:"aggregate_threshold"`

	AutoHideEnabled *bool `gorm:"column:auto_hide_enabled" json:"auto_hide_enabled"`

	Note *string `gorm:"column:note" json:"note"`

	CreatedAt time.Time `gorm:"not null;default:now();column:created_at" json:"created_at"`
	UpdatedAt time.Time `gorm:"not null;default:now();column:updated_at" json:"updated_at"`
}

func (TrustSitePolicy) TableName() string { return "trust_site_policy" }
