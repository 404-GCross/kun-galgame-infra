package handler

import (
	stderrors "errors"
	"strconv"
	"strings"
	"time"

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
	rec, found, err := h.svc.WorkDetail(c.Context(), id, service.ParsePublicInclude(c.Query("include")), nsfwQuery(c))
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
	data, found, err := h.svc.Lookup(c.Context(), source, externalID, nsfwQuery(c))
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
	items, err := h.svc.LookupBatch(c.Context(), req.Items, nsfwQuery(c))
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

// Name serves GET /v1/catalog/names/{id} — a credited identity ({id} is a
// credit-name id). include=credits attaches its works + roles.
func (h *PublicHandler) Name(c fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}
	limit, offset := pagePub(c)
	rec, found, err := h.svc.Name(c.Context(), id, service.ParsePublicInclude(c.Query("include")).Credits, nsfwQuery(c), limit, offset)
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
	rec, found, err := h.svc.Character(c.Context(), id, service.ParsePublicInclude(c.Query("include")).Works, nsfwQuery(c), spoilersQuery(c), limit, offset)
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
	rec, found, err := h.svc.Label(c.Context(), id, service.ParsePublicInclude(c.Query("include")).Works, nsfwQuery(c), limit, offset)
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
// indexes (names / characters / labels), projected to public briefs (never the
// internal document shape, 裁定 9).
func (h *PublicHandler) Search(c fiber.Ctx) error {
	uid, entityType, ok := publicSearchIndex(c.Query("type"))
	if !ok {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, "type must be one of names|characters|labels|works")
	}
	limit := atoiOrPub(c.Query("limit"), 20)
	if limit <= 0 || limit > 20 {
		limit = 20
	}
	// The works index carries r18 rows; the nsfw switch (wave 104 doctrine)
	// gates them server-side. Entity indexes have no rating — no filter.
	filter := ""
	if entityType == "work" && !nsfwQuery(c) {
		filter = "content_rating != " + strconv.Itoa(int(model.ContentRatingR18))
	}
	res, err := h.search.SearchEntities(c.Context(), uid, c.Query("q"), catsearch.LocalesForUI(c.Query("locale")), limit, filter)
	if err != nil {
		return response.InternalError(c, errors.ErrInternalServer)
	}
	out := dto.PublicEntitySearchData{Total: res.Total, Items: make([]dto.PublicEntityHit, 0, len(res.Hits))}
	for _, d := range res.Hits {
		id, ok := stripEntityPrefix(d.ID)
		if !ok {
			continue
		}
		hit := dto.PublicEntityHit{
			ID: id, EntityType: entityType, Name: d.Name(), Latin: d.Latin, Sources: d.Sources,
		}
		if d.ContentRating != nil {
			hit.ContentRating = publicContentRatingKey(*d.ContentRating)
		}
		out.Items = append(out.Items, hit)
	}
	c.Set("Cache-Control", cacheSearch)
	return response.Success(c, out)
}

// ─────────────────────────── helpers ───────────────────────────

// nsfwQuery reads the wave-104 caller-controlled r18 switch: nsfw=1|true opts
// into r18 content (works, relation/credit/label briefs, sexual traits).
// Default false keeps the Phase-1 hidden behavior bit-identical.
func nsfwQuery(c fiber.Ctx) bool {
	switch strings.ToLower(strings.TrimSpace(c.Query("nsfw"))) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// spoilersQuery reads the trait spoiler ceiling (0-2, default 0 = safe), the
// S2S read-face convention verbatim.
func spoilersQuery(c fiber.Ctx) int16 {
	switch c.Query("spoilers") {
	case "1":
		return 1
	case "2":
		return 2
	}
	return 0
}

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
// entity_type string surfaced on hits. names → the credit-names index (the
// same "name" key resolve/redirects use for credit_name — one public vocabulary).
func publicSearchIndex(t string) (uid, entityType string, ok bool) {
	switch t {
	case "names":
		uid, ok = catsearch.IndexForType("names")
		return uid, "name", ok
	case "characters":
		uid, ok = catsearch.IndexForType("characters")
		return uid, "character", ok
	case "labels":
		uid, ok = catsearch.IndexForType("labels")
		return uid, "label", ok
	case "works":
		uid, ok = catsearch.IndexForType("works")
		return uid, "work", ok
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

// publicContentRatingKey mirrors the service-layer projection (works search
// hits only): the contract speaks string keys, never enum ints.
func publicContentRatingKey(r int16) string {
	switch r {
	case model.ContentRatingAllAges:
		return "all_ages"
	case model.ContentRatingSensitive:
		return "sensitive"
	case model.ContentRatingR18:
		return "r18"
	default:
		return ""
	}
}

// contentRatingFromKey is the inverse of publicContentRatingKey (input
// validation for the works-list filter).
func contentRatingFromKey(k string) (int16, bool) {
	switch k {
	case "all_ages":
		return model.ContentRatingAllAges, true
	case "sensitive":
		return model.ContentRatingSensitive, true
	case "r18":
		return model.ContentRatingR18, true
	default:
		return 0, false
	}
}

// datePub parses an optional YYYY-MM-DD date param into the composed ordinal
// (y*10000+m*100+d); 0 for absent. ok=false on a malformed value.
func datePub(raw string) (int64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, true
	}
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return 0, false
	}
	return int64(t.Year())*10000 + int64(t.Month())*100 + int64(t.Day()), true
}

// WorksList serves GET /v1/catalog/works — the keyset works browse lane
// (doc 106 G1). Filters are conjunctive; sort=id (ASC, default) | updated
// (DESC, newest first). A malformed cursor / filter is a 400, never a 500.
func (h *PublicHandler) WorksList(c fiber.Ctx) error {
	f := service.WorksListFilter{NSFW: nsfwQuery(c), Platform: strings.TrimSpace(c.Query("platform"))}
	switch sort := c.Query("sort"); sort {
	case "", "id":
		f.Sort = "id"
	case "updated":
		f.Sort = "updated"
	default:
		return response.BadRequestMsg(c, errors.ErrInvalidParam, "sort must be id|updated")
	}
	if cr := c.Query("content_rating"); cr != "" {
		v, ok := contentRatingFromKey(cr)
		if !ok {
			return response.BadRequestMsg(c, errors.ErrInvalidParam, "content_rating must be all_ages|sensitive|r18")
		}
		if v == model.ContentRatingR18 && !f.NSFW {
			return response.BadRequestMsg(c, errors.ErrInvalidParam, "content_rating=r18 requires nsfw=1")
		}
		f.ContentRating = &v
	}
	if cl := c.Query("claimed"); cl != "" {
		switch strings.ToLower(strings.TrimSpace(cl)) {
		case "1", "true", "yes":
			t := true
			f.Claimed = &t
		case "0", "false", "no":
			v := false
			f.Claimed = &v
		default:
			return response.BadRequestMsg(c, errors.ErrInvalidParam, "claimed must be true|false")
		}
	}
	f.LabelID = int64(atoiOrPub(c.Query("label_id"), 0))
	f.TagID = int64(atoiOrPub(c.Query("tag_id"), 0))
	f.SeriesID = int64(atoiOrPub(c.Query("series_id"), 0))
	var ok bool
	if f.ReleasedAfter, ok = datePub(c.Query("released_after")); !ok {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, "released_after must be YYYY-MM-DD")
	}
	if f.ReleasedBefor, ok = datePub(c.Query("released_before")); !ok {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, "released_before must be YYYY-MM-DD")
	}
	if raw := strings.TrimSpace(c.Query("ids")); raw != "" {
		parts := strings.Split(raw, ",")
		if len(parts) > 100 {
			return response.BadRequestMsg(c, errors.ErrValidationFailed, "at most 100 ids")
		}
		for _, p := range parts {
			id, err := strconv.ParseInt(strings.TrimSpace(p), 10, 64)
			if err != nil || id <= 0 {
				return response.BadRequestMsg(c, errors.ErrInvalidParam, "ids must be positive integers")
			}
			f.IDs = append(f.IDs, id)
		}
	}
	limit := atoiOrPub(c.Query("limit"), 20)
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	data, err := h.svc.WorksList(c.Context(), f, c.Query("cursor"), limit)
	if err != nil {
		if stderrors.Is(err, service.ErrBadCursor) {
			return response.BadRequestMsg(c, errors.ErrInvalidParam, "malformed cursor")
		}
		return response.InternalError(c, errors.ErrInternalServer)
	}
	c.Set("Cache-Control", cacheSearch)
	return response.Success(c, data)
}

// Changes serves GET /v1/catalog/changes — the incremental works sync feed
// (doc 106 G2). No nsfw gate: ids + timestamps are identity, not content —
// the consumer's detail follow-up re-applies the r18 gate.
func (h *PublicHandler) Changes(c fiber.Ctx) error {
	if et := c.Query("entity_type"); et != "" && et != "work" {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, "entity_type must be work (the v1 feed scope)")
	}
	limit := atoiOrPub(c.Query("limit"), 100)
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	data, err := h.svc.Changes(c.Context(), c.Query("cursor"), limit)
	if err != nil {
		if stderrors.Is(err, service.ErrBadCursor) {
			return response.BadRequestMsg(c, errors.ErrInvalidParam, "malformed cursor")
		}
		return response.InternalError(c, errors.ErrInternalServer)
	}
	c.Set("Cache-Control", cacheRedirects)
	return response.Success(c, data)
}

// Tag serves GET /v1/catalog/tags/{id} — the canonical-tag record (doc 106
// G5). include=works attaches the works carrying any mapped source tag.
func (h *PublicHandler) Tag(c fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}
	limit, offset := pagePub(c)
	rec, found, err := h.svc.TagDetail(c.Context(), id, service.ParsePublicInclude(c.Query("include")).Works, nsfwQuery(c), limit, offset)
	if err != nil {
		return response.InternalError(c, errors.ErrInternalServer)
	}
	if !found {
		return response.NotFound(c, errors.ErrNotFound)
	}
	c.Set("Cache-Control", cacheDetail)
	return response.Success(c, rec)
}
