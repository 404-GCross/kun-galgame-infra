package dto

// ListGalgameRequest represents a galgame list query.
//
// ContentLimit accepts "sfw" / "nsfw" / "all" / "" — see
// docs/integration/galgame_wiki/00-handbook-for-downstream.md §NSFW. The
// service layer resolves "" to the safe-by-default "sfw".
//
// ReleasedFrom / ReleasedTo accept "YYYY" or "YYYY-MM" — year-only is
// backward-compatible with the search endpoint's historical int contract;
// month-precision lets the frontend support "2024 年 3 月发售" filter
// chips. Empty = no filter. See utils.ParseReleaseLowerBound /
// ParseReleaseUpperBound for normalization (leap years + 30/31-day
// months handled). The SQL filter is `WHERE release_date >= ?` against
// the indexed `release_date` column (btree), so range queries are O(log N).
//
// ReleasedMonths is a comma-separated set of calendar months (1-12), an
// orthogonal AND filter layered on the year range — e.g.
// released_from=2020&released_to=2024&released_months=3,7 = "March/July
// releases across 2020–2024". Empty = no month filter. See
// utils.ParseMonthSet. The SQL clause is `EXTRACT(MONTH FROM release_date)
// IN (...)`: non-sargable, but applied as a cheap recheck on rows the
// year-range btree scan already narrowed.
type ListGalgameRequest struct {
	Page           int    `query:"page" validate:"min=1"`
	Limit          int    `query:"limit" validate:"min=1,max=50"`
	SortField      string `query:"sort_field" validate:"omitempty,oneof=created updated view resource_update_time release_date"`
	SortOrder      string `query:"sort_order" validate:"omitempty,oneof=asc desc"`
	Search         string `query:"search"`
	ContentLimit   string `query:"content_limit" validate:"omitempty,oneof=sfw nsfw all"`
	ReleasedFrom   string `query:"released_from"`
	ReleasedTo     string `query:"released_to"`
	ReleasedMonths string `query:"released_months"`
}

// CreateGalgameRequest represents a galgame creation request
type CreateGalgameRequest struct {
	VNDBID           string `json:"vndb_id" validate:"required,max=10"`
	NameEnUS         string `json:"name_en_us" validate:"max=1000"`
	NameJaJP         string `json:"name_ja_jp" validate:"max=1000"`
	NameZhCN         string `json:"name_zh_cn" validate:"max=1000"`
	NameZhTW         string `json:"name_zh_tw" validate:"max=1000"`
	Banner           string `json:"banner"`
	// PromoteCoverHash is the image_service hash uploaded via multipart
	// `file` field on this Create. Handler sets it after the upload
	// succeeds; service merges it into Covers as sort_order=0 (pinned
	// banner). Not part of the JSON body — clients sending JSON should
	// use Covers directly.
	PromoteCoverHash string `json:"-"`
	IntroEnUS        string `json:"intro_en_us"`
	IntroJaJP        string `json:"intro_ja_jp"`
	IntroZhCN        string `json:"intro_zh_cn"`
	IntroZhTW        string `json:"intro_zh_tw"`
	ContentLimit     string `json:"content_limit" validate:"omitempty,oneof=sfw nsfw"`
	OriginalLanguage string `json:"original_language"`
	AgeLimit         string `json:"age_limit" validate:"omitempty,oneof=all r18"`
	// ReleaseDate is "YYYY-MM-DD" or "" (= unknown). Empty / omitted →
	// no date is recorded. Independent of ReleaseDateTBA.
	ReleaseDate      string `json:"release_date" validate:"omitempty,date_or_empty"`
	ReleaseDateTBA   bool   `json:"release_date_tba"`
	SeriesID         *int   `json:"series_id"`
	Aliases          string `json:"aliases"` // Comma-separated alias names
	TagIDs           []int  `json:"tag_ids"`
	OfficialIDs      []int  `json:"official_ids"`
	EngineIDs        []int  `json:"engine_ids"`
	// Cover candidate set (sort_order=0 = pinned banner). On Create the
	// empty default is "no covers yet"; downstream wizards typically
	// supply [{ImageHash, SortOrder: 0}] for a single initial banner.
	// `dive` is required for the element-level validations below to run —
	// without it the validator does not descend into slice-of-struct.
	Covers           []GalgameCoverInput      `json:"covers" validate:"omitempty,dive"`
	Screenshots      []GalgameScreenshotInput `json:"screenshots" validate:"omitempty,dive"`
}

// GalgameLinkInput is one external link in a galgame edit body. Kept in
// the dto layer (not model.SnapshotLink) so dto stays model-free.
type GalgameLinkInput struct {
	Name string `json:"name" validate:"required,max=107"`
	Link string `json:"link" validate:"required,max=500"`
}

// GalgameCoverInput is one cover candidate in a galgame edit body. The
// `image_hash` MUST point at an existing image in image_service (the
// service layer validates this before ApplySnapshot). `sort_order=0`
// designates the pinned banner — at most one cover per galgame may
// hold it (DB-enforced by partial unique index). Sexual / Violence
// are per-image content ratings (0 = unrated; see docs/galgame_wiki/09 §5.6).
type GalgameCoverInput struct {
	ImageHash string `json:"image_hash" validate:"required,len=64,hexadecimal"`
	SortOrder int    `json:"sort_order"`
	Sexual    int16  `json:"sexual" validate:"omitempty,min=0,max=3"`
	Violence  int16  `json:"violence" validate:"omitempty,min=0,max=3"`
	Source    string `json:"source" validate:"omitempty,max=16"`
	SourceKey string `json:"source_key" validate:"omitempty,max=128"`
	// Kind labels the VNDB cover type (main/pkgfront/dig/pkgback/…); "" for
	// user uploads. Sync-managed covers are preserved on edit, so downstream
	// echoes it back as-is (or omits it — the wiki re-applies the vndb set).
	Kind string `json:"kind" validate:"omitempty,max=16"`
}

// GalgameScreenshotInput is one gallery / CG entry. Same shape as
// GalgameCoverInput plus Caption.
type GalgameScreenshotInput struct {
	ImageHash string `json:"image_hash" validate:"required,len=64,hexadecimal"`
	SortOrder int    `json:"sort_order"`
	Caption   string `json:"caption"`
	Sexual    int16  `json:"sexual" validate:"omitempty,min=0,max=3"`
	Violence  int16  `json:"violence" validate:"omitempty,min=0,max=3"`
	Source    string `json:"source" validate:"omitempty,max=16"`
	SourceKey string `json:"source_key" validate:"omitempty,max=128"`
}

// UpdateGalgameRequest represents a galgame update request
type UpdateGalgameRequest struct {
	VNDBID           *string `json:"vndb_id" validate:"omitempty,max=10"`
	NameEnUS         *string `json:"name_en_us" validate:"omitempty,max=1000"`
	NameJaJP         *string `json:"name_ja_jp" validate:"omitempty,max=1000"`
	NameZhCN         *string `json:"name_zh_cn" validate:"omitempty,max=1000"`
	NameZhTW         *string `json:"name_zh_tw" validate:"omitempty,max=1000"`
	Banner           *string `json:"banner"`
	// PromoteCoverHash mirrors CreateGalgameRequest.PromoteCoverHash:
	// the handler sets it from a multipart-uploaded banner file. Service
	// merges it as sort_order=0 in the resulting snapshot, demoting any
	// existing pinned cover to keep the partial-unique index happy.
	// Not part of the JSON body.
	PromoteCoverHash string  `json:"-"`
	IntroEnUS        *string `json:"intro_en_us"`
	IntroJaJP        *string `json:"intro_ja_jp"`
	IntroZhCN        *string `json:"intro_zh_cn"`
	IntroZhTW        *string `json:"intro_zh_tw"`
	ContentLimit     *string `json:"content_limit" validate:"omitempty,oneof=sfw nsfw"`
	OriginalLanguage *string `json:"original_language"`
	AgeLimit         *string `json:"age_limit" validate:"omitempty,oneof=all r18"`
	// ReleaseDate / ReleaseDateTBA both use pointer-presence: nil = field
	// omitted = keep current; non-nil overwrites (incl. &"" = clear date
	// to unknown). The two are independent and overlay separately.
	ReleaseDate      *string `json:"release_date" validate:"omitempty,date_or_empty"`
	ReleaseDateTBA   *bool   `json:"release_date_tba"`
	SeriesID         *int    `json:"series_id"`
	// Relational / multi-value fields use POINTER types for presence
	// semantics, mirroring the *string scalars above: nil = field
	// omitted = keep the galgame's current set; non-nil (including an
	// empty []) = authoritative full replacement. A partial edit that
	// does not send these therefore never touches them (no silent wipe).
	// EVERY editable field in model.Snapshot is reachable here — see the
	// invariant in docs/galgame_wiki/01-revision-system-design.md §1.5
	// (`bid`/BangumiID is the only reserved exception: sync-managed,
	// intentionally not user-editable; Bangumi sync is deferred).
	Aliases     *[]string           `json:"aliases"`
	// `dive` descends into the slice elements so GalgameLinkInput's
	// required/max tags actually run (the pointer + omitempty skips when nil).
	Links       *[]GalgameLinkInput `json:"links" validate:"omitempty,dive"`
	TagIDs      *[]int              `json:"tag_ids"`
	OfficialIDs *[]int              `json:"official_ids"`
	EngineIDs   *[]int              `json:"engine_ids"`
	// Covers / Screenshots use pointer-presence (nil = keep current
	// cover/screenshot set; non-nil incl. empty `[]` = authoritative full
	// replacement). Editing only the title MUST omit these or it will
	// wipe the gallery — same footgun as TagIDs (see §1.5 #5).
	Covers      *[]GalgameCoverInput      `json:"covers" validate:"omitempty,dive"`
	Screenshots *[]GalgameScreenshotInput `json:"screenshots" validate:"omitempty,dive"`
	IsMinor     *bool                     `json:"is_minor"`
}

// BatchGetGalgameRequest represents a batch galgame query.
//
// ContentLimit follows the same wire contract as ListGalgameRequest, but
// the default-when-empty for /galgame/batch is "" (no filter) — callers
// already know which specific IDs they want (e.g. favorites list), so
// safe-by-default would silently hide entries the caller explicitly
// requested. Pass content_limit=sfw to opt INTO filtering.
type BatchGetGalgameRequest struct {
	IDs          []int  `query:"ids" validate:"required,min=1,max=100"`
	ContentLimit string `query:"content_limit" validate:"omitempty,oneof=sfw nsfw all"`
}

// GalgameBrief is a lightweight galgame info for cross-service display.
//
// status is included so callers can distinguish published (0) entries from
// the viewer's own pending/declined (3/4) returned when the request is
// authenticated. effective_banner_hash carries the image_service hash of
// the pinned cover (sort_order=0), preferred over the legacy Banner URL
// for thumbnail rendering. Both fields work for any caller regardless of viewer.
type GalgameBrief struct {
	ID                 int     `json:"id"`
	VNDBID             string  `json:"vndb_id"`
	NameEnUS           string  `json:"name_en_us"`
	NameJaJP           string  `json:"name_ja_jp"`
	NameZhCN           string  `json:"name_zh_cn"`
	NameZhTW           string  `json:"name_zh_tw"`
	Banner             string  `json:"banner"`
	// EffectiveBannerHash is the image_hash of the pinned cover
	// (sort_order=0) — the "currently shown" banner. Derived from
	// galgame_cover during the BatchGet query; nil when the galgame has
	// no covers yet. The legacy banner_image_hash column was retired
	// by PR5, so this is now the SOLE image-service banner reference.
	EffectiveBannerHash *string `json:"effective_banner_hash,omitempty"`
	ContentLimit       string  `json:"content_limit"`
	Status             int     `json:"status"`
	UserID             int     `json:"user_id"`
	ResourceUpdateTime string  `json:"resource_update_time"`
	OriginalLanguage   string  `json:"original_language"`
	AgeLimit           string  `json:"age_limit"`
}

// CheckVNDBRequest represents a VNDB existence check
type CheckVNDBRequest struct {
	VNDBID string `query:"vndb_id" validate:"required"`
}

// UserGalgameStats holds aggregated galgame statistics for a user
type UserGalgameStats struct {
	GalgameCreated      int `json:"galgame_created"`
	GalgameCreatedToday int `json:"galgame_created_today"`
	GalgameContributed  int `json:"galgame_contributed"`
	RevisionCount       int `json:"revision_count"`
	PRSubmitted         int `json:"pr_submitted"`
	PRMerged            int `json:"pr_merged"`
	PRDeclined          int `json:"pr_declined"`
	PRPending           int `json:"pr_pending"`
}

// UserBrief is a minimal user info for display purposes.
//
// Avatar is the legacy URL; AvatarImageHash is the image_service content hash
// (set on new uploads). The frontend resolves the right URL/variant from the
// hash (resolveAvatarUrl) — KunAvatar ≥0.3.4 renders the URL as-is and no
// longer derives thumbnail variants, so small avatars (actor/contributor) need
// the hash here to build the `_100` variant.
type UserBrief struct {
	ID              int     `json:"id"`
	Name            string  `json:"name"`
	Avatar          string  `json:"avatar"`
	AvatarImageHash *string `json:"avatar_image_hash,omitempty"`
}
