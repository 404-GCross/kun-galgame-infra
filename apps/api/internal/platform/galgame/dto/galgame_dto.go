package dto

// ListGalgameRequest represents a galgame list query
type ListGalgameRequest struct {
	Page      int    `query:"page" validate:"min=1"`
	Limit     int    `query:"limit" validate:"min=1,max=50"`
	SortField string `query:"sort_field" validate:"omitempty,oneof=created updated view resource_update_time"`
	SortOrder string `query:"sort_order" validate:"omitempty,oneof=asc desc"`
	Search    string `query:"search"`
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
	ReleaseDate      string `json:"release_date" validate:"omitempty,datetime=2006-01-02"`
	ReleaseDateTBA   bool   `json:"release_date_tba"`
	SeriesID         *int   `json:"series_id"`
	Aliases          string `json:"aliases"` // Comma-separated alias names
	TagIDs           []int  `json:"tag_ids"`
	OfficialIDs      []int  `json:"official_ids"`
	EngineIDs        []int  `json:"engine_ids"`
	// Cover candidate set (sort_order=0 = pinned banner). On Create the
	// empty default is "no covers yet"; downstream wizards typically
	// supply [{ImageHash, SortOrder: 0}] for a single initial banner.
	Covers           []GalgameCoverInput      `json:"covers"`
	Screenshots      []GalgameScreenshotInput `json:"screenshots"`
}

// GalgameLinkInput is one external link in a galgame edit body. Kept in
// the dto layer (not model.SnapshotLink) so dto stays model-free.
type GalgameLinkInput struct {
	Name string `json:"name" validate:"required,max=233"`
	Link string `json:"link" validate:"required,max=1007"`
}

// GalgameCoverInput is one cover candidate in a galgame edit body. The
// `image_hash` MUST point at an existing image in image_service (the
// service layer validates this before ApplySnapshot). `sort_order=0`
// designates the pinned banner — at most one cover per galgame may
// hold it (DB-enforced by partial unique index). Sexual / Violence
// are per-image content ratings (0 = unrated; see docs/galgame_wiki/09 §5.6).
type GalgameCoverInput struct {
	ImageHash string `json:"image_hash" validate:"required,len=64"`
	SortOrder int    `json:"sort_order"`
	Sexual    int16  `json:"sexual" validate:"omitempty,min=0,max=3"`
	Violence  int16  `json:"violence" validate:"omitempty,min=0,max=3"`
	Source    string `json:"source" validate:"omitempty,max=16"`
	SourceKey string `json:"source_key" validate:"omitempty,max=128"`
}

// GalgameScreenshotInput is one gallery / CG entry. Same shape as
// GalgameCoverInput plus Caption.
type GalgameScreenshotInput struct {
	ImageHash string `json:"image_hash" validate:"required,len=64"`
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
	ReleaseDate      *string `json:"release_date" validate:"omitempty,datetime=2006-01-02"`
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
	Links       *[]GalgameLinkInput `json:"links"`
	TagIDs      *[]int              `json:"tag_ids"`
	OfficialIDs *[]int              `json:"official_ids"`
	EngineIDs   *[]int              `json:"engine_ids"`
	// Covers / Screenshots use pointer-presence (nil = keep current
	// cover/screenshot set; non-nil incl. empty `[]` = authoritative full
	// replacement). Editing only the title MUST omit these or it will
	// wipe the gallery — same footgun as TagIDs (see §1.5 #5).
	Covers      *[]GalgameCoverInput      `json:"covers"`
	Screenshots *[]GalgameScreenshotInput `json:"screenshots"`
	IsMinor     *bool                     `json:"is_minor"`
}

// BatchGetGalgameRequest represents a batch galgame query
type BatchGetGalgameRequest struct {
	IDs []int `query:"ids" validate:"required,min=1,max=100"`
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

// UserBrief is a minimal user info for display purposes
type UserBrief struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Avatar string `json:"avatar"`
}
