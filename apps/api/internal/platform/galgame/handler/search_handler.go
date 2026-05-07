package handler

import (
	"strconv"
	"strings"

	"api/internal/platform/galgame/search"
	"api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
)

// SearchHandler wraps the Meilisearch-backed search service.
// Separate from per-entity handlers so routing can point at this directly
// for /galgame/search / /tag/search / /official/search.
type SearchHandler struct {
	svc *search.Service
}

func NewSearchHandler(svc *search.Service) *SearchHandler {
	return &SearchHandler{svc: svc}
}

// Galgame handles GET /galgame/search.
//
// See docs/galgame_wiki/05-search-design.md for the full query-param spec.
func (h *SearchHandler) Galgame(c fiber.Ctx) error {
	q := c.Queries()
	req := &search.GalgameSearchRequest{
		Query:             q["q"],
		Statuses:          parseIntList(q["status"]),
		ContentLimit:      q["content_limit"],
		AgeLimit:          q["age_limit"],
		OriginalLanguages: parseStringList(q["original_language"]),
		TagIDs:            parseIntList(q["tag_ids"]),
		OfficialIDs:       parseIntList(q["official_ids"]),
		EngineIDs:         parseIntList(q["engine_ids"]),
		SeriesID:          parseIntPtr(q["series_id"]),
		ReleasedFrom:      parseIntPtr(q["released_from"]),
		ReleasedTo:        parseIntPtr(q["released_to"]),
		IncludeIntro:      parseBool(q["include_intro"]),
		Sort:              q["sort"],
		Page:              atoiOr(q["page"], 1),
		Limit:             atoiOr(q["limit"], 24),
		WantFacets:        parseBoolDefault(q["facets"], true),
		WantHighlight:     parseBoolDefault(q["highlight"], true),
		Fields:            parseStringList(q["fields"]),
	}

	resp, err := h.svc.SearchGalgames(c.Context(), req)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	return response.Success(c, resp)
}

// Tag handles GET /tag/search.
func (h *SearchHandler) Tag(c fiber.Ctx) error {
	q := c.Queries()
	req := &search.TagSearchRequest{
		Query:    q["q"],
		Category: q["category"],
		Limit:    atoiOr(q["limit"], 50),
	}

	resp, err := h.svc.SearchTags(c.Context(), req)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	return response.Success(c, resp)
}

// Official handles GET /official/search.
func (h *SearchHandler) Official(c fiber.Ctx) error {
	q := c.Queries()
	req := &search.OfficialSearchRequest{
		Query:    q["q"],
		Category: q["category"],
		Lang:     q["lang"],
		Limit:    atoiOr(q["limit"], 50),
	}

	resp, err := h.svc.SearchOfficials(c.Context(), req)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	return response.Success(c, resp)
}

// ─────────────────────────── query helpers ───────────────────────────

// parseIntList splits "1,2,3" → [1, 2, 3]. Non-numeric tokens are skipped silently.
func parseIntList(s string) []int {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if v, err := strconv.Atoi(p); err == nil {
			out = append(out, v)
		}
	}
	return out
}

// parseStringList splits "a,b,c" → ["a", "b", "c"], trimming and dropping empties.
func parseStringList(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseIntPtr returns nil if s is empty or non-numeric. Used for optional int query params.
func parseIntPtr(s string) *int {
	if s == "" {
		return nil
	}
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return nil
	}
	return &v
}

// parseBool treats "1", "true", "yes" (case-insensitive) as true; everything else false.
func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "y":
		return true
	}
	return false
}

// parseBoolDefault returns the default when the value is not set, otherwise parses it.
func parseBoolDefault(s string, def bool) bool {
	if s == "" {
		return def
	}
	return parseBool(s)
}

// atoiOr returns the default when conversion fails.
func atoiOr(s string, def int) int {
	if s == "" {
		return def
	}
	if v, err := strconv.Atoi(s); err == nil {
		return v
	}
	return def
}
