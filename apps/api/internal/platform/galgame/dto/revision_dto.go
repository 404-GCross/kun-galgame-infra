package dto

import "api/internal/platform/galgame/model"

// ListRevisionRequest represents a revision list query
type ListRevisionRequest struct {
	Page         int  `query:"page" validate:"min=1"`
	Limit        int  `query:"limit" validate:"min=1,max=50"`
	IncludeMinor bool `query:"include_minor"`
}

// RevertRequest represents a revert request
type RevertRequest struct {
	Revision int `json:"revision" validate:"required,min=1"`
}

// SubmitPRRequest represents a PR submission
type SubmitPRRequest struct {
	VNDBID           *string              `json:"vndb_id"`
	NameEnUS         *string              `json:"name_en_us"`
	NameJaJP         *string              `json:"name_ja_jp"`
	NameZhCN         *string              `json:"name_zh_cn"`
	NameZhTW         *string              `json:"name_zh_tw"`
	Banner           *string              `json:"banner"`
	BannerImageHash  *string              `json:"banner_image_hash" validate:"omitempty,len=64"`
	IntroEnUS        *string              `json:"intro_en_us"`
	IntroJaJP        *string              `json:"intro_ja_jp"`
	IntroZhCN        *string              `json:"intro_zh_cn"`
	IntroZhTW        *string              `json:"intro_zh_tw"`
	ContentLimit     *string              `json:"content_limit"`
	OriginalLanguage *string              `json:"original_language"`
	AgeLimit         *string              `json:"age_limit"`
	// ReleaseDate / ReleaseDateTBA: pointer-presence like other scalars.
	// nil = field omitted = inherit base; non-nil overwrites in the PR.
	ReleaseDate      *string              `json:"release_date" validate:"omitempty,datetime=2006-01-02"`
	ReleaseDateTBA   *bool                `json:"release_date_tba"`
	SeriesID         *int                 `json:"series_id"`
	Aliases          []string             `json:"aliases"`
	TagIDs           []int                `json:"tag_ids"`
	OfficialIDs     []int                `json:"official_ids"`
	EngineIDs        []int                `json:"engine_ids"`
	Links            []model.SnapshotLink `json:"links"`
	Covers           []model.SnapshotCover      `json:"covers"`
	Screenshots      []model.SnapshotScreenshot `json:"screenshots"`
	Note             string               `json:"note"`
}

// ApplyToSnapshot applies PR changes to a base snapshot, returning the proposed snapshot
func (r *SubmitPRRequest) ApplyToSnapshot(base *model.Snapshot) *model.Snapshot {
	s := *base // copy
	if r.VNDBID != nil {
		s.VNDBID = *r.VNDBID
	}
	if r.NameEnUS != nil {
		s.NameEnUS = *r.NameEnUS
	}
	if r.NameJaJP != nil {
		s.NameJaJP = *r.NameJaJP
	}
	if r.NameZhCN != nil {
		s.NameZhCN = *r.NameZhCN
	}
	if r.NameZhTW != nil {
		s.NameZhTW = *r.NameZhTW
	}
	if r.Banner != nil {
		s.Banner = *r.Banner
	}
	if r.BannerImageHash != nil {
		s.BannerImageHash = *r.BannerImageHash
	}
	if r.IntroEnUS != nil {
		s.IntroEnUS = *r.IntroEnUS
	}
	if r.IntroJaJP != nil {
		s.IntroJaJP = *r.IntroJaJP
	}
	if r.IntroZhCN != nil {
		s.IntroZhCN = *r.IntroZhCN
	}
	if r.IntroZhTW != nil {
		s.IntroZhTW = *r.IntroZhTW
	}
	if r.ContentLimit != nil {
		s.ContentLimit = *r.ContentLimit
	}
	if r.OriginalLanguage != nil {
		s.OriginalLanguage = *r.OriginalLanguage
	}
	if r.AgeLimit != nil {
		s.AgeLimit = *r.AgeLimit
	}
	if r.ReleaseDate != nil {
		// "" clears the date; non-empty must be valid YYYY-MM-DD (validated upstream).
		if *r.ReleaseDate == "" {
			s.ReleaseDate = nil
		} else {
			v := *r.ReleaseDate
			s.ReleaseDate = &v
		}
	}
	if r.ReleaseDateTBA != nil {
		s.ReleaseDateTBA = *r.ReleaseDateTBA
	}
	if r.SeriesID != nil {
		s.SeriesID = r.SeriesID
	}
	if r.Aliases != nil {
		s.Aliases = r.Aliases
	}
	if r.TagIDs != nil {
		s.TagIDs = r.TagIDs
	}
	if r.OfficialIDs != nil {
		s.OfficialIDs = r.OfficialIDs
	}
	if r.EngineIDs != nil {
		s.EngineIDs = r.EngineIDs
	}
	if r.Links != nil {
		s.Links = r.Links
	}
	if r.Covers != nil {
		s.Covers = r.Covers
	}
	if r.Screenshots != nil {
		s.Screenshots = r.Screenshots
	}
	return &s
}

// ListPRRequest represents a PR list query
type ListPRRequest struct {
	Page  int `query:"page" validate:"min=1"`
	Limit int `query:"limit" validate:"min=1,max=50"`
}
