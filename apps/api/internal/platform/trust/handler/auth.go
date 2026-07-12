package handler

import (
	"context"
	"encoding/base64"
	stderrors "errors"
	"net/http"
	"strings"

	siteModel "api/internal/platform/site/model"
	siteRepo "api/internal/platform/site/repository"
	"api/pkg/errors"
	"api/pkg/response"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humafiber"
	"github.com/gofiber/fiber/v3"
)

// Auth plumbing, following the catalog/community split: auth runs as
// path-scoped Fiber middleware; bridges lift the authenticated identity into
// the Huma request context for the operation handlers.
//
// S2S intake face: Basic client credentials only. The tenant `site` is DERIVED
// from the client's binding (oauth_clients.catalog_site — the shared per-client
// site key), never taken from the request body, so a client can only report on
// its own site.
//
// Admin face: the shared middleware.JWTAuth (accept-both verifier) + the
// trust.queue_access permission gate the /api/v1/admin/trust prefix at the
// Fiber layer; AdminBridge lifts the operator's user id for the decision
// handlers.

const localClient = "trust:oauth_client"

type ctxKey string

const (
	ctxKeyClient      ctxKey = "trust:oauth_client"
	ctxKeyAdminID     ctxKey = "trust:admin_user_id"
	ctxKeyGlobalRoles ctxKey = "trust:user_global_roles"
	ctxKeyClientID    ctxKey = "trust:token_client_id"
)

// clientSiteLookup resolves an OAuth client id to its record, so the admin face
// can derive a site-scoped caller's catalog_site. *siteRepo.OAuthClientRepository
// satisfies it; a one-method seam keeps the admin scope logic unit-testable
// without a main-DB oauth_clients fixture.
type clientSiteLookup interface {
	FindByClientID(ctx context.Context, clientID string) (*siteModel.OAuthClient, error)
}

// S2SAuth authenticates backend callers via "Basic <b64(client_id:secret)>".
func S2SAuth(clients *siteRepo.OAuthClientRepository) fiber.Handler {
	return func(c fiber.Ctx) error {
		client, err := authenticateBasic(c, clients)
		if err != nil {
			return response.Unauthorized(c, errors.ErrAuthUnauthorized)
		}
		c.Locals(localClient, client)
		return c.Next()
	}
}

func authenticateBasic(c fiber.Ctx, clients *siteRepo.OAuthClientRepository) (*siteModel.OAuthClient, error) {
	header := c.Get("Authorization")
	const prefix = "Basic "
	if !strings.HasPrefix(header, prefix) {
		return nil, stderrors.New("not Basic auth")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(header, prefix))
	if err != nil {
		return nil, err
	}
	clientID, secret, ok := strings.Cut(string(raw), ":")
	if !ok {
		return nil, stderrors.New("malformed Basic auth")
	}
	client, err := clients.FindByClientID(c.Context(), clientID)
	if err != nil || client == nil {
		return nil, stderrors.New("bad client")
	}
	if !client.VerifySecret(secret) {
		return nil, stderrors.New("bad secret")
	}
	return client, nil
}

// S2SBridge lifts the authenticated client into the Huma context.
func S2SBridge(ctx huma.Context, next func(huma.Context)) {
	fc := humafiber.Unwrap(ctx)
	if client, ok := fc.Locals(localClient).(*siteModel.OAuthClient); ok {
		ctx = huma.WithValue(ctx, ctxKeyClient, client)
	}
	next(ctx)
}

func clientFromCtx(ctx context.Context) *siteModel.OAuthClient {
	c, _ := ctx.Value(ctxKeyClient).(*siteModel.OAuthClient)
	return c
}

// siteBinding returns the tenant site the authenticated client acts as, or a
// 403 when the client is not bound to a site.
func siteBinding(ctx context.Context) (string, *houseError) {
	client := clientFromCtx(ctx)
	if client == nil || client.CatalogSite == "" {
		return "", apiErrMsg(http.StatusForbidden, errors.ErrForbidden,
			"client is not bound to a site; it cannot submit reports")
	}
	return client.CatalogSite, nil
}

// AdminBridge lifts the authenticated operator's identity (set by
// middleware.JWTAuth) into the Huma context: the user id (from TokenClaims.ID)
// so decision endpoints can record who acted, plus the GLOBAL roles and token
// client id the scope resolver uses to split platform staff from a site-scoped
// moderator. The Fiber layer has already rejected non-staff (trust.queue_access).
func AdminBridge(ctx huma.Context, next func(huma.Context)) {
	fc := humafiber.Unwrap(ctx)
	if id, ok := fc.Locals("user_id").(uint); ok {
		ctx = huma.WithValue(ctx, ctxKeyAdminID, int64(id))
	}
	if roles, ok := fc.Locals("user_global_roles").([]string); ok {
		ctx = huma.WithValue(ctx, ctxKeyGlobalRoles, roles)
	}
	if cid, ok := fc.Locals("token_client_id").(string); ok {
		ctx = huma.WithValue(ctx, ctxKeyClientID, cid)
	}
	next(ctx)
}

func adminIDFromCtx(ctx context.Context) int64 {
	id, _ := ctx.Value(ctxKeyAdminID).(int64)
	return id
}

// globalRolesFromCtx returns the caller's GLOBAL roles (never the site union) —
// the discriminator for the platform-staff vs site-scoped tier.
func globalRolesFromCtx(ctx context.Context) []string {
	roles, _ := ctx.Value(ctxKeyGlobalRoles).([]string)
	return roles
}

// tokenClientIDFromCtx returns the OAuth client id the token was issued to
// (empty for first-party tokens).
func tokenClientIDFromCtx(ctx context.Context) string {
	id, _ := ctx.Value(ctxKeyClientID).(string)
	return id
}
