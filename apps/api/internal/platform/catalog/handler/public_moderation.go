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

const (
	worksStatusLive    = "live"
	worksStatusPending = "pending"
)

const msgBadWorksStatus = "status must be live|pending"

const msgStatusClaimStateConflict = "status=pending already selects the pending claim state; do not also pass claim_state"

type queueRefusal struct {
	status int
	msg    string
}

func forbidQueue(msg string) *queueRefusal {
	return &queueRefusal{status: http.StatusForbidden, msg: msg}
}

func pendingQueueSite(ctx context.Context, clients OAuthClientLookup, uid uint, roles []string, clientID string) (string, *queueRefusal) {
	if uid == 0 {
		return "", forbidQueue("the review queue needs the moderator's own access token in Authorization alongside the API key")
	}
	if clients == nil {
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
	f.Statuses = []int16{model.WorkStatusLive, model.WorkStatusStub}
	return nil
}
