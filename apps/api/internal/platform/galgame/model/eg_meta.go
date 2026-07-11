package model

import "time"

// GalgameEGMeta is the ErogameScape (EG) score narrow table (one row per
// EG-anchored galgame), kept OUT of the main galgame table on purpose —
// exactly like GalgameVNDBMeta / GalgameBangumiMeta: third-party rating data
// is source-owned, refreshed wholesale by cmd/enrich-eg-scores, and
// whether/where it is displayed is a later product decision. Values come
// straight from the EG mirror `games` table (median 中央値 + count2 votes),
// resolved through the catalog EG exact work anchor; no derivation.
type GalgameEGMeta struct {
	GalgameID int `gorm:"column:galgame_id;primaryKey;autoIncrement:false" json:"galgame_id"`
	EGGameID  int `gorm:"column:eg_game_id;not null" json:"eg_game_id"`
	// Median is EG's 中央値 on the native 0-100 scale; NULL when EG has no
	// median for the game (never store a fake zero — EG uses 0 as a real low
	// score, so NULL vs 0 must stay distinct, which also dodges the GORM
	// default-tag zero trap).
	Median    *int      `gorm:"column:median" json:"median"`
	VoteCount int       `gorm:"column:vote_count;not null" json:"vote_count"`
	SyncedAt  time.Time `gorm:"column:synced_at;not null" json:"synced_at"`

	Galgame *Galgame `gorm:"foreignKey:GalgameID" json:"galgame,omitempty"`
}

func (GalgameEGMeta) TableName() string { return "galgame_eg_meta" }
