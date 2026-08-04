package dto

// public_series_dto.go — the series record on the frozen public face
// (GET /v1/catalog/series/{id}, wave 149c).

// PublicSeriesDetail is one work series: identity + its source anchor + the
// multilingual intro set, with the member works include-gated. intros / refs
// are always present (empty → [], never null).
type PublicSeriesDetail struct {
	ID int64 `json:"id"`
	// DisplayName is the source's series name verbatim; it falls back to the
	// external id when the source published no name.
	DisplayName string `json:"display_name"`
	// Refs is the series' EXACT identity anchor, same {source, external_id}
	// shape as every other identity face. A series anchors in-row (its unique
	// (source_id, external_id) key) rather than through catalog_external_ref,
	// so this list holds exactly one element today.
	Refs   []PublicCatalogRef  `json:"refs"`
	Intros []PublicSeriesIntro `json:"intros"`
	// Works are the series' member works (include=works), the LIVE galgame
	// fetchable set with r18 briefs dropped unless nsfw — the labels/{id} and
	// tags/{id} convention verbatim.
	Works      []PublicWorkBrief `json:"works,omitempty"`
	NextOffset *int              `json:"next_offset,omitempty"`
	// HasNSFW mirrors the browse lane's field (see PublicSeriesListItem): at
	// least one member work's DISPLAY material is nsfw, regardless of the
	// caller's nsfw setting. Present unconditionally, not include-gated — it is
	// one aggregate over membership the caller needs before deciding whether to
	// ask for works at all, and a detail page that had to request include=works
	// to learn it would be asking for the very list the flag exists to warn
	// about.
	HasNSFW bool `json:"has_nsfw"`
}

// PublicSeriesListData is one page of the series browse lane
// (GET /v1/catalog/series), shaped exactly like the labels / tags / engines
// lanes so a consumer that can walk one can walk all four.
type PublicSeriesListData struct {
	Items      []PublicSeriesListItem `json:"items"`
	Total      int64                  `json:"total"`
	NextCursor *string                `json:"next_cursor,omitempty"`
}

// PublicSeriesListItem is one row of that lane. It carries identity plus the
// same nsfw-aware work_count the other three lanes publish — the number of
// works the SAME caller would page through via works?series_id=<id>.
type PublicSeriesListItem struct {
	ID          int64  `json:"id"`
	DisplayName string `json:"display_name"`
	// Source is the catalog_source KEY (public-face convention — never the
	// numeric source_id), matching the refs[] on the detail record.
	Source    string `json:"source"`
	WorkCount int    `json:"work_count"`
	// HasNSFW says at least one work in this series has content_limit = nsfw —
	// the DISPLAY axis (model.DisplayLimitKey), which is what "nsfw" means on
	// this face. It is NOT the age axis that work_count's nsfw parameter gates.
	//
	// The two are different questions and they disagree in bulk: of the live
	// claimed galgame works 61,690 are r18 while only 13,664 are editorially
	// nsfw, so a badge read off content_rating would over-mark by 4.5x — 48,299
	// works whose display material an editor judged safe to show. Collapsing the
	// axes the other way is what once hid 5,568 works from a downstream.
	//
	// Counted over the SAME population as work_count but WITHOUT the caller's
	// nsfw filter, so an sfw caller still learns the series leads somewhere it
	// is being shielded from. A flag derived from the filtered count could not:
	// it would read false for exactly the callers who need it.
	HasNSFW bool `json:"has_nsfw"`
}

// PublicSeriesIntro is one description of a series. source is the catalog_source
// key (public-face convention — never the numeric source_id). Unlike the label
// / tag intro sets this one is NOT merged to one row per language; see
// seriesIntros for why.
type PublicSeriesIntro struct {
	Lang   string `json:"lang"`
	Intro  string `json:"intro"`
	Source string `json:"source"`
}
