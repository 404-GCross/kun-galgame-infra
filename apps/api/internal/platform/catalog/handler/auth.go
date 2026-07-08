package handler

import (
	"context"
	"encoding/base64"
	stderrors "errors"
	"fmt"
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

// Auth plumbing, following the artifact service's split: auth runs as
// path-scoped Fiber middleware; bridges lift the authenticated identity into
// the Huma request context for the operation handlers.
//
// S2S face: Basic client credentials only (backend-to-backend — the catalog
// has no browser-facing S2S path, so no Bearer/JWT branch here). Any valid
// first-party OAuth client may authenticate; the WRITE path additionally
// requires a per-client site binding (oauth_clients.catalog_site, enforced by
// enforceSiteBinding) so a client can only claim works for the product it owns.
// Read faces (resolve / redirect feed) impose no binding.
//
// Admin face: the shared middleware.JWTAuth (accept-both verifier) + the
// catalog.review (ren) permission gate the /admin prefix at the Fiber layer,
// exactly like the galgame admin surface; AdminBridge lifts the user id and
// roles for handlers that record the operator.

// Locals keys written by S2SAuth into fiber.Ctx.
const (
	localClient = "catalog:oauth_client"
)

type ctxKey string

const (
	ctxKeyClient  ctxKey = "catalog:oauth_client"
	ctxKeyAdminID ctxKey = "catalog:admin_user_id"
)

// S2SAuth authenticates backend callers via "Basic <b64(client_id:secret)>"
// against the OAuth client registry.
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

// enforceSiteBinding gates the claim write path: the authenticated client must
// be bound to a catalog site (oauth_clients.catalog_site) AND that binding must
// equal the site it is claiming for. Unbound (empty) or mismatched → 403. Read
// faces never call this. Returns nil when the claim is authorized.
func enforceSiteBinding(client *siteModel.OAuthClient, site string) *houseError {
	if client == nil || client.CatalogSite == "" {
		return apiErrMsg(http.StatusForbidden, errors.ErrForbidden,
			"client is not bound to a catalog site; it cannot claim works")
	}
	if client.CatalogSite != site {
		return apiErrMsg(http.StatusForbidden, errors.ErrForbidden,
			fmt.Sprintf("client is bound to site %q and cannot claim for site %q", client.CatalogSite, site))
	}
	return nil
}

// AdminBridge lifts the operator's user id (set by middleware.JWTAuth from
// TokenClaims.ID, a uint) into the Huma context so decision endpoints can
// record who acted. The Fiber layer has already rejected non-admins before
// this runs.
func AdminBridge(ctx huma.Context, next func(huma.Context)) {
	fc := humafiber.Unwrap(ctx)
	if id, ok := fc.Locals("user_id").(uint); ok {
		ctx = huma.WithValue(ctx, ctxKeyAdminID, int64(id))
	}
	next(ctx)
}

func adminIDFromCtx(ctx context.Context) int64 {
	id, _ := ctx.Value(ctxKeyAdminID).(int64)
	return id
}
