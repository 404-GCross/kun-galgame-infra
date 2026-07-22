package galgameapp

import (
	"context"
	stderrors "errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"api/internal/platform/catalog/dto"
	catHandler "api/internal/platform/catalog/handler"
	"api/internal/platform/devapi"
	"api/internal/platform/editing"
	siteRepo "api/internal/platform/site/repository"
	"api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// The /internal/edit/* platform proposal face (09-open-api-phase2 06b, doc 23
// §5 — the dogfood bridge). A pure-Fiber, additive sibling of the S2S edit face
// (internal/platform/catalog/handler/edit.go): it exposes the USER PROPOSAL
// SUBSET only — create / mine / get-own / withdraw + the schema/snapshot
// read-only projections — never the review triad (amend/merge/decline), revert,
// or the review-queue list (those stay S2S, doc 23 ruling).
//
// It differs from the S2S face on exactly three axes, all hardened here:
//
//   - Actor (P2): NEVER asserted on the wire. The actor is the ALREADY-VERIFIED
//     end-user JWT (jwtAuth ran last in the chain → user_id local). It is a PLAIN
//     actor — trust 0, roles ∅, not entity-owner, HasPerm ≡ false — byte-for-byte
//     equivalent to the S2S plain-actor path. The platform DTOs carry NO actor,
//     roles, trust, or is_entity_owner field; a client that sends one is ignored.
//   - Site (P5): NEVER accepted on the wire. The filing tenant is reverse-looked-
//     up from the key's client binding (oauth_clients.catalog_site); an unbound
//     key is 403. A request-supplied site is ignored.
//   - Family (P4): every operation is hard-fenced to the galgame family.
//
// Response shapes reuse the exported S2S view constructors (catHandler.*View) so
// the envelopes are byte-identical to the S2S face (P10) — the BFF parses one
// shape regardless of which face served it.

const familyGalgame = "galgame"

// proposeHandler serves the six platform proposal operations over the single
// editing engine instance. It holds no per-request state.
type proposeHandler struct {
	engine *editing.Engine
	binder *siteBinder
}

func newProposeHandler(engine *editing.Engine, clients *siteRepo.OAuthClientRepository) *proposeHandler {
	return &proposeHandler{engine: engine, binder: newSiteBinder(clients)}
}

// isGalgameFamily reports whether a registered entity type belongs to the
// galgame family (its first dotted segment). The engine still rejects an
// unknown type; this is the face-level fence (P4).
func isGalgameFamily(entityType string) bool {
	fam, _, _ := strings.Cut(entityType, ".")
	return fam == familyGalgame
}

// actorContext derives the plain policy actor and the key-bound filing site for
// a request. On the failure paths it WRITES the response itself (403 when the
// key carries no site binding [P5]; 503 when the binding store is unreachable)
// and returns ok=false — the caller then just `return nil`. (The response.*
// helpers return nil, so an error return value cannot signal short-circuit; the
// ok flag does.)
func (h *proposeHandler) actorContext(c fiber.Ctx) (actor editing.PolicyContext, site string, ok bool) {
	cred := devapi.CredentialFrom(c)
	// The chain guarantees a resolved credential and a verified JWT before any
	// handler runs; these reads never miss in production.
	uid, _ := c.Locals("user_id").(uint)
	site, err := h.binder.siteFor(c.Context(), cred.ClientID)
	if err != nil {
		_ = response.Error(c, fiber.StatusServiceUnavailable, errors.ErrInternalServer, "site binding unavailable")
		return editing.PolicyContext{}, "", false
	}
	if site == "" {
		_ = response.ForbiddenMsg(c, errors.ErrForbidden,
			"client is not bound to a catalog site; it cannot file proposals")
		return editing.PolicyContext{}, "", false
	}
	// Plain actor (P2): the JWT's user_roles are deliberately discarded; trust 0,
	// not owner, no permissions. Strictly equivalent to the S2S plain-actor path.
	return editing.PolicyContext{
		UserID: int64(uid), Site: site, TrustTier: 0, IsEntityOwner: false,
		HasPerm: func(string) bool { return false },
	}, site, true
}

// proposeCreateRequest is the platform create DTO. It carries NO actor and NO
// site (P2/P5): both are server-derived. Any extra field a client sends is
// ignored by the JSON binder.
type proposeCreateRequest struct {
	EntityType string         `json:"entity_type"`
	EntityID   int64          `json:"entity_id"`
	Patch      map[string]any `json:"patch"`
	Note       string         `json:"note"`
}

// create files a proposal as the plain JWT actor. A galgame open-policy field
// yields an OPEN proposal that never automerges (plain actor holds no review
// perm and no ownership); the response mirrors the S2S create envelope exactly.
func (h *proposeHandler) create(c fiber.Ctx) error {
	actor, _, ok := h.actorContext(c)
	if !ok {
		return nil // 4xx/5xx already written
	}
	var req proposeCreateRequest
	if err := c.Bind().JSON(&req); err != nil {
		return response.Error(c, fiber.StatusUnprocessableEntity, errors.ErrValidationFailed, "invalid request body")
	}
	if !isGalgameFamily(req.EntityType) {
		return response.Error(c, fiber.StatusUnprocessableEntity, errors.ErrValidationFailed,
			"this face only accepts galgame-family entities")
	}
	prop, rev, err := h.engine.CreateProposal(c.Context(), editing.CreateProposalInput{
		EntityType: req.EntityType, EntityID: req.EntityID,
		Patch: req.Patch, Note: req.Note, Actor: actor,
	})
	if err != nil {
		return mapEditErr(c, err)
	}
	resp := dto.EditProposalCreateResponse{Proposal: catHandler.ProposalView(prop), Merged: rev != nil}
	if rev != nil {
		rv := catHandler.RevisionView(rev)
		resp.Revision = &rv
	}
	return response.Success(c, resp)
}

// proposeMineQuery narrows the caller's own proposal list. proposer_uid and site
// are NOT bindable — they are server-forced (P2/P5). entity_type, when present,
// must be galgame-family.
type proposeMineQuery struct {
	Status     string `query:"status"`
	EntityType string `query:"entity_type"`
	EntityID   int64  `query:"entity_id"`
	Limit      int    `query:"limit"`
}

// mine lists the caller's own proposals, hard-scoped to (this JWT uid, the
// key-bound site, galgame family). ProposerUID is forced server-side — a client
// can never widen it to enumerate others' proposals.
func (h *proposeHandler) mine(c fiber.Ctx) error {
	actor, site, ok := h.actorContext(c)
	if !ok {
		return nil // 4xx/5xx already written
	}
	var q proposeMineQuery
	if err := c.Bind().Query(&q); err != nil {
		return response.Error(c, fiber.StatusUnprocessableEntity, errors.ErrValidationFailed, "invalid query")
	}
	if q.EntityType != "" && !isGalgameFamily(q.EntityType) {
		return response.Error(c, fiber.StatusUnprocessableEntity, errors.ErrValidationFailed,
			"this face only accepts galgame-family entities")
	}
	status, ok := statusCode(q.Status)
	if !ok {
		return response.Error(c, fiber.StatusUnprocessableEntity, errors.ErrValidationFailed, "unknown status")
	}
	items, err := h.engine.ListProposals(c.Context(), editing.ProposalFilter{
		EntityType:  q.EntityType,
		EntityID:    q.EntityID,
		Site:        site,         // P5: key-bound tenant, never client-supplied
		ProposerUID: actor.UserID, // P2: forced to the JWT actor
		Status:      status,
		Limit:       q.Limit,
	})
	if err != nil {
		return mapEditErr(c, err)
	}
	resp := dto.EditProposalListResponse{Items: make([]dto.EditProposalView, 0, len(items))}
	for i := range items {
		// Defense-in-depth family fence (P4): the site scope already isolates the
		// kungal tenant, but never surface a non-galgame row from this face.
		if items[i].EntityFamily != familyGalgame {
			continue
		}
		resp.Items = append(resp.Items, catHandler.ProposalView(&items[i]))
	}
	return response.Success(c, resp)
}

// get reads ONE of the caller's own proposals with amendments + effective patch.
// The engine's GetProposal applies no proposer filter, so this handler enforces
// ownership itself (P1 越权枚举红线): any miss on family / tenant / proposer is a
// flat 404 — never revealing that a proposal it may not see exists.
func (h *proposeHandler) get(c fiber.Ctx) error {
	actor, site, ok := h.actorContext(c)
	if !ok {
		return nil // 4xx/5xx already written
	}
	id, ok := parseID(c)
	if !ok {
		return notFound(c)
	}
	prop, amendments, eff, err := h.engine.GetProposal(c.Context(), id)
	if err != nil {
		if stderrors.Is(err, editing.ErrProposalNotFound) {
			return notFound(c)
		}
		return mapEditErr(c, err)
	}
	if prop.EntityFamily != familyGalgame || prop.Site != site || prop.ProposerUID != actor.UserID {
		return notFound(c)
	}
	view := catHandler.ProposalView(prop)
	view.EffectivePatch = eff
	view.Amendments = catHandler.AmendmentViews(amendments)
	return response.Success(c, view)
}

// withdraw closes the caller's own open proposal. Family + tenant are fenced here
// (404, anti-enumeration); the PROPOSER check is the engine's native
// ErrNotProposer (403, mutate.go) and the open-state check its ErrNotOpen (409).
func (h *proposeHandler) withdraw(c fiber.Ctx) error {
	actor, site, ok := h.actorContext(c)
	if !ok {
		return nil // 4xx/5xx already written
	}
	id, ok := parseID(c)
	if !ok {
		return notFound(c)
	}
	prop, _, _, err := h.engine.GetProposal(c.Context(), id)
	if err != nil {
		if stderrors.Is(err, editing.ErrProposalNotFound) {
			return notFound(c)
		}
		return mapEditErr(c, err)
	}
	if prop.EntityFamily != familyGalgame || prop.Site != site {
		return notFound(c)
	}
	if err := h.engine.WithdrawProposal(c.Context(), id, actor); err != nil {
		return mapEditErr(c, err)
	}
	// Re-read for the closed view (mirrors the S2S closedView shape).
	closed, _, _, err := h.engine.GetProposal(c.Context(), id)
	if err != nil {
		return mapEditErr(c, err)
	}
	return response.Success(c, catHandler.ProposalView(closed))
}

// schema projects the entity type's field schema for the PLAIN caller (no
// asserted overlay). entity_type comes from the path; the optional entity_id
// query makes the owner-automerge projection entity-aware (0 = type-level).
func (h *proposeHandler) schema(c fiber.Ctx) error {
	actor, _, ok := h.actorContext(c)
	if !ok {
		return nil // 4xx/5xx already written
	}
	entityType := c.Params("entity_type")
	if !isGalgameFamily(entityType) {
		return response.Error(c, fiber.StatusUnprocessableEntity, errors.ErrValidationFailed,
			"this face only accepts galgame-family entities")
	}
	entityID, _ := strconv.ParseInt(c.Query("entity_id"), 10, 64)
	fields, err := h.engine.SchemaProjection(c.Context(), entityType, entityID, actor)
	if err != nil {
		return mapEditErr(c, err)
	}
	return response.Success(c, dto.EditSchemaResponse{
		EntityType: entityType, Fields: catHandler.SchemaFieldViews(fields),
	})
}

// snapshot returns the entity's current registered-field values (the editor's
// bootstrap read). No policy evaluation; family-fenced.
func (h *proposeHandler) snapshot(c fiber.Ctx) error {
	if _, _, ok := h.actorContext(c); !ok {
		return nil // 4xx/5xx already written
	}
	entityType := c.Query("entity_type")
	if !isGalgameFamily(entityType) {
		return response.Error(c, fiber.StatusUnprocessableEntity, errors.ErrValidationFailed,
			"this face only accepts galgame-family entities")
	}
	entityID, _ := strconv.ParseInt(c.Query("entity_id"), 10, 64)
	values, err := h.engine.CurrentSnapshot(c.Context(), entityType, entityID)
	if err != nil {
		return mapEditErr(c, err)
	}
	return response.Success(c, dto.EditSnapshotResponse{
		EntityType: entityType, EntityID: entityID, Values: values,
	})
}

// ---- shared helpers --------------------------------------------------------

func parseID(c fiber.Ctx) (int64, bool) {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func notFound(c fiber.Ctx) error {
	return response.Error(c, fiber.StatusNotFound, errors.ErrNotFound, editing.ErrProposalNotFound.Error())
}

// statusCode maps a wire status word to its engine code, mirroring the S2S list
// endpoint. "" (no filter) → (-1, ok); an unknown word → (0, false).
func statusCode(name string) (int16, bool) {
	if name == "" {
		return -1, true
	}
	for code, n := range editing.StatusName {
		if n == name {
			return code, true
		}
	}
	return 0, false
}

// mapEditErr maps engine errors onto the house envelope, matching the S2S face's
// editErr (same status + same code + same message string → byte-identical error
// bodies). The platform face can only surface the subset its six operations
// reach, but the full mapping is kept for parity and defense-in-depth.
func mapEditErr(c fiber.Ctx, err error) error {
	var (
		unknownField *editing.UnknownFieldError
		lockedField  *editing.LockedFieldError
		validation   *editing.ValidationError
		permission   *editing.PermissionError
		conflict     *editing.ConflictError
	)
	switch {
	case stderrors.Is(err, editing.ErrProposalNotFound),
		stderrors.Is(err, editing.ErrRevisionNotFound),
		stderrors.Is(err, editing.ErrEntityNotFound),
		stderrors.Is(err, editing.ErrUnknownEntityType):
		return response.Error(c, fiber.StatusNotFound, errors.ErrNotFound, err.Error())
	case stderrors.Is(err, editing.ErrEmptyPatch),
		stderrors.Is(err, editing.ErrEmptyDelta),
		stderrors.Is(err, editing.ErrNoEffectiveChanges):
		return response.Error(c, fiber.StatusUnprocessableEntity, errors.ErrValidationFailed, err.Error())
	case stderrors.As(err, &unknownField), stderrors.As(err, &lockedField), stderrors.As(err, &validation):
		return response.Error(c, fiber.StatusUnprocessableEntity, errors.ErrValidationFailed, err.Error())
	case stderrors.As(err, &permission), stderrors.Is(err, editing.ErrNotProposer):
		return response.Error(c, fiber.StatusForbidden, errors.ErrForbidden, err.Error())
	case stderrors.As(err, &conflict), stderrors.Is(err, editing.ErrNotOpen):
		return response.Error(c, fiber.StatusConflict, errors.ErrOperationFailed, err.Error())
	}
	// Unexpected engine failure — log it like the S2S face's editErr does, so the
	// platform face never swallows a genuine error into a bare 500.
	slog.Error("platform propose face", "err", err)
	return response.Error(c, fiber.StatusInternalServerError, errors.ErrInternalServer, errors.GetMessage(errors.ErrInternalServer))
}

// ---- site binder (P5) ------------------------------------------------------

// siteBinder reverse-looks-up a key's filing tenant from its client row
// (oauth_clients.catalog_site) with a small process-local TTL cache. The catalog
// process already owns this table (S2S Basic is same-origin), so the lookup is a
// single indexed read; the cache spares it on the hot path.
type siteBinder struct {
	clients *siteRepo.OAuthClientRepository
	ttl     time.Duration
	mu      sync.Mutex
	cache   map[string]siteBindEntry
}

type siteBindEntry struct {
	site string
	exp  time.Time
}

func newSiteBinder(clients *siteRepo.OAuthClientRepository) *siteBinder {
	return &siteBinder{clients: clients, ttl: 60 * time.Second, cache: map[string]siteBindEntry{}}
}

// siteFor returns the client's catalog_site binding ("" when unbound). A DB
// error is returned so the caller can 503 rather than mis-tenant the write.
// The empty ("" unbound) result is cached too, so a stream of unbound-key calls
// does not hammer the DB.
func (b *siteBinder) siteFor(ctx context.Context, clientID string) (string, error) {
	now := time.Now()
	b.mu.Lock()
	if e, ok := b.cache[clientID]; ok && now.Before(e.exp) {
		b.mu.Unlock()
		return e.site, nil
	}
	b.mu.Unlock()

	client, err := b.clients.FindByClientID(ctx, clientID)
	if err != nil {
		// A missing client row (the resolved key's app was deleted mid-flight) is
		// treated as unbound → 403, not a 503; only a genuine store failure 503s.
		if stderrors.Is(err, gorm.ErrRecordNotFound) {
			b.mu.Lock()
			b.cache[clientID] = siteBindEntry{site: "", exp: now.Add(b.ttl)}
			b.mu.Unlock()
			return "", nil
		}
		return "", err
	}
	site := client.CatalogSite

	b.mu.Lock()
	b.cache[clientID] = siteBindEntry{site: site, exp: now.Add(b.ttl)}
	b.mu.Unlock()
	return site, nil
}
