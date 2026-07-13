package model

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
// `Source`/`SourceKey` carry provenance (""=user, "vndb"=sync; SourceKey is
// the VNDB cv-id). `Kind` labels the VNDB cover type so a "view all covers"
// gallery can group/filter: "" (unknown / user upload) / "main" (vn.image) /
// "pkgfront" / "dig" / "pkgback" / "pkgcontent" / "pkgside" / "pkgmed" (the
// VNDB release image types). VNDB syncs the full set; one is pinned (sort_order=0).
type GalgameCover struct {
	GalgameID int       `gorm:"column:galgame_id;primaryKey" json:"galgame_id"`
	ImageHash string    `gorm:"column:image_hash;type:char(64);primaryKey" json:"image_hash"`
	SortOrder int       `gorm:"column:sort_order;not null;default:0;index" json:"sort_order"`
	Sexual    int16     `gorm:"column:sexual;not null;default:0" json:"sexual"`
	Violence  int16     `gorm:"column:violence;not null;default:0" json:"violence"`
	Source    string    `gorm:"column:source;size:16;default:''" json:"source"`
	SourceKey string    `gorm:"column:source_key;size:128;default:''" json:"source_key"`
	Kind      string    `gorm:"column:kind;size:16;default:''" json:"kind"`
	Created   Timestamp `gorm:"column:created;autoCreateTime" json:"created"`

	// PortraitPinned marks this cover as the galgame's pinned PORTRAIT banner —
	// the second, vertical pin that runs ALONGSIDE the landscape sort_order=0 pin
	// (the portrait-first UI toggles between them). A partial unique index
	// `idx_galgame_cover_portrait_pinned` (galgame_id) WHERE portrait_pinned —
	// created by migrate-galgame — enforces "at most one pinned portrait per
	// galgame". Independent of sort_order (a portrait pin never touches
	// sort_order, so the landscape pin is untouched).
	//
	// json:"-": this flag is an internal derivation input, not part of the wire —
	// the read surface exposes the resolved `effective_portrait_hash` instead
	// (mirroring how per-cover state stays internal and effective_banner_hash is
	// the exposed landscape pin). Not migrated by AutoMigrate's default handling
	// beyond adding the column; the DB default (false) is intentional so the
	// out-of-band raw cover inserts (sync-vndb-covers / refping / portraitfill,
	// which omit this column) stay valid.
	PortraitPinned bool `gorm:"column:portrait_pinned;not null;default:false" json:"-"`

	// Width/Height/Thumbhash are NOT columns here — image bytes + intrinsic
	// metadata live in image_service (the single source of truth, keyed by
	// ImageHash). They are filled at READ time by the galgame service from a
	// batched, cached image-meta lookup so the frontend can render a blur-up
	// placeholder AND reserve the correct aspect ratio with no second
	// roundtrip and no layout shift. `gorm:"-"` → never read/written by SQL;
	// `omitempty` → absent (not 0/"") when the lookup is unavailable, so the
	// frontend falls back to a skeleton. See GalgameService image enrichment.
	Width     int    `gorm:"-" json:"width,omitempty"`
	Height    int    `gorm:"-" json:"height,omitempty"`
	Thumbhash string `gorm:"-" json:"thumbhash,omitempty"`
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
	Created   Timestamp `gorm:"column:created;autoCreateTime" json:"created"`

	// Width/Height/Thumbhash: transient image_service metadata filled at read
	// time (not columns). See GalgameCover for the full rationale.
	Width     int    `gorm:"-" json:"width,omitempty"`
	Height    int    `gorm:"-" json:"height,omitempty"`
	Thumbhash string `gorm:"-" json:"thumbhash,omitempty"`
}

func (GalgameScreenshot) TableName() string { return "galgame_screenshot" }

// PopulateEffectiveBanner fills g.EffectiveBannerHash from the loaded
// Cover list: the pinned cover (sort_order=0) when present, otherwise the
// lowest-sort_order cover as a fallback. The partial unique index keeps the
// sort_order=0 row unique per galgame, so the pinned case is unambiguous;
// the fallback covers the data quirk where a galgame's only cover ended up
// at a non-zero sort_order (the cover add/promote path) — without it such a
// galgame shows no head image at all.
//
// Idempotent and safe on a partially-loaded galgame (zero covers
// leaves EffectiveBannerHash nil; nil g is a no-op via the nil-receiver
// guard).
func PopulateEffectiveBanner(g *Galgame) {
	if g == nil {
		return
	}
	best := -1
	for i := range g.Cover {
		if g.Cover[i].SortOrder == 0 {
			best = i
			break
		}
		if best == -1 || g.Cover[i].SortOrder < g.Cover[best].SortOrder {
			best = i
		}
	}
	if best >= 0 {
		h := g.Cover[best].ImageHash
		g.EffectiveBannerHash = &h
	}
}

// PopulateEffectivePortrait fills g.EffectivePortraitHash from the loaded Cover
// list: the image_hash of the row flagged PortraitPinned. Unlike the landscape
// banner, there is NO fallback — a galgame with no pinned portrait leaves the
// field nil (the frontend applies its own fallback for landscape-only games,
// step 37). The partial unique index keeps the portrait_pinned row unique per
// galgame, so the pinned case is unambiguous.
//
// Idempotent; nil-receiver safe; zero-portrait leaves EffectivePortraitHash nil.
func PopulateEffectivePortrait(g *Galgame) {
	if g == nil {
		return
	}
	for i := range g.Cover {
		if g.Cover[i].PortraitPinned {
			h := g.Cover[i].ImageHash
			g.EffectivePortraitHash = &h
			return
		}
	}
}
