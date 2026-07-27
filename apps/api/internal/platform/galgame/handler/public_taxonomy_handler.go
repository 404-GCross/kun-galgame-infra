package handler

import (
	"strconv"

	"api/internal/platform/devapi"
	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/search"
	"api/internal/platform/galgame/service"
	"api/pkg/errors"
	"api/pkg/response"
	"api/pkg/utils"

	"github.com/gofiber/fiber/v3"
)

// PublicTaxonomyHandler serves the NextMoe open-API taxonomy public projection
// (`/v1/galgame/{tags,officials,engines,series}/*`, W1b) — the curated by-id
// faces for the four taxonomy families. It is a NEW bypass over the taxonomy
// projection service + the shared Meilisearch service; the internal bridge
// handlers are untouched. Every route is mounted behind the same devapi chain
// (credential → rate → quota → galgame:read scope) as the aggregate face.
type PublicTaxonomyHandler struct {
	svc    *service.PublicTaxonomyService
	search *search.Service
}

// NewPublicTaxonomyHandler builds the public taxonomy projection handler.
func NewPublicTaxonomyHandler(svc *service.PublicTaxonomyService, searchSvc *search.Service) *PublicTaxonomyHandler {
	return &PublicTaxonomyHandler{svc: svc, search: searchSvc}
}

// taxContentLimit resolves the effective content_limit filter for a taxonomy
// request via the three-state gate (P5), mapped to the repository filter value
// ("all" → "" = no filter). A no-scope key silently resolves to "sfw". Mirrors
// PublicHandler.contentLimit — the same wired-but-inert Phase-1 gate.
func taxContentLimit(c fiber.Ctx) string {
	return utils.ParseContentLimit(devapi.ResolveContentLimit(c, c.Query("content_limit")), "sfw")
}

// taxPageLimit reads the 1-based page + limit query params, clamping the limit to
// [1, maxLimit] with defLimit when absent/invalid.
func taxPageLimit(c fiber.Ctx, defLimit, maxLimit int) (int, int) {
	page := atoiOr(c.Query("page"), 1)
	if page < 1 {
		page = 1
	}
	limit := atoiOr(c.Query("limit"), defLimit)
	if limit < 1 {
		limit = defLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	return page, limit
}

// taxEntityID parses + validates the {id} path param (positive int); returns
// ok=false after writing the 400 response.
func taxEntityID(c fiber.Ctx) (int, bool) {
	id, err := strconv.Atoi(c.Params("id"))
	if err != nil || id <= 0 {
		_ = response.BadRequest(c, errors.ErrInvalidID)
		return 0, false
	}
	return id, true
}

// nonNilStrings returns s or an empty slice (so a JSON array key is [] not null).
func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ─────────────────────────── Tags ───────────────────────────

// TagList serves GET /v1/galgame/tags — a page of curated tag records. The
// content_limit gate hides the sexual tag category on the sfw face (P5).
func (h *PublicTaxonomyHandler) TagList(c fiber.Ctx) error {
	page, limit := taxPageLimit(c, 50, 100)
	data, err := h.svc.TagList(c.Context(), page, limit, taxContentLimit(c))
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	return response.Success(c, data)
}

// TagSearch serves GET /v1/galgame/tags/search — Meilisearch relevance over
// tags, projected to curated search items.
func (h *PublicTaxonomyHandler) TagSearch(c fiber.Ctx) error {
	resp, err := h.search.SearchTags(c.Context(), &search.TagSearchRequest{
		Query:    c.Query("q"),
		Category: c.Query("category"),
		Limit:    atoiOr(c.Query("limit"), 50),
	})
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	items := make([]dto.PublicTagSearchItem, 0, len(resp.Items))
	for i := range resp.Items {
		d := resp.Items[i]
		items = append(items, dto.PublicTagSearchItem{
			ID:           d.ID,
			Name:         d.Name,
			Aliases:      nonNilStrings(d.Aliases),
			Category:     d.Category,
			GalgameCount: d.GalgameCount,
		})
	}
	return response.Success(c, dto.PublicTagSearchData{Items: items, Total: resp.Total, ProcessingTimeMS: resp.ProcessingTimeMS})
}

// TagMulti serves GET /v1/galgame/tags/multi?ids= — the published galgames
// matching the given tag ids, projected to thin /v1 items. content_limit-gated.
// expand=descendants widens each id to {itself + its hierarchy descendants}
// and matches AND-of-OR-groups; the default stays the frozen flat-AND face.
func (h *PublicTaxonomyHandler) TagMulti(c fiber.Ctx) error {
	page, limit := taxPageLimit(c, 24, 50)
	expand := c.Query("expand") == "descendants"
	data, err := h.svc.TagMulti(c.Context(), parseIntList(c.Query("ids")), page, limit, taxContentLimit(c), service.ParsePublicItemInclude(c.Query("include")), expand)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	return response.Success(c, data)
}

// Tag serves GET /v1/galgame/tags/{id} — one tag's curated record. 404 on a
// missing id (by-id only; the bridge /:name lookup does not carry over).
func (h *PublicTaxonomyHandler) Tag(c fiber.Ctx) error {
	id, ok := taxEntityID(c)
	if !ok {
		return nil
	}
	data, found, err := h.svc.Tag(c.Context(), id)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	if !found {
		return response.NotFound(c, errors.ErrNotFound)
	}
	return response.Success(c, data)
}

// TagGalgameIDs serves GET /v1/galgame/tags/{id}/galgame-ids — {ids:[]int}.
func (h *PublicTaxonomyHandler) TagGalgameIDs(c fiber.Ctx) error {
	id, ok := taxEntityID(c)
	if !ok {
		return nil
	}
	ids, err := h.svc.TagGalgameIDs(c.Context(), id)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	return response.Success(c, dto.PublicIDsData{IDs: ids})
}

// ─────────────────────────── Officials ───────────────────────────

// OfficialList serves GET /v1/galgame/officials — a page of curated maker records.
func (h *PublicTaxonomyHandler) OfficialList(c fiber.Ctx) error {
	page, limit := taxPageLimit(c, 50, 100)
	data, err := h.svc.OfficialList(c.Context(), page, limit)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	return response.Success(c, data)
}

// OfficialSearch serves GET /v1/galgame/officials/search — Meilisearch relevance
// over makers, projected to curated search items.
func (h *PublicTaxonomyHandler) OfficialSearch(c fiber.Ctx) error {
	resp, err := h.search.SearchOfficials(c.Context(), &search.OfficialSearchRequest{
		Query:    c.Query("q"),
		Category: c.Query("category"),
		Lang:     c.Query("lang"),
		Limit:    atoiOr(c.Query("limit"), 50),
	})
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	items := make([]dto.PublicOfficialSearchItem, 0, len(resp.Items))
	for i := range resp.Items {
		d := resp.Items[i]
		items = append(items, dto.PublicOfficialSearchItem{
			ID:           d.ID,
			Name:         d.Name,
			Aliases:      nonNilStrings(d.Aliases),
			Category:     d.Category,
			GalgameCount: d.GalgameCount,
			Original:     d.Original,
			Lang:         d.Lang,
		})
	}
	return response.Success(c, dto.PublicOfficialSearchData{Items: items, Total: resp.Total, ProcessingTimeMS: resp.ProcessingTimeMS})
}

// Official serves GET /v1/galgame/officials/{id} — one maker's curated record.
func (h *PublicTaxonomyHandler) Official(c fiber.Ctx) error {
	id, ok := taxEntityID(c)
	if !ok {
		return nil
	}
	data, found, err := h.svc.Official(c.Context(), id)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	if !found {
		return response.NotFound(c, errors.ErrNotFound)
	}
	return response.Success(c, data)
}

// OfficialGalgameIDs serves GET /v1/galgame/officials/{id}/galgame-ids.
func (h *PublicTaxonomyHandler) OfficialGalgameIDs(c fiber.Ctx) error {
	id, ok := taxEntityID(c)
	if !ok {
		return nil
	}
	ids, err := h.svc.OfficialGalgameIDs(c.Context(), id)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	return response.Success(c, dto.PublicIDsData{IDs: ids})
}

// ─────────────────────────── Engines ───────────────────────────

// EngineList serves GET /v1/galgame/engines — a page of curated engine records
// (page/limit paginated; the bridge bare-array list does not carry over).
func (h *PublicTaxonomyHandler) EngineList(c fiber.Ctx) error {
	page, limit := taxPageLimit(c, 50, 100)
	data, err := h.svc.EngineList(c.Context(), page, limit)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	return response.Success(c, data)
}

// Engine serves GET /v1/galgame/engines/{id} — one engine's curated record.
func (h *PublicTaxonomyHandler) Engine(c fiber.Ctx) error {
	id, ok := taxEntityID(c)
	if !ok {
		return nil
	}
	data, found, err := h.svc.Engine(c.Context(), id)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	if !found {
		return response.NotFound(c, errors.ErrNotFound)
	}
	return response.Success(c, data)
}

// EngineGalgameIDs serves GET /v1/galgame/engines/{id}/galgame-ids.
func (h *PublicTaxonomyHandler) EngineGalgameIDs(c fiber.Ctx) error {
	id, ok := taxEntityID(c)
	if !ok {
		return nil
	}
	ids, err := h.svc.EngineGalgameIDs(c.Context(), id)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	return response.Success(c, dto.PublicIDsData{IDs: ids})
}

// ─────────────────────────── Series ───────────────────────────

// SeriesList serves GET /v1/galgame/series — a page of curated series records,
// each with a content_limit-gated member preview (thin items, batch-hydrated).
func (h *PublicTaxonomyHandler) SeriesList(c fiber.Ctx) error {
	page, limit := taxPageLimit(c, 24, 50)
	data, err := h.svc.SeriesList(c.Context(), page, limit, taxContentLimit(c), service.ParsePublicItemInclude(c.Query("include")))
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	return response.Success(c, data)
}

// Series serves GET /v1/galgame/series/{id} — one series' curated record with its
// full content_limit-gated member set projected to thin items.
func (h *PublicTaxonomyHandler) Series(c fiber.Ctx) error {
	id, ok := taxEntityID(c)
	if !ok {
		return nil
	}
	data, found, err := h.svc.Series(c.Context(), id, taxContentLimit(c), service.ParsePublicItemInclude(c.Query("include")))
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	if !found {
		return response.NotFound(c, errors.ErrNotFound)
	}
	return response.Success(c, data)
}
