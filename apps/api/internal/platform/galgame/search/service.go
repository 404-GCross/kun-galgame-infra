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
	SeriesID          *int     // nil = no filter
	ReleasedFrom      *int     // year inclusive
	ReleasedTo        *int     // year inclusive

	IncludeIntro bool

	Sort  string // "relevance" / "released_desc" / "released_asc" / "view" / "updated"
	Page  int    // 1-based
	Limit int

	WantFacets    bool
	WantHighlight bool
}

// GalgameSearchResponse mirrors what we return in the API.
type GalgameSearchResponse struct {
	Items            []map[string]any          `json:"items"`
	Total            int64                     `json:"total"`
	Facets           map[string]map[string]int `json:"facets,omitempty"`
	ProcessingTimeMS int64                     `json:"processing_time_ms"`
}

// defaultSearchableGalgame mirrors galgamesSettings().SearchableAttributes.
// When include_intro=true we tack intro_* onto this list via attributesToSearchOn.
var defaultSearchableGalgame = []string{
	"vndb_id",
	"name_zh_cn", "name_ja_jp", "name_en_us", "name_zh_tw",
	"aliases",
	"tag_names",
	"official_names",
}

var introSearchable = []string{
	"intro_zh_cn", "intro_ja_jp", "intro_en_us", "intro_zh_tw",
}

// SearchGalgames runs a galgame search.
func (s *Service) SearchGalgames(ctx context.Context, req *GalgameSearchRequest) (*GalgameSearchResponse, error) {
	req.normalize()

	msReq := &meilisearch.SearchRequest{
		Page:        int64(req.Page),
		HitsPerPage: int64(req.Limit),
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

	// attributesToSearchOn: expand to include intro_* when requested
	if req.IncludeIntro {
		searchOn := make([]string, 0, len(defaultSearchableGalgame)+len(introSearchable))
		searchOn = append(searchOn, defaultSearchableGalgame...)
		searchOn = append(searchOn, introSearchable...)
		msReq.AttributesToSearchOn = searchOn
	}

	resp, err := s.client.Index(IndexGalgames).SearchWithContext(ctx, req.Query, msReq)
	if err != nil {
		return nil, err
	}

	return toGalgameResponse(resp), nil
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
	if r.Page < 1 {
		r.Page = 1
	}
	normalizeLimit(&r.Limit, 24, 100)
}

func normalizeLimit(limit *int, def, max int) {
	if *limit <= 0 {
		*limit = def
	}
	if *limit > max {
		*limit = max
	}
}

// buildGalgameFilter produces a Meilisearch filter string. Empty string = no filter.
func buildGalgameFilter(r *GalgameSearchRequest) string {
	var clauses []string

	if len(r.Statuses) > 0 {
		clauses = append(clauses, inIntFilter("status", r.Statuses))
	}
	if r.ContentLimit != "" {
		clauses = append(clauses, fmt.Sprintf("content_limit = '%s'", escapeFilter(r.ContentLimit)))
	}
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
	if r.ReleasedFrom != nil {
		clauses = append(clauses, fmt.Sprintf("released_year >= %d", *r.ReleasedFrom))
	}
	if r.ReleasedTo != nil {
		clauses = append(clauses, fmt.Sprintf("released_year <= %d", *r.ReleasedTo))
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
