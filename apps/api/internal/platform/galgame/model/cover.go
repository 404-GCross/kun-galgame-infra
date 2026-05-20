package model

import "time"

// GalgameCover is one entry in a galgame's cover candidate set.
//
// Image bytes / dimensions / moderation / TTL all live in image_service
// (referenced by ImageHash = sha-256). This table only holds the
// *relation* attributes: which images are this galgame's covers, in
// what order, with which content rating, from which source.
//
// "Effective banner" semantics (PR2-era, no voting yet):
//
//	effective = SELECT image_hash FROM galgame_cover
//	            WHERE galgame_id = ? AND sort_order = 0
//
// A partial unique index `idx_galgame_cover_pinned`
// (galgame_id) WHERE sort_order = 0 — created by migrate-galgame —
// enforces "at most one pinned cover per galgame". Admins re-pinning
// must demote the old (sort_order=0 → 1) before promoting the new
// (sort_order=N → 0), inside the same transaction.
//
// `Source`/`SourceKey` are extension positions for a future fill-only
// re-sync flow; they cost two empty-string columns until then.
type GalgameCover struct {
	GalgameID int       `gorm:"column:galgame_id;primaryKey" json:"galgame_id"`
	ImageHash string    `gorm:"column:image_hash;type:char(64);primaryKey" json:"image_hash"`
	SortOrder int       `gorm:"column:sort_order;not null;default:0;index" json:"sort_order"`
	Sexual    int16     `gorm:"column:sexual;not null;default:0" json:"sexual"`
	Violence  int16     `gorm:"column:violence;not null;default:0" json:"violence"`
	Source    string    `gorm:"column:source;size:16;default:''" json:"source"`
	SourceKey string    `gorm:"column:source_key;size:128;default:''" json:"source_key"`
	Created   time.Time `gorm:"column:created;autoCreateTime" json:"created"`
}

func (GalgameCover) TableName() string { return "galgame_cover" }

// GalgameScreenshot is one entry in a galgame's gallery / CG / event
// screenshot set. Same {galgame_id, image_hash} composite identity as
// GalgameCover; the two tables are structurally isomorphic but
// independent — the same image hash can serve as a cover on one game
// and a screenshot on another. Adds Caption for per-image gallery text.
//
// SortOrder here means "position in the gallery", not "pinned banner";
// there is no uniqueness constraint on it (multiple screenshots may
// share an order — typically 0 for "no manual ordering yet").
type GalgameScreenshot struct {
	GalgameID int       `gorm:"column:galgame_id;primaryKey" json:"galgame_id"`
	ImageHash string    `gorm:"column:image_hash;type:char(64);primaryKey" json:"image_hash"`
	SortOrder int       `gorm:"column:sort_order;not null;default:0" json:"sort_order"`
	Caption   string    `gorm:"column:caption;type:text;default:''" json:"caption"`
	Sexual    int16     `gorm:"column:sexual;not null;default:0" json:"sexual"`
	Violence  int16     `gorm:"column:violence;not null;default:0" json:"violence"`
	Source    string    `gorm:"column:source;size:16;default:''" json:"source"`
	SourceKey string    `gorm:"column:source_key;size:128;default:''" json:"source_key"`
	Created   time.Time `gorm:"column:created;autoCreateTime" json:"created"`
}

func (GalgameScreenshot) TableName() string { return "galgame_screenshot" }

// PopulateEffectiveBanner fills g.EffectiveBannerHash from the loaded
// Cover list. The rule is now simply "the image_hash of the cover row
// with sort_order=0", since PR5 retired the legacy banner_image_hash
// fallback (the partial unique index makes the sort_order=0 row unique
// per galgame, so the result is unambiguous).
//
// Idempotent and safe on a partially-loaded galgame (zero covers
// leaves EffectiveBannerHash nil; nil g is a no-op via the nil-receiver
// guard).
func PopulateEffectiveBanner(g *Galgame) {
	if g == nil {
		return
	}
	for i := range g.Cover {
		if g.Cover[i].SortOrder == 0 {
			h := g.Cover[i].ImageHash
			g.EffectiveBannerHash = &h
			return
		}
	}
}
