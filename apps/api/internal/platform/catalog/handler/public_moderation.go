// public_moderation.go — the moderator review-queue view on the OPEN works
// list (GET /v1/catalog/works?status=pending), wave 186a.
//
// Why the parameter is not a reading of catalog_work.status:
//
// The registry status axis is {live, stub, merged} (model.WorkStatus*) and
// holds no review state at all: a user submission is minted status=LIVE
// (service.SubmitWork) and what is "awaiting a curator" about it lives on the
// CLAIM axis — model.ClaimStatePending, the very column
// ClaimLifecycleService.PendingClaims reads to build the staff queue. `stub` is
// the importer's "below the metadata bar" pile, which is UNCLAIMED by
// construction and therefore belongs to no tenant; `merged` is the 404
// tombstone. So `status=pending` selects the true queue — claim_state=pending,
// on the registry rows that are not tombstones — and `status=live` names
// today's default set. The wire word follows the STATE A MODERATOR WORKS, not
// the column name it happens to be stored in.
//
// Why it needs a second credential:
//
// The open API's only credential is a machine key (X-API-Key), which says
// which APPLICATION is calling and nothing about which PERSON. A review queue
// is a person's authority (catalog.claim.review), so the queue view reads the
// OPTIONAL end-user JWT the dual-credential transport leaves room for in
// Authorization (devapi.extractKey) — mounted as middleware.OptionalJWT, which
// never blocks, so every existing caller is untouched.
//
// Every refusal here is LOUD (403), never a quiet fall back to the live set: a
// moderator handed an empty page would conclude the queue is empty, which is
// the one wrong answer this view must never give.
package handler

import (
	"context"
	"net/http"
	"strings"

	"api/internal/platform/catalog/model"
	catperm "api/internal/platform/catalog/perm"
	"api/internal/platform/catalog/service"

	"github.com/gofiber/fiber/v3"
)

// The status= vocabulary. CLOSED — an unknown token is a 400, never a
// silently-ignored filter.
const (
	worksStatusLive    = "live"
	worksStatusPending = "pending"
)

const msgBadWorksStatus = "status must be live|pending"

// msgStatusClaimStateConflict: status=pending IS a claim_state gate, so a
// caller who also sent claim_state= is asking for two different answers to one
// question. Refuse instead of silently overriding either side.
const msgStatusClaimStateConflict = "status=pending already selects the pending claim state; do not also pass claim_state"

// queueRefusal is a composed refusal the caller renders. Keeping the gate a
// pure function of (locals, client registry) — rather than something that
// writes to the response itself — is what lets the whole matrix be tested
// without standing up a Fiber app per row.
type queueRefusal struct {
	status int
	msg    string
}

func forbidQueue(msg string) *queueRefusal {
	return &queueRefusal{status: http.StatusForbidden, msg: msg}
}

// pendingQueueSite authorizes the moderator queue view and returns the ONE
// tenant it may be served for.
//
// The four doors, in order, are the four ways this can be someone else's
// queue:
//
//  1. no verified end-user identity — the API key alone is an application, not
//     a moderator;
//  2. the token is not client-bound, or its client is unknown / bound to no
//     catalog site — there is no tenant to scope the queue to;
//  3. the token was issued through a THIRD-PARTY application
//     (OwnerUserID != nil, wave 186b) — an arbitrary developer's UI must never
//     become a moderation surface, whatever roles the person behind it holds;
//  4. the person's roles do not carry catalog.claim.review.
//
// The tenant is then the token client's own catalog_site, exactly as the user
// write plane derives it (handler/user.go userActor): nothing about identity or
// tenancy is taken from the query string. A platform-wide queue stays on the
// staff face (/api/v1/admin/catalog), which has a staff JWT behind it rather
// than a per-product token.
func pendingQueueSite(ctx context.Context, clients OAuthClientLookup, uid uint, roles []string, clientID string) (string, *queueRefusal) {
	if uid == 0 {
		return "", forbidQueue("the review queue needs the moderator's own access token in Authorization alongside the API key")
	}
	if clients == nil {
		// The face was mounted without the client registry, so tenancy cannot be
		// established. Refuse rather than serve an unpinned queue.
		return "", forbidQueue("the review queue is not available on this deployment")
	}
	if clientID == "" {
		return "", forbidQueue("the access token is not bound to an OAuth client; the review queue requires a client-bound token")
	}
	client, err := clients.FindByClientID(ctx, clientID)
	if err != nil || client == nil {
		return "", forbidQueue("the access token's client is not registered")
	}
	if isThirdPartyClient(client) {
		return "", forbidQueue("a third-party application is not a moderation surface; the review queue needs a first-party site client")
	}
	if client.CatalogSite == "" {
		return "", forbidQueue("the access token's client is not bound to a catalog site")
	}
	if !catperm.Resolver.Can(roles, catperm.ClaimReview) {
		return "", forbidQueue("the review queue requires the " + string(catperm.ClaimReview) + " permission")
	}
	return client.CatalogSite, nil
}

// applyWorksStatus reads the status= parameter and, for the queue view, writes
// the three things it implies onto the filter: the widened registry status set,
// the pending claim gate, and the tenant pin.
//
// The pin follows the user write plane's pattern — the tenant comes from the
// token's client and from nowhere else — with one addition this face needs and
// that one does not: `site=` already EXISTS here as an ordinary public filter.
// A caller naming a site that is not theirs is refused (403) rather than
// silently re-pointed at their own, because a moderator who asked for another
// tenant's queue and got their own back would misread every row on the page.
// Naming their own site, or naming none, is the pin.
func (h *PublicHandler) applyWorksStatus(c fiber.Ctx, f *service.WorksListFilter) *queueRefusal {
	switch strings.TrimSpace(c.Query("status")) {
	case "", worksStatusLive:
		return nil
	case worksStatusPending:
	default:
		return &queueRefusal{status: http.StatusBadRequest, msg: msgBadWorksStatus}
	}
	if strings.TrimSpace(c.Query("claim_state")) != "" {
		return &queueRefusal{status: http.StatusBadRequest, msg: msgStatusClaimStateConflict}
	}
	uid, _ := c.Locals("user_id").(uint)
	roles, _ := c.Locals("user_roles").([]string)
	clientID, _ := c.Locals("token_client_id").(string)
	site, refusal := pendingQueueSite(c.Context(), h.clients, uid, roles, clientID)
	if refusal != nil {
		return refusal
	}
	if asked := strings.TrimSpace(c.Query("site")); asked != "" && asked != site {
		return forbidQueue("the review queue is pinned to the token client's own catalog site; site=" + asked + " is not it")
	}
	f.Site = site
	f.ClaimStates = []string{model.ClaimStateKeyPending}
	// A submission is minted live, but a registry row can be downgraded to stub
	// while its claim still waits — that row is still this tenant's to review.
	// merged is deliberately absent: a tombstone answers 404 everywhere.
	f.Statuses = []int16{model.WorkStatusLive, model.WorkStatusStub}
	return nil
}
