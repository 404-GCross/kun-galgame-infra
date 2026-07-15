package handler

import (
	"fmt"
	"strconv"
	"strings"

	"api/internal/platform/devapi"
	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/search"
	"api/internal/platform/galgame/service"
	"api/pkg/errors"
	"api/pkg/response"
	"api/pkg/utils"

	"github.com/gofiber/fiber/v3"
)

// PublicHandler serves the NextMoe open-API galgame public projection
// (`/v1/galgame/*`, step 02) — the frozen aggregate-record face. It is a NEW
// bypass over the same galgame service + search service; the internal read
// handlers are untouched. Every route on this handler is mounted behind the
// devapi middleware chain (credential → rate → quota → galgame:read scope).
type PublicHandler struct {
	svc    *service.GalgameService
	search *search.Service
}

// NewPublicHandler builds the public projection handler.
func NewPublicHandler(svc *service.GalgameService, searchSvc *search.Service) *PublicHandler {
	return &PublicHandler{svc: svc, search: searchSvc}
}

// contentLimit resolves the effective content_limit for a public request. Phase
// 1 keys never carry galgame:nsfw, so this is always "sfw" — the gate is wired
// but inert (裁定 2/6).
func (h *PublicHandler) contentLimit(c fiber.Ctx) string {
	return devapi.ResolveContentLimit(c, c.Query("content_limit"))
}

const (
	// Cache-Control windows (裁定 6): detail/batch cache the longest with cheap
	// ETag revalidation; the mutable feeds cache shorter.
	cacheDetail  = "public, max-age=0, s-maxage=300, stale-while-revalidate=60"
	cacheList    = "public, max-age=0, s-maxage=60, stale-while-revalidate=60"
	cacheChanges = "public, max-age=0, s-maxage=30, stale-while-revalidate=30"
)

// List serves GET /v1/galgame — a keyset (cursor) page of thin aggregate items.
// sort = id (default) | release_date; cursor/limit are opaque + clamped in the
// service. No offset (design §8).
func (h *PublicHandler) List(c fiber.Ctx) error {
	data, err := h.svc.PublicList(c.Context(), c.Query("sort"), c.Query("cursor"), atoiOr(c.Query("limit"), 0), h.contentLimit(c))
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	c.Set("Cache-Control", cacheList)
	return response.Success(c, data)
}

// Detail serves GET /v1/galgame/{id} — the full aggregate record. include gates
// the heavy blocks (intro,scores,covers,taxonomy). Weak ETag folds the updated
// timestamp; a matching If-None-Match returns 304.
func (h *PublicHandler) Detail(c fiber.Ctx) error {
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}
	rec, found, updated, err := h.svc.PublicDetail(c.Context(), id, service.ParsePublicInclude(c.Query("include")), h.contentLimit(c))
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	if !found {
		return response.NotFound(c, errors.ErrGalgameNotFound)
	}
	etag := fmt.Sprintf(`W/"g%d-%d"`, id, updated.Unix())
	c.Set("ETag", etag)
	c.Set("Cache-Control", cacheDetail)
	if c.Get("If-None-Match") == etag {
		return c.SendStatus(fiber.StatusNotModified)
	}
	return response.Success(c, rec)
}

// Batch serves GET /v1/galgame/batch?ids=1,2,3 — thin items by default,
// ?view=detail returns full aggregate records (no include). Weak ETag folds
// max(updated) + count across the returned set.
func (h *PublicHandler) Batch(c fiber.Ctx) error {
	ids := parseIntList(c.Query("ids"))
	if len(ids) == 0 {
		return response.BadRequestMsg(c, errors.ErrBadRequest, "ids is required (comma-separated, 1-100)")
	}
	if len(ids) > 100 {
		ids = ids[:100]
	}
	cl := h.contentLimit(c)

	var body any
	var maxUpdated string
	var count int
	if c.Query("view") == "detail" {
		recs, err := h.svc.PublicBatchDetail(c.Context(), ids, cl)
		if err != nil {
			return response.InternalError(c, errors.ErrOperationFailed)
		}
		for i := range recs {
			if recs[i].Updated > maxUpdated {
				maxUpdated = recs[i].Updated
			}
		}
		count = len(recs)
		body = fiber.Map{"items": recs}
	} else {
		items, err := h.svc.PublicBatchThin(c.Context(), ids, cl)
		if err != nil {
			return response.InternalError(c, errors.ErrOperationFailed)
		}
		for i := range items {
			if items[i].Updated > maxUpdated {
				maxUpdated = items[i].Updated
			}
		}
		count = len(items)
		body = fiber.Map{"items": items}
	}

	// RFC3339 UTC strings sort lexicographically = chronologically, so the max
	// string is max(updated) with no parsing.
	etag := fmt.Sprintf(`W/"gbatch-%s-%d"`, maxUpdated, count)
	c.Set("ETag", etag)
	c.Set("Cache-Control", cacheDetail)
	if c.Get("If-None-Match") == etag {
		return c.SendStatus(fiber.StatusNotModified)
	}
	return response.Success(c, body)
}

// Search serves GET /v1/galgame/search — Meilisearch relevance over published +
// sfw galgames, projected to thin aggregate items in relevance order. Page/limit
// (not cursor): relevance ranking is not stable for keyset paging.
func (h *PublicHandler) Search(c fiber.Ctx) error {
	fromTime, err := utils.ParseReleaseLowerBound(c.Query("released_from"))
	if err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}
	toTime, err := utils.ParseReleaseUpperBound(c.Query("released_to"))
	if err != nil {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, err.Error())
	}
	months, err := utils.ParseMonthSet(c.Query("released_months"))
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

	req := &search.GalgameSearchRequest{
		Query:             c.Query("q"),
		Statuses:          []int{0}, // public: published only
		ContentLimit:      h.contentLimit(c),
		AgeLimit:          c.Query("age_limit"),
		OriginalLanguages: parseStringList(c.Query("original_language")),
		TagIDs:            parseIntList(c.Query("tag_ids")),
		OfficialIDs:       parseIntList(c.Query("official_ids")),
		EngineIDs:         parseIntList(c.Query("engine_ids")),
		SeriesID:          parseIntPtr(c.Query("series_id")),
		ReleasedFromTS:    fromTS,
		ReleasedToTS:      toTS,
		ReleasedMonths:    months,
		Sort:              c.Query("sort"),
		Page:              atoiOr(c.Query("page"), 1),
		Limit:             atoiOr(c.Query("limit"), 24),
		Fields:            []string{"id"}, // only need ids; re-hydrate from DB
	}
	resp, err := h.search.SearchGalgames(c.Context(), req)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	ids := hitIDs(resp.Items)
	items, err := h.svc.PublicBatchThin(c.Context(), ids, req.ContentLimit)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	c.Set("Cache-Control", cacheList)
	return response.Success(c, dto.PublicSearchData{Items: items, Total: resp.Total})
}

// Changes serves GET /v1/galgame/changes — the incremental-sync keyset stream of
// {id, updated} for published + sfw galgames, ascending by (updated, id).
func (h *PublicHandler) Changes(c fiber.Ctx) error {
	data, err := h.svc.PublicChanges(c.Context(), c.Query("cursor"), atoiOr(c.Query("limit"), 0), h.contentLimit(c))
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	c.Set("Cache-Control", cacheChanges)
	return response.Success(c, data)
}

// hitIDs extracts the integer id from each Meilisearch hit, preserving order.
// Meili returns JSON numbers as float64.
func hitIDs(hits []map[string]any) []int {
	out := make([]int, 0, len(hits))
	for _, h := range hits {
		switch v := h["id"].(type) {
		case float64:
			out = append(out, int(v))
		case int:
			out = append(out, v)
		case int64:
			out = append(out, int(v))
		case string:
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				out = append(out, n)
			}
		}
	}
	return out
}
