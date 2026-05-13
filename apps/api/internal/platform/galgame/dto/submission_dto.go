package dto

// SubmitGalgameRequest is the body of POST /galgame/submit.
//
// Unlike CreateGalgameRequest, vndb_id is OPTIONAL — user-submitted originals
// (no VNDB entry) may leave it empty. Resulting galgame has status=3.
// Daily quota enforced separately.
type SubmitGalgameRequest struct {
	// Optional. Empty allowed. When non-empty, must match `v\d+` and be globally unique.
	VNDBID           string `json:"vndb_id" validate:"omitempty,max=10"`
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
	Aliases          string `json:"aliases"`
	TagIDs           []int  `json:"tag_ids"`
	OfficialIDs     []int  `json:"official_ids"`
	EngineIDs        []int  `json:"engine_ids"`
}

// ListMineRequest is the query of GET /galgame/mine.
type ListMineRequest struct {
	// CSV of statuses to include. Default "3,4". Empty string means default.
	Status string `query:"status"`
	Page   int    `query:"page" validate:"omitempty,min=1"`
	Limit  int    `query:"limit" validate:"omitempty,min=1,max=50"`
}
