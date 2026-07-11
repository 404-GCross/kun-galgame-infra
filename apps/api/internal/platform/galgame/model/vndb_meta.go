package model

import "time"

// GalgameVNDBMeta is the VNDB rating/votecount narrow table (one row per
// vndb-anchored galgame), kept OUT of the main galgame table on purpose —
// exactly like GalgameBangumiMeta: third-party rating data is source-owned,
// refreshed wholesale by cmd/sync-vndb-scores, and whether/where it is
// displayed is a later product decision. Values come straight from the kana
// API /vn (rating + votecount); no derivation.
type GalgameVNDBMeta struct {
	GalgameID int    `gorm:"column:galgame_id;primaryKey;autoIncrement:false" json:"galgame_id"`
	VNDBID    string `gorm:"column:vndb_id;not null" json:"vndb_id"`
	// Rating is the kana-API vote average on VNDB's native 10-100 scale;
	// NULL when the VN has no votes (never store a fake zero). A meaningful
	// zero can't exist on this scale, but keeping the pointer makes "no votes"
	// vs "some score" unambiguous and dodges the GORM default-tag zero trap.
	Rating    *float64  `gorm:"column:rating" json:"rating"`
	VoteCount int       `gorm:"column:vote_count;not null" json:"vote_count"`
	SyncedAt  time.Time `gorm:"column:synced_at;not null" json:"synced_at"`

	Galgame *Galgame `gorm:"foreignKey:GalgameID" json:"galgame,omitempty"`
}

func (GalgameVNDBMeta) TableName() string { return "galgame_vndb_meta" }
