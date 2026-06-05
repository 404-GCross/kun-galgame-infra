package handler

import (
	"strconv"
	"strings"

	"api/internal/platform/galgame/search"
	"api/pkg/errors"
	"api/pkg/response"
	"api/pkg/utils"

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
//
// include_pending=true: when paired with a valid Bearer JWT (OptionalJWT
// middleware populates user_id in Locals), runs a second search for the
// caller's own status=3/4 entries and returns them in resp.Pending.
//
// content_limit policy: safe-by-default — caller must explicitly opt
// into NSFW via `content_limit=nsfw` (NSFW only) or `content_limit=all`
// (both). Omitting the param falls back to "sfw". See handbook §NSFW.
func (h *SearchHandler) Galgame(c fiber.Ctx) error {
	q := c.Queries()
	includePending := parseBool(q["include_pending"])

	// Only honor include_pending when the caller is authenticated.
	viewerUserID, _ := c.Locals("user_id").(uint)
	if !includePending || viewerUserID == 0 {
		includePending = false
	}

	// Release date bounds: accept YYYY (year-only, legacy contract) or
	// YYYY-MM (new month precision). utils handles both formats + leap
	// years + 30/31-day months. Invalid input → 400 to surface the
	// typo to the caller rather than silently dropping the filter.
	fromTime, err := utils.ParseReleaseLowerBound(q["released_from"])
	if err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}
	toTime, err := utils.ParseReleaseUpperBound(q["released_to"])
	if err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}
	var fromTS, toTS int64
	if !fromTime.IsZero() {
		fromTS = fromTime.Unix()
	}
	if !toTime.IsZero() {
		toTS = toTime.Unix()
	}

	// Discontinuous month set (1-12), AND-layered on the year range.
	months, err := utils.ParseMonthSet(q["released_months"])
	if err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}

	// Public search defaults to published-only (status=0). Without this the
	// empty filter returned ALL states, leaking banned (1) / declined (4) /
	// others' pending (3) galgame — and making banned-galgame "soft delete"
	// invisible only on the SQL list, not here. A logged-in viewer's OWN
	// pending drafts still come back via the independent include_pending
	// sub-query; callers needing other states pass `status` explicitly.
	statuses := parseIntList(q["status"])
	// `status` is an aggregation filter on a PUBLIC endpoint.
	//   - Non-admins may only see published (0) + claimable VNDB drafts (2).
	//     Both are public by design (status=2 is the "草稿搜索 / 认领" inventory);
	//     banned(1)/others'-pending(3)/declined(4) are never exposed via the
	//     status filter (a viewer's OWN pending/declined still arrive via the
	//     user-scoped include_pending path). Default = [0] (published only) so
	//     the public /search excludes drafts; the publish wizard opts INTO
	//     drafts by passing status=0,2.
	//   - Admins/moderators (the wiki UI) are unclamped: empty status means
	//     "all states" (the service applies no status filter), so a wiki search
	//     surfaces every game.
	roles, _ := c.Locals("user_roles").([]string)
	if !hasRole(roles, "admin", "moderator") {
		filtered := statuses[:0]
		for _, st := range statuses {
			if st == 0 || st == 2 { // published OR claimable VNDB draft
				filtered = append(filtered, st)
			}
		}
		statuses = filtered
		if len(statuses) == 0 {
			statuses = []int{0}
		}
	}

	req := &search.GalgameSearchRequest{
		Query:             q["q"],
		Statuses:          statuses,
		ContentLimit:      utils.ParseContentLimit(q["content_limit"], "sfw"),
		AgeLimit:          q["age_limit"],
		OriginalLanguages: parseStringList(q["original_language"]),
		TagIDs:            parseIntList(q["tag_ids"]),
		OfficialIDs:       parseIntList(q["official_ids"]),
		EngineIDs:         parseIntList(q["engine_ids"]),
		SeriesID:          parseIntPtr(q["series_id"]),
		ReleasedFromTS:    fromTS,
		ReleasedToTS:      toTS,
		ReleasedMonths:    months,
		IncludeIntro:      parseBool(q["include_intro"]),
		Sort:              q["sort"],
		Page:              atoiOr(q["page"], 1),
		Limit:             atoiOr(q["limit"], 24),
		WantFacets:        parseBoolDefault(q["facets"], true),
		WantHighlight:     parseBoolDefault(q["highlight"], true),
		Fields:            parseStringList(q["fields"]),
		IncludePending:    includePending,
		ViewerUID:         int(viewerUserID),
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
