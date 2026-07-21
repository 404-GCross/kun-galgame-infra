package model

import "time"

// GalgameDlsiteMeta is the DLsite rating + popularity narrow table (one row per
// DLsite-anchored galgame), kept OUT of the main galgame table on purpose —
// exactly like GalgameVNDBMeta / GalgameBangumiMeta / GalgameEGMeta:
// third-party source-owned data, refreshed wholesale by cmd/enrich-dlsite-meta,
// and whether/where it is displayed is a later product decision. Values come
// straight from the DLsite mirror `works.info_json` (surveyed 2026-07-21: the
// only JSON column carrying the counts — product_json has the star but none of
// dl/wishlist/review), resolved through the catalog DLsite exact RELEASE anchor
// (workno anchors are SKU-natured and hang at release level, doc 17 R3).
//
// Every value column is nullable: DLsite genuinely omits fields per work
// (dl_count only exists for doujin-shop works; the rating trio only once
// >=5 votes exist), and NULL vs 0 must stay distinct (0 downloads is a real
// value, an absent counter is not) — which also dodges the GORM default-tag
// zero trap. SyncedAt advances only when a value actually changed (the upsert
// is change-detected), so it reads as "last observed change", not "last run".
type GalgameDlsiteMeta struct {
	GalgameID int `gorm:"column:galgame_id;primaryKey;autoIncrement:false" json:"galgame_id"`
	// Workno is the anchoring DLsite product id (RJ/VJ/BJ…), the join key back
	// into the mirror and the provenance of every value on the row.
	Workno string `gorm:"column:workno;not null" json:"workno"`
	// RateAverageStar is DLsite's own displayed star average on the native 0-5
	// scale with the source's full precision (mirror key `rate_average_2dp`,
	// e.g. 4.36). The wire key literally named rate_average_star is that value
	// bucketed to half stars and ×10 (10-50) for the star widget — a display
	// encoding, NOT stored (58 拍板 stores the source's native scale, and the
	// native scale here is the 0-5 star average DLsite itself prints). NULL =
	// DLsite publishes no rating (fewer than the ~5-vote threshold); never a
	// fake zero.
	RateAverageStar *float64 `gorm:"column:rate_average_star;type:numeric" json:"rate_average_star"`
	// RateCount backs RateAverageStar; the pair is always present or absent
	// together (surveyed: no star without votes, minimum observed count 5).
	RateCount *int `gorm:"column:rate_count" json:"rate_count"`
	// DlCount is the DLsite sales counter. NULL = DLsite does not publish it
	// for this work (commercial/pro works carry no public sales count).
	DlCount *int64 `gorm:"column:dl_count" json:"dl_count"`
	// WishlistCount is the お気に入り counter.
	WishlistCount *int64 `gorm:"column:wishlist_count" json:"wishlist_count"`
	// ReviewCount is the written-review counter.
	ReviewCount *int      `gorm:"column:review_count" json:"review_count"`
	SyncedAt    time.Time `gorm:"column:synced_at;not null" json:"synced_at"`

	Galgame *Galgame `gorm:"foreignKey:GalgameID" json:"galgame,omitempty"`
}

func (GalgameDlsiteMeta) TableName() string { return "galgame_dlsite_meta" }
