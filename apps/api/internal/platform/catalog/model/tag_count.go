package model

import "time"

type CatalogTagWorkCount struct {
	TagID      int64     `gorm:"primaryKey;autoIncrement:false" json:"tag_id"`
	NAll       int       `gorm:"not null" json:"n_all"`
	NSfw       int       `gorm:"not null" json:"n_sfw"`
	NNsfw      int       `gorm:"not null" json:"n_nsfw"`
	ComputedAt time.Time `gorm:"not null" json:"computed_at"`
}

func (CatalogTagWorkCount) TableName() string { return "catalog_tag_work_count" }
