package dto

// ListGalgameRequest represents a galgame list query
type ListGalgameRequest struct {
	Page      int    `query:"page" validate:"min=1"`
	Limit     int    `query:"limit" validate:"min=1,max=50"`
	SortField string `query:"sortField" validate:"omitempty,oneof=created updated view"`
	SortOrder string `query:"sortOrder" validate:"omitempty,oneof=asc desc"`
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
	IntroEnUS        *string `json:"intro_en_us"`
	IntroJaJP        *string `json:"intro_ja_jp"`
	IntroZhCN        *string `json:"intro_zh_cn"`
	IntroZhTW        *string `json:"intro_zh_tw"`
	ContentLimit     *string `json:"content_limit" validate:"omitempty,oneof=sfw nsfw"`
	OriginalLanguage *string `json:"original_language"`
	AgeLimit         *string `json:"age_limit" validate:"omitempty,oneof=all r18"`
	SeriesID         *int    `json:"series_id"`
}

// CheckVNDBRequest represents a VNDB existence check
type CheckVNDBRequest struct {
	VNDBID string `query:"vndb_id" validate:"required"`
}

// UserBrief is a minimal user info for display purposes
type UserBrief struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Avatar string `json:"avatar"`
}
