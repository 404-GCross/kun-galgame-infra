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

// contentLimit resolves the effective content_limit filter for a public request:
// the three-state gate (sfw|nsfw|all) is resolved by devapi against the key's
// scope, then mapped to the repository filter value ("all" → "" = no filter;
// sfw/nsfw pass through). A no-scope key silently resolves to "sfw" regardless
// of the requested value (P5). Phase 1 keys never carry galgame:nsfw, so this is
// still always "sfw" today — the gate is wired but inert.
func (h *PublicHandler) contentLimit(c fiber.Ctx) string {
	return utils.ParseContentLimit(devapi.ResolveContentLimit(c, c.Query("content_limit")), "sfw")
}

const (
	// Cache-Control windows (裁定 6): detail/batch cache the longest with cheap
	// ETag revalidation; the mutable feeds cache shorter.
	cacheDetail  = "public, max-age=0, s-maxage=300, stale-while-revalidate=60"
	cacheList    = "public, max-age=0, s-maxage=60, stale-while-revalidate=60"
	cacheChanges = "public, max-age=0, s-maxage=30, stale-while-revalidate=30"
	// cacheStats mirrors the internal /galgame/stats window: the stats rebuild
	// daily, so a long shared-cache window + cheap weak-ETag revalidation.
	cacheStats = "public, max-age=0, s-maxage=3600, stale-while-revalidate=300"
)

// applyItemFields projects each item of a page to the sparse fieldset (fields=)
// when active, returning a []any of trimmed maps; when inactive it returns the
// typed slice UNCHANGED so the caller's default envelope is byte-identical. The
// caller wraps the result in its endpoint's envelope (items + next_cursor/total).
func applyItemFields[T any](items []T, f service.PublicFields) any {
	if !f.Active() {
		return items
	}
	out := make([]any, len(items))
	for i := range items {
		out[i] = service.ApplyPublicFields(items[i], f)
	}
	return out
}

// List serves GET /v1/galgame — a keyset (cursor) page of thin aggregate items.
// sort = id (default) | release_date; cursor/limit are opaque + clamped in the
// service. No offset (design §8). include=officials,scores expands each item;
// fields= trims the returned keys (step 07, both optional + additive).
func (h *PublicHandler) List(c fiber.Ctx) error {
	inc := service.ParsePublicItemInclude(c.Query("include"))
	data, err := h.svc.PublicList(c.Context(), c.Query("sort"), c.Query("cursor"), atoiOr(c.Query("limit"), 0), h.contentLimit(c), inc)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	c.Set("Cache-Control", cacheList)
	f := service.ParsePublicFields(c.Query("fields"))
	if !f.Active() {
		return response.Success(c, data) // byte-identical default
	}
	return response.Success(c, fiber.Map{"items": applyItemFields(data.Items, f), "next_cursor": data.NextCursor})
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
	// track_view=1 bumps the view counter — but ONLY for an internal-tier caller
	// (P6): the bridge Get bumped view, so an internal consumer migrating to /v1
	// keeps that behavior; every other tier silently ignores the param (public
	// traffic must not mutate wiki state). The row is published (PublicDetail only
	// returns found for status=0), so the counter is always the public-view count.
	if parseBool(c.Query("track_view")) {
		if cred := devapi.CredentialFrom(c); cred != nil && cred.Tier == devapi.TierInternal {
			h.svc.PublicTrackView(id)
		}
	}
	etag := fmt.Sprintf(`W/"g%d-%d"`, id, updated.Unix())
	c.Set("ETag", etag)
	c.Set("Cache-Control", cacheDetail)
	if c.Get("If-None-Match") == etag {
		return c.SendStatus(fiber.StatusNotModified)
	}
	// fields= trims top-level keys before serialization; inactive → rec unchanged
	// (the ETag folds id+updated only, same as include — the full URL is the
	// cache key, so different field selections never collide, mirroring step 02).
	return response.Success(c, service.ApplyPublicFields(rec, service.ParsePublicFields(c.Query("fields"))))
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
	f := service.ParsePublicFields(c.Query("fields"))

	var itemsBody any
	var maxUpdated string
	var count int
	if c.Query("view") == "detail" {
		// view=detail is full aggregate records (no list-level include); fields=
		// still trims them.
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
		itemsBody = applyItemFields(recs, f)
	} else {
		items, err := h.svc.PublicBatchThin(c.Context(), ids, cl, service.ParsePublicItemInclude(c.Query("include")))
		if err != nil {
			return response.InternalError(c, errors.ErrOperationFailed)
		}
		for i := range items {
			if items[i].Updated > maxUpdated {
				maxUpdated = items[i].Updated
			}
		}
		count = len(items)
		itemsBody = applyItemFields(items, f)
	}

	// RFC3339 UTC strings sort lexicographically = chronologically, so the max
	// string is max(updated) with no parsing.
	etag := fmt.Sprintf(`W/"gbatch-%s-%d"`, maxUpdated, count)
	c.Set("ETag", etag)
	c.Set("Cache-Control", cacheDetail)
	if c.Get("If-None-Match") == etag {
		return c.SendStatus(fiber.StatusNotModified)
	}
	return response.Success(c, fiber.Map{"items": itemsBody})
}

// highlightFields is the Meilisearch attribute set retrieved when highlight=1:
// id (to key the highlight entry) + the four localized names (so their
// <mark>-wrapped `_formatted` variants come back). It is used ONLY for the
// highlight projection; the response items are still re-hydrated from the DB.
var highlightFields = []string{"id", "name_zh_cn", "name_ja_jp", "name_en_us", "name_zh_tw"}

// Search serves GET /v1/galgame/search — Meilisearch relevance over published
// galgames, projected to thin aggregate items in relevance order. Page/limit
// (not cursor): relevance ranking is not stable for keyset paging.
//
// W1a add-only params: facets=1 adds the Meilisearch facet distribution;
// highlight=1 adds a parallel <mark>-wrapped names array keyed by id;
// include_pending=1 (dual-credential — key in X-API-Key, an OPTIONAL end-user
// JWT in Authorization: Bearer, populated by optionalJWT) adds the caller's own
// pending/declined drafts. Without a valid JWT, include_pending is silently
// dropped (no pending key). Each block is omitted entirely when not requested,
// so the default response is byte-identical.
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

	wantFacets := parseBool(c.Query("facets"))
	wantHighlight := parseBool(c.Query("highlight"))
	// include_pending is dual-credential: honored only when the OPTIONAL end-user
	// JWT (Authorization: Bearer, parsed by optionalJWT) is present and valid.
	viewerUID, _ := c.Locals("user_id").(uint)
	wantPending := parseBool(c.Query("include_pending")) && viewerUID > 0

	// Only need ids to re-hydrate from the DB; widen to the name fields when
	// highlight is requested so Meili returns their `_formatted` variants.
	fields := []string{"id"}
	if wantHighlight {
		fields = highlightFields
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
		Fields:            fields,
		WantFacets:        wantFacets,
		WantHighlight:     wantHighlight,
		IncludePending:    wantPending,
		ViewerUID:         int(viewerUID),
	}
	resp, err := h.search.SearchGalgames(c.Context(), req)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	inc := service.ParsePublicItemInclude(c.Query("include"))
	ids := hitIDs(resp.Items)
	items, err := h.svc.PublicBatchThin(c.Context(), ids, req.ContentLimit, inc)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}

	// Optional add-only blocks. Each stays nil (→ omitempty → key absent) unless
	// requested, so the default response is byte-identical.
	var facets *map[string]map[string]int
	if wantFacets && len(resp.Facets) > 0 {
		facets = &resp.Facets
	}
	var highlight *[]dto.PublicSearchHighlight
	if wantHighlight {
		hl := highlightFromHits(resp.Items)
		highlight = &hl
	}
	var pending *[]dto.PublicGalgameItem
	if wantPending && len(resp.Pending) > 0 {
		// The viewer's own drafts are re-hydrated with viewer visibility (status
		// 3/4 for this uid). content_limit is NOT applied to the caller's own
		// drafts — mirrors the internal search pending sub-query.
		pendItems, pErr := h.svc.PublicBatchThinViewer(c.Context(), hitIDs(resp.Pending), int(viewerUID), "", inc)
		if pErr != nil {
			return response.InternalError(c, errors.ErrOperationFailed)
		}
		pending = &pendItems
	}

	c.Set("Cache-Control", cacheList)
	f := service.ParsePublicFields(c.Query("fields"))
	if !f.Active() {
		// omitempty pointers keep the no-optional default byte-identical.
		return response.Success(c, dto.PublicSearchData{
			Items: items, Total: resp.Total,
			Facets: facets, Highlight: highlight, Pending: pending,
		})
	}
	// fields= trims the item keys (envelope keys facets/highlight/pending are not
	// item-level and are unaffected). Build the map, adding optional keys present.
	out := fiber.Map{"items": applyItemFields(items, f), "total": resp.Total}
	if facets != nil {
		out["facets"] = facets
	}
	if highlight != nil {
		out["highlight"] = highlight
	}
	if pending != nil {
		out["pending"] = applyItemFields(*pending, f)
	}
	return response.Success(c, out)
}

// highlightFromHits builds the parallel highlight array from Meilisearch hits:
// each entry is the hit id + its localized names taken from the hit's
// `_formatted` object (the <mark>-wrapped variants), applying the empty→null
// discipline of PublicNames. A hit without `_formatted` yields all-null names.
func highlightFromHits(hits []map[string]any) []dto.PublicSearchHighlight {
	out := make([]dto.PublicSearchHighlight, 0, len(hits))
	for _, hit := range hits {
		id, ok := intFromAny(hit["id"])
		if !ok {
			continue
		}
		formatted, _ := hit["_formatted"].(map[string]any)
		out = append(out, dto.PublicSearchHighlight{
			ID: id,
			Names: dto.PublicNames{
				JaJP: formattedName(formatted, "name_ja_jp"),
				ZhCN: formattedName(formatted, "name_zh_cn"),
				ZhTW: formattedName(formatted, "name_zh_tw"),
				EnUS: formattedName(formatted, "name_en_us"),
			},
		})
	}
	return out
}

// formattedName reads one localized name from a Meili `_formatted` object,
// mapping ""/absent → nil (the PublicNames empty→null discipline).
func formattedName(formatted map[string]any, key string) *string {
	if formatted == nil {
		return nil
	}
	s, ok := formatted[key].(string)
	if !ok || s == "" {
		return nil
	}
	return &s
}

// intFromAny coerces a Meili hit id (JSON number → float64, or int/int64/string)
// to an int.
func intFromAny(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(n)); err == nil {
			return i, true
		}
	}
	return 0, false
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

// Stats serves GET /v1/galgame/stats — the site-wide cross-source statistics
// overview (the six frozen galgame_stats snapshots, payloads verbatim). Mirrors
// the internal /galgame/stats: weak ETag folded from max(built_at) with a
// matching If-None-Match → 304, and the daily-rebuild Cache-Control window.
func (h *PublicHandler) Stats(c fiber.Ctx) error {
	data, maxBuilt, err := h.svc.GetStats(c.Context())
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	etag := fmt.Sprintf(`W/"gstats-%d"`, maxBuilt.Unix())
	c.Set("ETag", etag)
	c.Set("Cache-Control", cacheStats)
	if c.Get("If-None-Match") == etag {
		return c.SendStatus(fiber.StatusNotModified)
	}
	return response.Success(c, data)
}

// Lookup serves GET /v1/galgame/lookup?vndb_id= — whether a galgame with that
// vndb_id exists, and its id when it does. Mirrors the internal /galgame/check,
// but the id key is `id` (public DTO convention), not the internal `galgame_id`.
func (h *PublicHandler) Lookup(c fiber.Ctx) error {
	vndbID := strings.TrimSpace(c.Query("vndb_id"))
	if vndbID == "" {
		return response.BadRequestMsg(c, errors.ErrValidationFailed, "vndb_id is required")
	}
	exists, galgameID, err := h.svc.CheckVNDB(c.Context(), vndbID)
	if err != nil {
		return response.InternalError(c, errors.ErrOperationFailed)
	}
	data := dto.PublicLookupData{Exists: exists}
	if exists {
		id := galgameID
		data.ID = &id
	}
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
