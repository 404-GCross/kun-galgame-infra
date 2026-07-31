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
	// stats backs the SLIM public counts lane only (wave 149b) — the same
	// service the internal dashboard uses, through a different, public-only
	// method. None of the dashboard's telemetry reaches this face.
	stats *service.StatsService
}

// NewPublicHandler builds the public projection handler.
func NewPublicHandler(svc *service.PublicService, resolve *service.ResolveService, searcher *catsearch.Indexer, stats *service.StatsService) *PublicHandler {
	return &PublicHandler{svc: svc, resolve: resolve, search: searcher, stats: stats}
}

const (
	// Cache-Control windows: identity/detail records are stable → cache the
	// longest; the mutable feeds cache shorter.
	cacheDetail    = "public, max-age=0, s-maxage=300, stale-while-revalidate=60"
	cacheSearch    = "public, max-age=0, s-maxage=60, stale-while-revalidate=60"
	cacheRedirects = "public, max-age=0, s-maxage=30, stale-while-revalidate=30"

	// msgBadLimit is the single wording for a present-but-illegal limit across
	// every public lane (the clamp handles "too big"; this covers "not a
	// positive integer").
	msgBadLimit = "limit must be a positive integer"

	// msgBadLookupType is the single wording for a lookup type outside the
	// closed vocabulary — our own token set, so an unknown one is a caller
	// mistake (400), unlike an unknown source (an open registry → a miss).
	msgBadLookupType = "type must be one of work, name, character, label"
)

// WorkDetail serves GET /v1/catalog/works/{id} — the frozen work record.
// include gates relations / credits; spoilers raises the tag spoiler ceiling
// (default 0 = no spoiler-flagged tag at all). 404 outside the fetchable set
// (galgame, live, non-r18).
func (h *PublicHandler) WorkDetail(c fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return response.BadRequest(c, errors.ErrInvalidID)
	}
	rec, found, err := h.svc.WorkDetail(c.Context(), id,
		service.ParsePublicInclude(c.Query("include")), nsfwQuery(c), spoilersQuery(c))
	if err != nil {
		return response.InternalError(c, errors.ErrInternalServer)
	}
	if !found {
		return response.NotFound(c, errors.ErrNotFound)
	}
	c.Set("Cache-Control", cacheDetail)
	return response.Success(c, rec)
}

// Lookup serves GET /v1/catalog/lookup?source=&external_id=&type= — the
// external-id reverse-lookup killer. Exact anchors only; 404 on a miss / hidden
// entity. type selects the family (work default | name | character | label).
func (h *PublicHandler) Lookup(c fiber.Ctx) error {
	source := strings.TrimSpace(c.Query("source"))
	externalID := c.Query("external_id")
	if source == "" || strings.TrimSpace(externalID) == "" {
		return response.BadRequestMsg(c, errors.ErrBadRequest, "source and external_id are required")
	}
	entityType, _, ok := service.ParsePublicLookupType(c.Query("type"))
	if !ok {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, msgBadLookupType)
	}
	data, found, err := h.svc.LookupTyped(c.Context(), source, externalID, entityType, nsfwQuery(c))
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
// external_id, type) pairs; misses return null blocks in their slot (order
// preserved). One illegal type token fails the whole request (400) rather than
// silently degrading that pair to a miss.
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
	for _, p := range req.Items {
		if _, _, ok := service.ParsePublicLookupType(p.Type); !ok {
			return response.BadRequestMsg(c, errors.ErrInvalidParam, msgBadLookupType)
		}
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
	limit, offset, ok := pagePub(c)
	if !ok {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, msgBadLimit)
	}
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
	limit, offset, ok := pagePub(c)
	if !ok {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, msgBadLimit)
	}
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
	limit, offset, ok := pagePub(c)
	if !ok {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, msgBadLimit)
	}
	rec, found, err := h.svc.Label(c.Context(), id, service.ParsePublicInclude(c.Query("include")).Works, nsfwQuery(c), limit, offset)
	if err != nil {
		return response.InternalError(c, errors.ErrInternalServer)
	}
	if !found {
		return h.missOrMoved(c, model.EntityTypeLabel, "labels", id)
	}
	c.Set("Cache-Control", cacheDetail)
	return response.Success(c, rec)
}

// missOrMoved is the miss branch of every MERGE-CAPABLE detail lane.
//
// A merged entity is soft-deleted and leaves a catalog_redirect behind, so
// "the row is not there" has two very different meanings: the id never existed
// (404) or its identity moved to a survivor (301). Serving the survivor's
// record under the dead id would be the third, worst option — a 200 that
// duplicates one entity across two URLs — so the survivor's CONTENT never
// travels this path; only its id does.
//
// The resolver is the shared ResolveService, which is fixpoint by construction:
// merges flatten chains at write time and it errors loudly rather than
// following one, so the current_id handed out here is always final.
//
// lane is the /v1/catalog path segment ("labels") used to build Location.
func (h *PublicHandler) missOrMoved(c fiber.Ctx, entityType int16, lane string, id int64) error {
	current, moved, err := h.resolve.Resolve(c.Context(), entityType, id)
	if err != nil {
		// A broken flatten invariant (ErrRedirectChain) is a real fault, not a
		// miss: 500 rather than a 404 that would hide it.
		return response.InternalError(c, errors.ErrInternalServer)
	}
	if !moved {
		return response.NotFound(c, errors.ErrNotFound)
	}
	c.Set("Location", "/v1/catalog/"+lane+"/"+strconv.FormatInt(current, 10))
	c.Set("Cache-Control", cacheDetail)
	return c.Status(fiber.StatusMovedPermanently).JSON(response.Response{
		Code:    errors.ErrMoved,
		Message: "this id was merged away; use current_id",
		Data: dto.PublicMovedData{
			EntityType: entityTypeKey(entityType), ID: id, CurrentID: current,
		},
	})
}

// Search serves GET /v1/catalog/search — entity relevance AUTOCOMPLETE over the
// entity indexes (names / characters / labels / works / tags), projected to
// public briefs (never the internal document shape, 裁定 9).
//
// This is the identity finder: 20 hits, one flat shape per family, no
// pagination. The works PRODUCT search — filters, facets, sort, paging,
// works-list rows — is a separate door at GET /v1/catalog/works/search; this
// lane's wire stays frozen (A2-1d adds the `tags` type and nothing else).
func (h *PublicHandler) Search(c fiber.Ctx) error {
	uid, entityType, ok := publicSearchIndex(c.Query("type"))
	if !ok {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, "type must be one of names|characters|labels|works|tags")
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
	res, err := h.search.SearchEntities(c.Context(), uid, c.Query("q"), catsearch.LocalesForUI(uid, c.Query("locale")), limit, filter)
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
		if entityType == "tag" {
			// Tag docs carry both axes; every other family leaves them empty, so
			// the four pre-A2-1d hit shapes stay byte-identical.
			hit.Tier = publicTagTierKey(derefI16(d.Tier))
			hit.Kind = publicTagKindKey(derefI16(d.Kind))
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

// boolQueryPub reads an opt-in boolean query flag, the same vocabulary
// nsfwQuery accepts. Anything else — including a typo — is false: these flags
// only ever WIDEN a response, so an unrecognized value degrading to the narrow
// default is the safe direction.
func boolQueryPub(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
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
	case "tags":
		// A2-1d settles the A2-1b account: the canonical tag vocabulary joins
		// the entity search. Same index shape as labels; the hit additionally
		// carries tier + kind.
		uid, ok = catsearch.IndexForType("tags")
		return uid, "tag", ok
	default:
		return "", "", false
	}
}

// publicTagTierKey / publicTagKindKey mirror the service-layer projections (tag
// search hits only): the contract speaks string keys, never enum ints.
func publicTagTierKey(t int16) string {
	switch t {
	case model.TagTierLongtail:
		return "longtail"
	case model.TagTierHidden:
		return "hidden"
	default:
		return "core"
	}
}

func publicTagKindKey(k int16) string {
	if k == model.TagKindMeta {
		return "meta"
	}
	return "content"
}

// stripEntityPrefix turns a prefixed entity-search id (n123 / c123 / b123 /
// w123 / t123) into its numeric id (裁定 2 — public ids are plain numbers).
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

// pagePub reads the offset-pagination params of the sub-list lanes (limit
// 1-50, default 50; non-negative offset). ok=false when limit is present but
// not a positive integer — the caller turns that into a 400.
func pagePub(c fiber.Ctx) (limit, offset int, ok bool) {
	limit, ok = limitPub(c.Query("limit"), 50, 50)
	if !ok {
		return 0, 0, false
	}
	offset = atoiOrPub(c.Query("offset"), 0)
	if offset < 0 {
		offset = 0
	}
	return limit, offset, true
}

// limitPub reads a `limit` query param: absent/empty → def; a value above max
// is CLAMPED down to max (never reset to def — a caller asking for more than
// we serve should get the most we serve); anything that is not a positive
// integer is rejected (ok=false → 400) instead of silently falling back, which
// would hide the caller's mistake behind a plausible-looking page.
func limitPub(raw string, def, max int) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, false
	}
	if n > max {
		return max, true
	}
	return n, true
}

// posIntQueryPub reads an optional positive-integer id filter: absent/empty →
// 0 (no filter). A present-but-illegal value (non-numeric, 0, negative) is
// rejected (ok=false → 400) rather than degrading to 0, which would silently
// DROP the filter and serve the unfiltered first page as if it matched.
func posIntQueryPub(raw string) (int64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, true
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// maxTagIDFilters caps the conjunctive tag filter. Ten is well past any real
// UI (a facet sidebar with ten tags already returns nothing) and keeps the
// EXISTS chain / Meilisearch expression bounded.
const maxTagIDFilters = 10

// msgBadTagIDs is the single 400 message for every malformed tag_id list.
const msgBadTagIDs = "tag_id must be up to 10 comma-separated positive integers"

// msgBadClaimState is the single 400 message for a malformed claim_state list —
// shared by the works LIST and the works SEARCH faces, which must reject the
// same tokens with the same words.
const msgBadClaimState = "claim_state must be a comma-separated subset of none, live, draft, pending, declined, hidden"

// claimStatesPub reads the comma-separated claim_state= filter: the CLOSED
// public claim vocabulary (model.ClaimStateKey's whole range — the six values
// claimed_by.state renders). Absent/empty → nil, meaning NO gate at all, so
// every pre-existing caller's wire stays byte-identical. ok=false — a LOUD 400
// — on any token outside the vocabulary, never a silent drop: answering 200 to
// `claim_state=liev` with a page full of the drafts the caller asked to exclude
// is the production incident this parameter exists to end.
//
// Both faces parse through THIS function (A2-R4): "the list parameter is
// word-for-word the search parameter" is then structural, not a promise two
// copies have to keep.
func claimStatesPub(raw string) ([]string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, true
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		tok := strings.TrimSpace(p)
		if !service.IsWorksSearchClaimState(tok) {
			return nil, false
		}
		out = append(out, tok)
	}
	return out, true
}

// msgBadDisplayLimit is the single 400 message for a malformed content_limit
// list — shared by the works LIST, the works SEARCH and the three calendar
// buckets, which must reject the same tokens with the same words.
const msgBadDisplayLimit = "content_limit must be a comma-separated subset of sfw, nsfw"

// displayLimitsPub reads the comma-separated content_limit= filter: the CLOSED
// editorial display vocabulary (model.DisplayLimitKey's whole range — the two
// values claimed_by.content_limit renders). Absent/empty → nil, meaning NO gate
// at all, so every pre-existing caller's wire stays byte-identical. ok=false —
// a LOUD 400 — on any token outside the vocabulary.
//
// All three faces parse through THIS function (A2-R5), the claimStatesPub
// posture verbatim: "the list parameter is word-for-word the search parameter"
// is then structural, not a promise three copies have to keep.
//
// Note the vocabulary is NOT the wiki face's own content_limit vocabulary,
// which additionally accepts `all`. Here absence already means "both", so an
// `all` token would be a second spelling of the default — and a caller that
// typed it expecting the wiki semantics would get exactly what it wanted
// anyway, which is precisely why it must 400 instead: silently agreeing here
// would hide the fact that the two faces' parameters are not the same thing.
func displayLimitsPub(raw string) ([]string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, true
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		tok := strings.TrimSpace(p)
		if !service.IsDisplayLimit(tok) {
			return nil, false
		}
		out = append(out, tok)
	}
	return out, true
}

// posIntListQueryPub reads a comma-separated positive-integer id filter, the
// multi-value form of posIntQueryPub (A2-1e). Absent/empty → nil (no filter);
// a SINGLE value behaves exactly as it did before this wave. Duplicates
// collapse (asking for tag 7 twice is asking for tag 7). ok=false — a 400 — on
// any non-positive / non-numeric entry or on more than max entries, never a
// silent drop of the filter.
func posIntListQueryPub(raw string, max int) ([]int64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, true
	}
	parts := strings.Split(raw, ",")
	if len(parts) > max {
		return nil, false
	}
	out := make([]int64, 0, len(parts))
	seen := make(map[int64]struct{}, len(parts))
	for _, p := range parts {
		n, err := strconv.ParseInt(strings.TrimSpace(p), 10, 64)
		if err != nil || n <= 0 {
			return nil, false
		}
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out, true
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
// (DESC, newest first). include= attaches the rich-brief blocks (A2-1a);
// an unknown token is ignored, never a 400. A malformed cursor / filter is a
// 400, never a 500.
func (h *PublicHandler) WorksList(c fiber.Ctx) error {
	f := service.WorksListFilter{
		NSFW:     nsfwQuery(c),
		Platform: strings.TrimSpace(c.Query("platform")),
		Include:  service.ParseWorksListInclude(c.Query("include")),
		// site (wave 161 P5): the tenant gate. Any non-empty value is a legal
		// query — an unknown site simply matches nothing, exactly like an
		// unknown label_id. There is no closed vocabulary to validate against
		// (sites are registered per OAuth client, not enumerated on this face),
		// so 400ing on "not a site I know" would leak the tenant registry.
		Site: strings.TrimSpace(c.Query("site")),
	}
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
	var ok bool
	// claim_state (A2-R4): the works/search parameter word-for-word, same helper.
	// `claimed` answers "does a product own this row"; this answers "may it be
	// shown" — a site's own entity page listing its members needs the second.
	if f.ClaimStates, ok = claimStatesPub(c.Query("claim_state")); !ok {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, msgBadClaimState)
	}
	// content_limit (A2-R5): the EDITORIAL DISPLAY axis, orthogonal to both
	// `content_rating` (the age axis) and `claim_state` — same helper as the
	// works/search and calendar faces.
	if f.DisplayLimits, ok = displayLimitsPub(c.Query("content_limit")); !ok {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, msgBadDisplayLimit)
	}
	if f.LabelID, ok = posIntQueryPub(c.Query("label_id")); !ok {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, "label_id must be a positive integer")
	}
	if f.TagIDs, ok = posIntListQueryPub(c.Query("tag_id"), maxTagIDFilters); !ok {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, msgBadTagIDs)
	}
	if f.SeriesID, ok = posIntQueryPub(c.Query("series_id")); !ok {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, "series_id must be a positive integer")
	}
	if f.EngineID, ok = posIntQueryPub(c.Query("engine_id")); !ok {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, "engine_id must be a positive integer")
	}
	if f.ReleasedAfter, ok = datePub(c.Query("released_after")); !ok {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, "released_after must be YYYY-MM-DD")
	}
	if f.ReleasedBefore, ok = datePub(c.Query("released_before")); !ok {
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
	limit, ok := limitPub(c.Query("limit"), 20, 100)
	if !ok {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, msgBadLimit)
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
	limit, ok := limitPub(c.Query("limit"), 100, 500)
	if !ok {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, msgBadLimit)
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
	limit, offset, ok := pagePub(c)
	if !ok {
		return response.BadRequestMsg(c, errors.ErrInvalidParam, msgBadLimit)
	}
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
