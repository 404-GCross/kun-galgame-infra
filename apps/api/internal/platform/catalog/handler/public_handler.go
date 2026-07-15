package handler

import (
	"strconv"
	"strings"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/catalog/model"
	catsearch "api/internal/platform/catalog/search"
	"api/internal/platform/catalog/service"
	"api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
)

// PublicHandler serves the NextMoe open-API catalog public projection
// (`/v1/catalog/*`, step 03) — the frozen cross-media identity face. It is a NEW
// bypass over the same read + resolve services + the entity search indexer; the
// S2S / admin handlers are untouched. Every route is mounted behind the devapi
// middleware chain (credential → rate → quota → catalog:read scope). Probable
// anchors and r18 works never surface (硬红线); the projections live in
// service.PublicService.
type PublicHandler struct {
	svc     *service.PublicService
	resolve *service.ResolveService
	search  *catsearch.Indexer
}

// NewPublicHandler builds the public projection handler.
func NewPublicHandler(svc *service.PublicService, resolve *service.ResolveService, searcher *catsearch.Indexer) *PublicHandler {
	return &PublicHandler{svc: svc, resolve: resolve, search: searcher}
}

const (
	// Cache-Control windows: identity/detail records are stable → cache the
	// longest; the mutable feeds cache shorter.
	cacheDetail    = "public, max-age=0, s-maxage=300, stale-while-revalidate=60"
	cacheSearch    = "public, max-age=0, s-maxage=60, stale-while-revalidate=60"
	cacheRedirects = "public, max-age=0, s-maxage=30, stale-while-revalidate=30"
)

// WorkDetail serves GET /v1/catalog/works/{id} — the frozen work record.
// include gates relations / credits. 404 outside the fetchable set (galgame,
// live, non-r18).
func (h *PublicHandler) WorkDetail(c fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}
	rec, found, err := h.svc.WorkDetail(c.Context(), id, service.ParsePublicInclude(c.Query("include")))
	if err != nil {
		return response.InternalError(c, errors.ErrInternalServer)
	}
	if !found {
		return response.NotFound(c, errors.ErrNotFound)
	}
	c.Set("Cache-Control", cacheDetail)
	return response.Success(c, rec)
}

// Lookup serves GET /v1/catalog/lookup?source=&external_id= — the external-id
// reverse-lookup killer. Exact anchors only; 404 on a miss / hidden work.
func (h *PublicHandler) Lookup(c fiber.Ctx) error {
	source := strings.TrimSpace(c.Query("source"))
	externalID := c.Query("external_id")
	if source == "" || strings.TrimSpace(externalID) == "" {
		return response.BadRequestMsg(c, errors.ErrBadRequest, "source and external_id are required")
	}
	data, found, err := h.svc.Lookup(c.Context(), source, externalID)
	if err != nil {
		return response.InternalError(c, errors.ErrInternalServer)
	}
	if !found {
		return response.NotFound(c, errors.ErrNotFound)
	}
	c.Set("Cache-Control", cacheDetail)
	return response.Success(c, data)
}

// LookupBatch serves POST /v1/catalog/lookup/batch — up to 100 (source,
// external_id) pairs; misses return a null work in their slot (order preserved).
func (h *PublicHandler) LookupBatch(c fiber.Ctx) error {
	var req dto.PublicLookupBatchRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrBadRequest, "malformed body")
	}
	if len(req.Items) == 0 {
		return response.BadRequestMsg(c, errors.ErrBadRequest, "items is required (1-100 pairs)")
	}
	if len(req.Items) > 100 {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, "at most 100 pairs per batch")
	}
	items, err := h.svc.LookupBatch(c.Context(), req.Items)
	if err != nil {
		return response.InternalError(c, errors.ErrInternalServer)
	}
	return response.Success(c, dto.PublicLookupBatchData{Items: items})
}

// Resolve serves POST /v1/catalog/resolve — batch old-id → canonical (redirect
// flattening), the public projection of the internal resolve semantics.
func (h *PublicHandler) Resolve(c fiber.Ctx) error {
	var req dto.PublicResolveRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.BadRequestMsg(c, errors.ErrBadRequest, "malformed body")
	}
	et, ok := entityTypeFromKey(req.EntityType)
	if !ok {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, "unknown entity_type")
	}
	if len(req.IDs) > 1000 {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, "at most 1000 ids per call")
	}
	mappings, err := h.resolve.ResolveBatch(c.Context(), et, req.IDs)
	if err != nil {
		return response.InternalError(c, errors.ErrInternalServer)
	}
	out := dto.PublicResolveData{Mappings: make(map[string]int64, len(mappings))}
	for old, canonical := range mappings {
		out.Mappings[strconv.FormatInt(old, 10)] = canonical
		if old != canonical {
			out.Redirected = append(out.Redirected, old)
		}
	}
	return response.Success(c, out)
}

// Redirects serves GET /v1/catalog/redirects — the keyset id-convergence feed
// (public projection of the internal S2S feed). Filter by entity_type.
func (h *PublicHandler) Redirects(c fiber.Ctx) error {
	cursor, err := decodeRedirectCursor(c.Query("cursor"))
	if err != nil {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, "malformed cursor")
	}
	var filter *int16
	if k := c.Query("entity_type"); k != "" {
		et, ok := entityTypeFromKey(k)
		if !ok {
			return response.BadRequestMsg(c, errors.ErrInvalidParam, "unknown entity_type")
		}
		filter = &et
	}
	limit := atoiOrPub(c.Query("limit"), 0)
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	items, next, err := h.resolve.RedirectsSince(c.Context(), filter, cursor, limit)
	if err != nil {
		return response.InternalError(c, errors.ErrInternalServer)
	}
	out := dto.PublicRedirectsData{Items: make([]dto.PublicRedirectItem, len(items))}
	for i, it := range items {
		out.Items[i] = dto.PublicRedirectItem{
			EntityType: entityTypeKey(it.EntityType), OldID: it.OldID,
			CurrentID: it.CurrentID, MergedAt: it.MergedAt,
		}
	}
	if len(items) == limit {
		nc := encodeRedirectCursor(next)
		out.NextCursor = &nc
	}
	c.Set("Cache-Control", cacheRedirects)
	return response.Success(c, out)
}

// Person serves GET /v1/catalog/persons/{id} — a credited identity ({id} is a
// credit-name id). include=credits attaches its works + roles.
func (h *PublicHandler) Person(c fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}
	limit, offset := pagePub(c)
	rec, found, err := h.svc.Person(c.Context(), id, service.ParsePublicInclude(c.Query("include")).Credits, limit, offset)
	if err != nil {
		return response.InternalError(c, errors.ErrInternalServer)
	}
	if !found {
		return response.NotFound(c, errors.ErrNotFound)
	}
	c.Set("Cache-Control", cacheDetail)
	return response.Success(c, rec)
}

// Character serves GET /v1/catalog/characters/{id}. include=works attaches the
// works it appears in with voice names.
func (h *PublicHandler) Character(c fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}
	limit, offset := pagePub(c)
	rec, found, err := h.svc.Character(c.Context(), id, service.ParsePublicInclude(c.Query("include")).Works, limit, offset)
	if err != nil {
		return response.InternalError(c, errors.ErrInternalServer)
	}
	if !found {
		return response.NotFound(c, errors.ErrNotFound)
	}
	c.Set("Cache-Control", cacheDetail)
	return response.Success(c, rec)
}

// Label serves GET /v1/catalog/labels/{id}. include=works attaches the works
// attributed to it.
func (h *PublicHandler) Label(c fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}
	limit, offset := pagePub(c)
	rec, found, err := h.svc.Label(c.Context(), id, service.ParsePublicInclude(c.Query("include")).Works, limit, offset)
	if err != nil {
		return response.InternalError(c, errors.ErrInternalServer)
	}
	if !found {
		return response.NotFound(c, errors.ErrNotFound)
	}
	c.Set("Cache-Control", cacheDetail)
	return response.Success(c, rec)
}

// Search serves GET /v1/catalog/search — entity relevance search over the three
// indexes (persons / characters / labels), projected to public briefs (never the
// internal document shape, 裁定 9).
func (h *PublicHandler) Search(c fiber.Ctx) error {
	uid, entityType, ok := publicSearchIndex(c.Query("type"))
	if !ok {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, "type must be one of persons|characters|labels")
	}
	limit := atoiOrPub(c.Query("limit"), 20)
	if limit <= 0 || limit > 20 {
		limit = 20
	}
	res, err := h.search.SearchEntities(c.Context(), uid, c.Query("q"), catsearch.LocalesForUI(c.Query("locale")), limit)
	if err != nil {
		return response.InternalError(c, errors.ErrInternalServer)
	}
	out := dto.PublicEntitySearchData{Total: res.Total, Items: make([]dto.PublicEntityHit, 0, len(res.Hits))}
	for _, d := range res.Hits {
		id, ok := stripEntityPrefix(d.ID)
		if !ok {
			continue
		}
		out.Items = append(out.Items, dto.PublicEntityHit{
			ID: id, EntityType: entityType, Name: d.Name(), Latin: d.Latin, Sources: d.Sources,
		})
	}
	c.Set("Cache-Control", cacheSearch)
	return response.Success(c, out)
}

// ─────────────────────────── helpers ───────────────────────────

// entityTypeKey maps a redirect/resolve entity-type constant to its public
// string key.
func entityTypeKey(t int16) string {
	switch t {
	case model.EntityTypePerson:
		return "person"
	case model.EntityTypeCreditName:
		return "name"
	case model.EntityTypeOrg:
		return "org"
	case model.EntityTypeLabel:
		return "label"
	case model.EntityTypeCharacter:
		return "character"
	case model.EntityTypeWork:
		return "work"
	case model.EntityTypeRelease:
		return "release"
	default:
		return ""
	}
}

// entityTypeFromKey is the inverse of entityTypeKey (input validation).
func entityTypeFromKey(k string) (int16, bool) {
	switch k {
	case "person":
		return model.EntityTypePerson, true
	case "name":
		return model.EntityTypeCreditName, true
	case "org":
		return model.EntityTypeOrg, true
	case "label":
		return model.EntityTypeLabel, true
	case "character":
		return model.EntityTypeCharacter, true
	case "work":
		return model.EntityTypeWork, true
	case "release":
		return model.EntityTypeRelease, true
	default:
		return 0, false
	}
}

// publicSearchIndex maps a public search type to its Meili index uid + the
// entity_type string surfaced on hits. persons → the credit-names index.
func publicSearchIndex(t string) (uid, entityType string, ok bool) {
	switch t {
	case "persons":
		uid, ok = catsearch.IndexForType("names")
		return uid, "person", ok
	case "characters":
		uid, ok = catsearch.IndexForType("characters")
		return uid, "character", ok
	case "labels":
		uid, ok = catsearch.IndexForType("labels")
		return uid, "label", ok
	default:
		return "", "", false
	}
}

// stripEntityPrefix turns a prefixed entity-search id (n123 / c123 / b123) into
// its numeric id (裁定 2 — public ids are plain numbers).
func stripEntityPrefix(id string) (int64, bool) {
	if len(id) < 2 {
		return 0, false
	}
	n, err := strconv.ParseInt(id[1:], 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// pagePub reads the offset-pagination params (limit ≤50, non-negative offset).
func pagePub(c fiber.Ctx) (limit, offset int) {
	limit = atoiOrPub(c.Query("limit"), 50)
	if limit <= 0 || limit > 50 {
		limit = 50
	}
	offset = atoiOrPub(c.Query("offset"), 0)
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func atoiOrPub(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}
