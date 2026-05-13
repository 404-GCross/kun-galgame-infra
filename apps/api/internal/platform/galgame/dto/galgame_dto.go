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
	BannerImageHash  string `json:"banner_image_hash" validate:"omitempty,len=64"`
	IntroEnUS        string `json:"intro_en_us"`
	IntroJaJP        string `json:"intro_ja_jp"`
	IntroZhCN        string `json:"intro_zh_cn"`
	IntroZhTW        string `json:"intro_zh_tw"`
	ContentLimit     string `json:"content_limit" validate:"omitempty,oneof=sfw nsfw"`
	OriginalLanguage string `json:"original_language"`
	AgeLimit         string `json:"age_limit" validate:"omitempty,oneof=all r18"`
	SeriesID         *int   `json:"series_id"`
	Aliases          string `json:"aliases"` // Comma-separated alias names
	TagIDs           []int  `json:"tag_ids"`
	OfficialIDs      []int  `json:"official_ids"`
	EngineIDs        []int  `json:"engine_ids"`
}

// UpdateGalgameRequest represents a galgame update request
type UpdateGalgameRequest struct {
	VNDBID           *string `json:"vndb_id" validate:"omitempty,max=10"`
	NameEnUS         *string `json:"name_en_us" validate:"omitempty,max=1000"`
	NameJaJP         *string `json:"name_ja_jp" validate:"omitempty,max=1000"`
	NameZhCN         *string `json:"name_zh_cn" validate:"omitempty,max=1000"`
	NameZhTW         *string `json:"name_zh_tw" validate:"omitempty,max=1000"`
	Banner           *string `json:"banner"`
	BannerImageHash  *string `json:"banner_image_hash" validate:"omitempty,len=64"`
	IntroEnUS        *string `json:"intro_en_us"`
	IntroJaJP        *string `json:"intro_ja_jp"`
	IntroZhCN        *string `json:"intro_zh_cn"`
	IntroZhTW        *string `json:"intro_zh_tw"`
	ContentLimit     *string `json:"content_limit" validate:"omitempty,oneof=sfw nsfw"`
	OriginalLanguage *string `json:"original_language"`
	AgeLimit         *string `json:"age_limit" validate:"omitempty,oneof=all r18"`
	SeriesID         *int    `json:"series_id"`
	IsMinor          *bool   `json:"is_minor"`
}

// BatchGetGalgameRequest represents a batch galgame query
type BatchGetGalgameRequest struct {
	IDs []int `query:"ids" validate:"required,min=1,max=100"`
}

// GalgameBrief is a lightweight galgame info for cross-service display.
//
// status is included so callers can distinguish published (0) entries from
// the viewer's own pending/declined (3/4) returned when the request is
// authenticated. banner_image_hash is included so the caller can resolve
// the image_service-hosted variant URL — preferred over the legacy Banner
// string. Both fields work for any caller regardless of viewer.
type GalgameBrief struct {
	ID                 int     `json:"id"`
	VNDBID             string  `json:"vndb_id"`
	NameEnUS           string  `json:"name_en_us"`
	NameJaJP           string  `json:"name_ja_jp"`
	NameZhCN           string  `json:"name_zh_cn"`
	NameZhTW           string  `json:"name_zh_tw"`
	Banner             string  `json:"banner"`
	BannerImageHash    *string `json:"banner_image_hash,omitempty"`
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
