package search

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"api/internal/infrastructure/search"

	"github.com/meilisearch/meilisearch-go"
)

// Service wraps the Meilisearch client with request-parsing + response-shaping.
type Service struct {
	client *search.Client
}

// NewService creates a search Service.
func NewService(client *search.Client) *Service {
	return &Service{client: client}
}

// ─────────────────────────── Galgame search ───────────────────────────

// GalgameSearchRequest represents an incoming galgame search.
// All slice fields are empty == "no filter on this attribute".
type GalgameSearchRequest struct {
	Query string

	Statuses          []int    // empty → no filter (admin can view all). default at handler layer.
	ContentLimit      string   // "sfw" / "nsfw" / ""
	AgeLimit          string   // "all" / "r18" / ""
	OriginalLanguages []string // OR
	TagIDs            []int    // AND — every galgame must have all
	OfficialIDs       []int    // AND
	EngineIDs         []int    // AND
	SeriesID *int // nil = no filter
	// ReleasedFromTS / ReleasedToTS are Unix-second boundaries on the
	// galgame's release date (inclusive on both ends). Handler accepts
	// either YYYY or YYYY-MM in the query string and converts via
	// utils.ParseReleaseLowerBound / ParseReleaseUpperBound — so
	// year-only callers (the historical API) still work, but the new
	// month-precision callers can pass `released_from=2024-03&released_to=2024-08`.
	// 0 = no filter.
	ReleasedFromTS int64
	ReleasedToTS   int64
	// ReleasedMonths is a set of calendar months (1-12), an orthogonal AND
	// filter on the year range — `released_month IN [...]`. Empty = no
	// month filter. Handler resolves the CSV via utils.ParseMonthSet.
	ReleasedMonths []int

	IncludeIntro bool

	Sort  string // "relevance" / "released_desc" / "released_asc" / "view" / "updated"
	Page  int    // 1-based
	Limit int

	WantFacets    bool
	WantHighlight bool

	// Fields restricts the returned attributes (Meilisearch
	// attributesToRetrieve). Empty = all attributes. Useful for list views
	// that don't need intro_* etc — cuts response size dramatically.
	Fields []string

	// IncludePending, when true and ViewerUID > 0, runs an extra search for
	// the user's own status ∈ {3,4} entries matching the same query and
	// returns them in GalgameSearchResponse.Pending.
	IncludePending bool
	// ViewerUID is the authenticated user's id. 0 means anonymous —
	// include_pending is ignored.
	ViewerUID int
}

// GalgameSearchResponse mirrors what we return in the API.
type GalgameSearchResponse struct {
	Items            []map[string]any          `json:"items"`
	Total            int64                     `json:"total"`
	Facets           map[string]map[string]int `json:"facets,omitempty"`
	ProcessingTimeMS int64                     `json:"processing_time_ms"`
	// Pending is populated only when the request had include_pending=true
	// and a viewer_uid. Returns the user's own status=3/4 entries that
	// match the same query. Empty otherwise.
	Pending []map[string]any `json:"pending,omitempty"`
}

// nonIntroSearchable is the DEFAULT free-text search scope (applied via
// attributesToSearchOn). It deliberately covers only the identity fields —
// vndb_id, localized names, aliases — and EXCLUDES:
//   - intro_* : long markdown bodies, only searched when include_intro=true;
//   - tag_names / official_names : ③ precision fix. These flooded results —
//     a query word that happens to be a tag/studio name matched that tag's or
//     studio's WHOLE catalog, drowning the by-name hits. Browse-by-tag/studio
//     is served by the tag_ids/official_ids filters and the dedicated tag /
//     official search indexes instead.
//
// tag_names/official_names stay in galgamesSettings().SearchableAttributes so
// an explicit include_intro=true broad search can still reach them.
var nonIntroSearchable = []string{
	"vndb_id",
	"name_zh_cn", "name_ja_jp", "name_en_us", "name_zh_tw",
	"aliases",
}

// galgameSearchScoreThreshold is the minimum Meilisearch _rankingScore (0..1)
// a hit must clear to be returned (② relevance floor). It drops the long tail
// of barely-relevant matches. Calibrated against real queries; raise to
// tighten precision, lower to recover recall.
const galgameSearchScoreThreshold = 0.4

// SearchGalgames runs a galgame search.
func (s *Service) SearchGalgames(ctx context.Context, req *GalgameSearchRequest) (*GalgameSearchResponse, error) {
	req.normalize()

	// VNDB-ID exact lookup. A query that is exactly "v<digits>" means the user
	// wants that ONE galgame by its unique vndb_id — resolve it with an exact
	// filter rather than full-text. Full-text would prefix-bleed ("v1965" also
	// matches v19650/v19658) and depends on tokenization; an exact filter is
	// single, deterministic, and case-insensitive.
	if vid := normalizeVNDBID(req.Query); vid != "" {
		return s.lookupByVNDBID(ctx, vid, req)
	}

	msReq := &meilisearch.SearchRequest{
		Page:        int64(req.Page),
		HitsPerPage: int64(req.Limit),
		// ① Require ALL query terms to be present. CJK is the reason: with the
		// previous Frequency strategy (drop the most-common term, retry) a
		// partial-title query segmented into common tokens — "时间奏响的" →
		// 时间 / 奏响 / (的, now a stop word) — and any single shared token
		// (时间) was enough to surface unrelated titles ("秘密的时间", …), while
		// the full title diluted those matches below the score floor → 0. All
		// flips that: a title that isn't actually in the corpus returns
		// nothing instead of common-token noise. Trade-off is lower recall (a
		// query word absent from every title yields 0), which is the right
		// call for title lookup. StopWords (settings.go) keep particles like
		// 的/之 from becoming spuriously-required terms under this strategy.
		MatchingStrategy: meilisearch.All,
		// ② Drop weak matches below the relevance floor.
		RankingScoreThreshold: galgameSearchScoreThreshold,
	}

	// Filter
	filter := buildGalgameFilter(req)
	if filter != "" {
		msReq.Filter = filter
	}

	// Sort
	if sortRule := galgameSortRule(req.Sort); sortRule != "" {
		msReq.Sort = []string{sortRule}
	}

	// Facets
	if req.WantFacets {
		msReq.Facets = []string{"age_limit", "original_language"}
	}

	// Highlight
	if req.WantHighlight {
		msReq.AttributesToHighlight = []string{
			"name_zh_cn", "name_ja_jp", "name_en_us", "name_zh_tw", "aliases",
		}
		msReq.HighlightPreTag = "<mark>"
		msReq.HighlightPostTag = "</mark>"
	}

	// By default exclude intro_* from search. Leaving AttributesToSearchOn nil
	// when include_intro=true makes MS use the full searchable list (which
	// includes intro_*). Otherwise restrict to the non-intro subset.
	if !req.IncludeIntro {
		msReq.AttributesToSearchOn = nonIntroSearchable
	}

	// Field projection. When the caller specifies, only those attributes
	// come back in the hits — saves bandwidth on list views that don't
	// need intro_* (each can be 1-10KB markdown).
	if len(req.Fields) > 0 {
		msReq.AttributesToRetrieve = req.Fields
	}

	resp, err := s.client.Index(IndexGalgames).SearchWithContext(ctx, req.Query, msReq)
	if err != nil {
		return nil, err
	}

	out := toGalgameResponse(resp)

	// Second-pass pending search. Cheap (small result set, narrow filter),
	// runs only when caller opted in AND authenticated. Failure here is
	// non-fatal: we still return the main result.
	//
	// Matches the main query: NSFW (content_limit) is NOT filtered — the
	// caller's own pending drafts come back regardless of NSFW, same as the
	// main results. (SEO is handled by page-level noindex, not search filters.)
	if req.IncludePending && req.ViewerUID > 0 {
		pendingFilter := fmt.Sprintf(
			"status IN [%d, %d] AND user_id = %d",
			3, 4, req.ViewerUID,
		)
		pendingReq := &meilisearch.SearchRequest{
			Page:        1,
			HitsPerPage: 20,
			Filter:      pendingFilter,
			// Same matching as the main pass. No score threshold here: this is
			// the viewer's own small, time-sorted draft set — keep recall high.
			MatchingStrategy: meilisearch.All,
		}
		// Keep the same Highlight / Fields config so consumers can render
		// pending entries with the same shape. Sort by latest first.
		pendingReq.Sort = []string{"updated_ts:desc"}
		if len(req.Fields) > 0 {
			pendingReq.AttributesToRetrieve = req.Fields
		}
		if !req.IncludeIntro {
			pendingReq.AttributesToSearchOn = nonIntroSearchable
		}
		if pendingResp, err := s.client.Index(IndexGalgames).SearchWithContext(ctx, req.Query, pendingReq); err == nil {
			out.Pending = make([]map[string]any, 0, len(pendingResp.Hits))
			for _, h := range pendingResp.Hits {
				out.Pending = append(out.Pending, hitToMap(h))
			}
		}
	}

	return out, nil
}

// ─────────────────────────── Tag search ───────────────────────────

type TagSearchRequest struct {
	Query    string
	Category string // "content" / "sexual" / "technical" / ""
	Limit    int
}

type EntitySearchResponse[T any] struct {
	Items            []T   `json:"items"`
	Total            int64 `json:"total"`
	ProcessingTimeMS int64 `json:"processing_time_ms"`
}

// SearchTags runs a tag search.
func (s *Service) SearchTags(ctx context.Context, req *TagSearchRequest) (*EntitySearchResponse[TagDoc], error) {
	normalizeLimit(&req.Limit, 50, 100)
	req.Query = sanitizeQuery(req.Query)

	msReq := &meilisearch.SearchRequest{
		HitsPerPage: int64(req.Limit),
	}
	if req.Category != "" {
		msReq.Filter = fmt.Sprintf("category = '%s'", escapeFilter(req.Category))
	}
	// Empty query → return top by galgame_count
	if req.Query == "" {
		msReq.Sort = []string{"galgame_count:desc"}
	}

	resp, err := s.client.Index(IndexTags).SearchWithContext(ctx, req.Query, msReq)
	if err != nil {
		return nil, err
	}

	items := decodeHits[TagDoc](resp.Hits)
	return &EntitySearchResponse[TagDoc]{
		Items:            items,
		Total:            totalHits(resp),
		ProcessingTimeMS: resp.ProcessingTimeMs,
	}, nil
}

// ─────────────────────────── Official search ───────────────────────────

type OfficialSearchRequest struct {
	Query    string
	Category string
	Lang     string
	Limit    int
}

// SearchOfficials runs an official search.
func (s *Service) SearchOfficials(ctx context.Context, req *OfficialSearchRequest) (*EntitySearchResponse[OfficialDoc], error) {
	normalizeLimit(&req.Limit, 50, 100)
	req.Query = sanitizeQuery(req.Query)

	msReq := &meilisearch.SearchRequest{
		HitsPerPage: int64(req.Limit),
	}

	filters := []string{}
	if req.Category != "" {
		filters = append(filters, fmt.Sprintf("category = '%s'", escapeFilter(req.Category)))
	}
	if req.Lang != "" {
		filters = append(filters, fmt.Sprintf("lang = '%s'", escapeFilter(req.Lang)))
	}
	if len(filters) > 0 {
		msReq.Filter = strings.Join(filters, " AND ")
	}

	if req.Query == "" {
		msReq.Sort = []string{"galgame_count:desc"}
	}

	resp, err := s.client.Index(IndexOfficials).SearchWithContext(ctx, req.Query, msReq)
	if err != nil {
		return nil, err
	}

	items := decodeHits[OfficialDoc](resp.Hits)
	return &EntitySearchResponse[OfficialDoc]{
		Items:            items,
		Total:            totalHits(resp),
		ProcessingTimeMS: resp.ProcessingTimeMs,
	}, nil
}

// ─────────────────────────── helpers ───────────────────────────

func (r *GalgameSearchRequest) normalize() {
	r.Query = sanitizeQuery(r.Query)
	if r.Page < 1 {
		r.Page = 1
	}
	normalizeLimit(&r.Limit, 24, 100)
}

// sanitizeQuery neutralizes Meilisearch's full-text query-string operators so a
// plain title/name search box can't silently sabotage itself. Meilisearch (since
// v1.8) treats a leading '-' on a term as NEGATION ("-word" excludes documents
// containing that word) and '"' as phrase delimiters — and exposes NO
// server-side switch to turn them off. VNDB titles routinely use the
// "-サブタイトル-" convention (e.g. `CRAZY CHA!N -エルピスの鎖-`): pasted verbatim
// into the search box, `-エルピスの鎖` was parsed as "exclude エルピスの鎖",
// excluding the very game being searched — so searching a game by its exact name
// returned zero (or the wrong) results. Both '-' and '"' are already tokenizer
// separators, so mapping them to spaces is lossless for matching while stripping
// the accidental operators. This is a title-lookup box, not an advanced-query
// DSL, so no expressive power is lost. Only ASCII '-' (U+002D) triggers negation;
// the Japanese long-vowel mark 'ー' (U+30FC) is a letter and left untouched.
func sanitizeQuery(q string) string {
	if strings.ContainsAny(q, "-\"") {
		q = strings.Map(func(r rune) rune {
			if r == '-' || r == '"' {
				return ' '
			}
			return r
		}, q)
	}
	return strings.TrimSpace(q)
}

func normalizeLimit(limit *int, def, max int) {
	if *limit <= 0 {
		*limit = def
	}
	if *limit > max {
		*limit = max
	}
}

// normalizeVNDBID returns the lowercased VNDB id when q is EXACTLY a VNDB id
// ("v" followed by one or more digits, case-insensitive), else "". Used to
// detect when a search query should resolve to a single galgame by vndb_id.
func normalizeVNDBID(q string) string {
	q = strings.TrimSpace(q)
	if len(q) < 2 || (q[0] != 'v' && q[0] != 'V') {
		return ""
	}
	for i := 1; i < len(q); i++ {
		if q[i] < '0' || q[i] > '9' {
			return ""
		}
	}
	return strings.ToLower(q)
}

// lookupByVNDBID returns the single galgame whose unique vndb_id == vid via an
// exact Meilisearch filter. It still applies the caller's status/age/language/
// entity filters (so an unpublished or otherwise out-of-scope game stays
// hidden), and returns the normal empty-result shape when nothing matches.
// vid is pre-validated to [v0-9]+ by normalizeVNDBID, so it's filter-safe.
func (s *Service) lookupByVNDBID(ctx context.Context, vid string, req *GalgameSearchRequest) (*GalgameSearchResponse, error) {
	filter := fmt.Sprintf("vndb_id = '%s'", vid)
	if base := buildGalgameFilter(req); base != "" {
		filter += " AND " + base
	}
	msReq := &meilisearch.SearchRequest{
		Page:        1,
		HitsPerPage: 1, // vndb_id is unique → at most one match
		Filter:      filter,
	}
	if len(req.Fields) > 0 {
		msReq.AttributesToRetrieve = req.Fields
	}
	resp, err := s.client.Index(IndexGalgames).SearchWithContext(ctx, "", msReq)
	if err != nil {
		return nil, err
	}
	return toGalgameResponse(resp), nil
}

// buildGalgameFilter produces a Meilisearch filter string. Empty string = no filter.
func buildGalgameFilter(r *GalgameSearchRequest) string {
	var clauses []string

	if len(r.Statuses) > 0 {
		clauses = append(clauses, inIntFilter("status", r.Statuses))
	}
	// NSFW (content_limit) is intentionally NOT filtered in search — search must
	// surface ALL games regardless of NSFW (product decision). Keeping R18 out of
	// search engines is handled at the PAGE layer via noindex (R18 detail pages +
	// search-result pages), not by filtering search results. r.ContentLimit is
	// therefore ignored here on purpose; do not re-add a content_limit clause.
	if r.AgeLimit != "" {
		clauses = append(clauses, fmt.Sprintf("age_limit = '%s'", escapeFilter(r.AgeLimit)))
	}
	if len(r.OriginalLanguages) > 0 {
		clauses = append(clauses, inStringFilter("original_language", r.OriginalLanguages))
	}
	// tag/official/engine IDs are AND — each must be present (one term each).
	for _, id := range r.TagIDs {
		clauses = append(clauses, fmt.Sprintf("tag_ids = %d", id))
	}
	for _, id := range r.OfficialIDs {
		clauses = append(clauses, fmt.Sprintf("official_ids = %d", id))
	}
	for _, id := range r.EngineIDs {
		clauses = append(clauses, fmt.Sprintf("engine_ids = %d", id))
	}
	if r.SeriesID != nil {
		clauses = append(clauses, fmt.Sprintf("series_id = %d", *r.SeriesID))
	}
	// Release date range — Unix-second comparison against the indexed
	// `released_ts` field. Handler resolved YYYY / YYYY-MM via utils into
	// these int64 bounds (0 = caller didn't pass that side).
	if r.ReleasedFromTS != 0 {
		clauses = append(clauses, fmt.Sprintf("released_ts >= %d", r.ReleasedFromTS))
	}
	if r.ReleasedToTS != 0 {
		clauses = append(clauses, fmt.Sprintf("released_ts <= %d", r.ReleasedToTS))
	}
	// Discontinuous months within the year range — `released_month IN [...]`.
	if len(r.ReleasedMonths) > 0 {
		clauses = append(clauses, inIntFilter("released_month", r.ReleasedMonths))
	}

	return strings.Join(clauses, " AND ")
}

// inIntFilter builds `field IN [1, 2, 3]`.
func inIntFilter(field string, values []int) string {
	strs := make([]string, len(values))
	for i, v := range values {
		strs[i] = strconv.Itoa(v)
	}
	return fmt.Sprintf("%s IN [%s]", field, strings.Join(strs, ", "))
}

// inStringFilter builds `field IN ['a', 'b']`.
func inStringFilter(field string, values []string) string {
	strs := make([]string, len(values))
	for i, v := range values {
		strs[i] = "'" + escapeFilter(v) + "'"
	}
	return fmt.Sprintf("%s IN [%s]", field, strings.Join(strs, ", "))
}

// escapeFilter makes a value safe for inclusion in a single-quoted Meilisearch filter string.
// Meilisearch filters use single-quoted strings; escape single quotes and backslashes.
func escapeFilter(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return s
}

// galgameSortRule converts API sort name → MS sort expression. Empty = no sort (use default ranking).
func galgameSortRule(sort string) string {
	switch sort {
	case "released_desc":
		return "released_ts:desc"
	case "released_asc":
		return "released_ts:asc"
	case "view":
		return "view:desc"
	case "updated":
		return "updated_ts:desc"
	case "", "relevance":
		return ""
	default:
		return ""
	}
}

// totalHits reads the exact total from a page-based response.
func totalHits(resp *meilisearch.SearchResponse) int64 {
	if resp.TotalHits > 0 {
		return resp.TotalHits
	}
	// Fallback: estimated (for limit/offset mode)
	return resp.EstimatedTotalHits
}

// decodeHits converts Meilisearch hits (each a map[string]json.RawMessage) into typed docs.
func decodeHits[T any](hits meilisearch.Hits) []T {
	out := make([]T, 0, len(hits))
	for _, h := range hits {
		var doc T
		if err := h.DecodeInto(&doc); err == nil {
			out = append(out, doc)
		}
	}
	return out
}

// hitToMap converts a single Meilisearch Hit into a map[string]any — flattening
// the RawMessage values so the JSON response is self-describing.
func hitToMap(h meilisearch.Hit) map[string]any {
	m := make(map[string]any, len(h))
	for k, raw := range h {
		var v any
		if err := json.Unmarshal(raw, &v); err == nil {
			m[k] = v
		}
	}
	return m
}

// toGalgameResponse converts MS response to our API response shape.
// Returns hits as raw maps (preserves `_formatted`, unknown fields, etc.)
func toGalgameResponse(resp *meilisearch.SearchResponse) *GalgameSearchResponse {
	items := make([]map[string]any, 0, len(resp.Hits))
	for _, h := range resp.Hits {
		items = append(items, hitToMap(h))
	}

	out := &GalgameSearchResponse{
		Items:            items,
		Total:            totalHits(resp),
		ProcessingTimeMS: resp.ProcessingTimeMs,
	}

	if len(resp.FacetDistribution) > 0 {
		var dist map[string]map[string]int
		if err := json.Unmarshal(resp.FacetDistribution, &dist); err == nil {
			out.Facets = dist
		}
	}

	return out
}
