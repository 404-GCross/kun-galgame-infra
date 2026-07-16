package dto

// NextMoe open-API public projection (step 02). These DTOs are the FROZEN v1
// public contract for the galgame face (`/v1/galgame/*`) — deliberately kept
// SEPARATE from the internal read DTOs (GalgameDetail / GalgameBrief) so the
// public wire can evolve on its own "add-only, never change" cadence
// (docs/developer-platform/01-design.md §3.5). Locale keys use BCP-47 lowercase
// (ja-jp / zh-cn / zh-tw / en-us); an empty localized string is surfaced as
// null (the key is always present). The aggregate record is a multi-source
// MERGE, not a per-source raw re-distribution (design §11): scores are per-source
// values + attribution links, everything else is the merged/curated projection.

// PublicNames is the localized-names object. Every key is present; an empty
// string becomes null (never "").
type PublicNames struct {
	JaJP *string `json:"ja-jp"`
	ZhCN *string `json:"zh-cn"`
	ZhTW *string `json:"zh-tw"`
	EnUS *string `json:"en-us"`
}

// PublicIntro is the localized-introduction object (include=intro). Same
// key-always-present / empty→null discipline as PublicNames.
type PublicIntro struct {
	ZhCN *string `json:"zh-cn"`
	EnUS *string `json:"en-us"`
	JaJP *string `json:"ja-jp"`
	ZhTW *string `json:"zh-tw"`
}

// PublicRefs is the per-source external-id block (裁定 3). Keys are always
// present; a source the galgame carries no id for is null. dlsite is
// intentionally absent from this face — a consumer reaches it via
// catalog_work_id → the catalog face's external-id lookup (design §3.3).
type PublicRefs struct {
	VNDB         *string `json:"vndb"`
	Bangumi      *string `json:"bangumi"`
	ErogameScape *string `json:"erogamescape"`
}

// PublicImage is one rendered image: a COMPLETE CDN URL (never a bare hash) plus
// its intrinsic dimensions and the ThumbHash blur-up placeholder. Kind is only
// meaningful on covers[] entries (the VNDB cover type); it is omitted elsewhere.
type PublicImage struct {
	URL       string `json:"url"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Thumbhash string `json:"thumbhash"`
	Kind      string `json:"kind,omitempty"`
}

// PublicImages is the images block. banner / portrait are the single effective
// pins (null when the galgame has none); covers is the full set, present only
// under include=covers. All entries are NSFW-filtered on the sfw face (裁定 2).
type PublicImages struct {
	Banner   *PublicImage  `json:"banner"`
	Portrait *PublicImage  `json:"portrait"`
	Covers   []PublicImage `json:"covers,omitempty"`
}

// PublicOfficial is a lightweight maker reference (id + name) in the taxonomy
// block; the full maker page lives behind the whitelisted officials endpoints.
type PublicOfficial struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// PublicTaxonomy is the name-level taxonomy block (include=taxonomy): tag names,
// maker references, engine names, and the series id. Deep detail is served by
// the whitelisted /v1/galgame/{tags,officials,engines,series} endpoints.
type PublicTaxonomy struct {
	Tags      []string         `json:"tags"`
	Officials []PublicOfficial `json:"officials"`
	Engines   []string         `json:"engines"`
	SeriesID  *int             `json:"series_id"`
}

// PublicAttribution is the per-source attribution-URL block (detail always
// carries it). Keys mirror PublicRefs: always present, null when the source id
// is absent. URLs are backend-composed from constant templates + the id.
type PublicAttribution struct {
	VNDB         *string `json:"vndb"`
	Bangumi      *string `json:"bangumi"`
	ErogameScape *string `json:"erogamescape"`
}

// PublicGalgame is the frozen v1 aggregate record — the body of
// GET /v1/galgame/{id}. include-gated blocks (intro / scores / covers /
// taxonomy) are omitted from the response entirely when not requested (thin,
// cache-friendly default). refs / attribution / catalog_work_id / updated keys
// are always present.
type PublicGalgame struct {
	ID               int                `json:"id"`
	Names            PublicNames        `json:"names"`
	Aliases          []string           `json:"aliases"`
	Intro            *PublicIntro       `json:"intro,omitempty"`
	ReleaseDate      *string            `json:"release_date"`
	ReleaseDateTBA   bool               `json:"release_date_tba"`
	OriginalLanguage string             `json:"original_language"`
	AgeLimit         string             `json:"age_limit"`
	Refs             PublicRefs         `json:"refs"`
	Scores           *GalgameScores     `json:"scores,omitempty"`
	Images           PublicImages       `json:"images"`
	Taxonomy         *PublicTaxonomy    `json:"taxonomy,omitempty"`
	CatalogWorkID    *int64             `json:"catalog_work_id"`
	Updated          string             `json:"updated"`
	Attribution      *PublicAttribution `json:"attribution,omitempty"`
}

// PublicGalgameItem is the thin list / batch / search item — a fixed subset of
// the aggregate record (design D1). The seven scalar/image keys are the frozen
// thin shape; Officials / Scores are the OPTIONAL list-level include expansions
// (step 07, add-only). Both are omitted entirely unless the caller asked for
// them via include=officials / include=scores, so the default thin item is
// byte-for-byte the frozen contract. A consumer that needs the full record still
// fetches /v1/galgame/{id} (or batch ?view=detail).
type PublicGalgameItem struct {
	ID          int          `json:"id"`
	Names       PublicNames  `json:"names"`
	ReleaseDate *string      `json:"release_date"`
	AgeLimit    string       `json:"age_limit"`
	Portrait    *PublicImage `json:"portrait"`
	Banner      *PublicImage `json:"banner"`
	Updated     string       `json:"updated"`
	// Officials is present (a possibly-empty array) only under include=officials;
	// the pointer distinguishes "not requested" (nil → key absent) from
	// "requested, none" ([] → key present). Same {id,name} shape as the detail
	// taxonomy block (05 frozen).
	Officials *[]PublicOfficial `json:"officials,omitempty"`
	// Scores is present only under include=scores — the same GalgameScores shape
	// (per-source values + attribution url) the detail scores block carries.
	Scores *GalgameScores `json:"scores,omitempty"`
}

// PublicListData is the cursor-paginated list envelope (GET /v1/galgame).
// next_cursor is null on the last page (no more rows). No total / offset — the
// public list is keyset-only (design §8: offset deep-paging has a perf cliff on
// a 60k+ catalog).
type PublicListData struct {
	Items      []PublicGalgameItem `json:"items"`
	NextCursor *string             `json:"next_cursor"`
}

// PublicSearchData is the search envelope (GET /v1/galgame/search): thin items
// in relevance order + the Meilisearch total. Page/limit, not cursor — search
// relevance ranking is not stable for keyset paging.
type PublicSearchData struct {
	Items []PublicGalgameItem `json:"items"`
	Total int64               `json:"total"`
}

// PublicBatchData is the batch envelope (GET /v1/galgame/batch, default): thin
// items for the requested ids. view=detail returns PublicBatchDetailData.
type PublicBatchData struct {
	Items []PublicGalgameItem `json:"items"`
}

// PublicBatchDetailData is the batch envelope under ?view=detail: full aggregate
// records (equivalent to detail with no include) for the requested ids.
type PublicBatchDetailData struct {
	Items []PublicGalgame `json:"items"`
}

// PublicChangeItem is one changes-stream entry: the id and its updated
// timestamp — nothing else. A consumer takes the ids and calls batch to hydrate.
type PublicChangeItem struct {
	ID      int    `json:"id"`
	Updated string `json:"updated"`
}

// PublicChangesData is the changes-stream envelope (GET /v1/galgame/changes):
// ids that changed, ascending by (updated, id), plus the cursor to resume from.
// next_cursor is always present (it advances past the last item even on a short
// page, so an incremental sync can poll the same cursor for new rows).
type PublicChangesData struct {
	Items      []PublicChangeItem `json:"items"`
	NextCursor *string            `json:"next_cursor"`
}

// nullIfEmpty maps "" → nil and any non-empty string → &s, for the empty→null
// locale-key discipline.
func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
