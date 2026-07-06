package model

import "time"

// GalgameBangumiMeta is the Bangumi score/rank narrow table (one row per
// bid-anchored galgame), kept OUT of the main galgame table on purpose:
// third-party rating data is source-owned, refreshed wholesale by
// cmd/enrich-bangumi, and whether/where it is displayed is a later product
// decision. nsfw is stored verbatim (doc 17 §6.1: no content-rating
// inference from it — true does not imply r18 policy here, false implies
// nothing).
type GalgameBangumiMeta struct {
	GalgameID int `gorm:"column:galgame_id;primaryKey;autoIncrement:false" json:"galgame_id"`
	BID       int `gorm:"column:bid;not null" json:"bid"`
	// Score/Rank/Total mirror src_bangumi.subject (score, rank, and the
	// rating count = sum of the score_details buckets).
	Score    float64   `gorm:"not null" json:"score"`
	Rank     int       `gorm:"not null" json:"rank"`
	Total    int       `gorm:"not null" json:"total"`
	NSFW     bool      `gorm:"not null" json:"nsfw"`
	SyncedAt time.Time `gorm:"not null" json:"synced_at"`

	Galgame *Galgame `gorm:"foreignKey:GalgameID" json:"galgame,omitempty"`
}

func (GalgameBangumiMeta) TableName() string { return "galgame_bangumi_meta" }
