package handler

import (
	"context"
	"net/http"
	"strings"

	siteModel "api/internal/platform/site/model"
	"api/pkg/errors"
	"api/pkg/response"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humafiber"
	"github.com/gofiber/fiber/v3"
)

// The USER-TOKEN write plane (wave 176).
//
// The third catalog face, beside the S2S one (Basic client credentials, actor
// ASSERTED in the body) and the admin one (staff JWT + a permission). Here the
// caller is the end user's own browser session, carrying an OAuth access token,
// and the doctrine is one line:
//
//	the actor comes from the verified token, and the tenant comes from the
//	client the token was issued to — NOTHING about identity is taken from the
//	request body.
//
// That is the whole difference from the S2S face. There, a product backend says
// "user 5 of site kungal did this" and the catalog believes it because the
// backend authenticated itself; a bug or a compromised backend can therefore
// write as anyone. On this face there is no field to lie in: the uid is the
// token's `id` claim and the site is oauth_clients.catalog_site of the token's
// `client_id`, both resolved server-side.
//
// The chain, in order, is:
//
//	1. middleware.JWTAuth  — signature/expiry (401; JWKS outage → 503).
//	2. scope               — the token's grant must carry catalog:edit (403).
//	3. client binding      — the token must BE client-bound (403 otherwise) and
//	                         that client must be bound to a catalog site (403).
//
// Step 3's first half is why a first-party /auth/login token is refused: it has
// no `client_id` claim (RFC 9068 §2.2 is optional for it), so there is no site
// to attribute the write to. Asking such a caller to name its own site would
// re-open exactly the assertion hole this face exists to close.
//
// Prefix: /api/v1/user/catalog — deliberately disjoint from /api/v1/catalog and
// /api/v1/admin/catalog, because Huma registers on the Fiber APP and the
// path-scoped Use middleware is therefore the only gate. An overlapping prefix
// would put the S2S Basic auth in front of these routes.

// ScopeCatalogEdit is the OAuth scope a user token must carry to write on the
// catalog user plane. It is a USER scope (granted through the OP's consent
// flow), not one of the developer-platform API-key scopes in
// internal/platform/devapi — those key a different credential type entirely.
// Named here beside the face that enforces it, following the image service's
// precedent for image:upload.
const ScopeCatalogEdit = "catalog:edit"

// UserPrefix is the mount point of the user-token write plane.
const UserPrefix = "/api/v1/user/catalog"

const ctxKeyUserID ctxKey = "catalog:user_id"

// OAuthClientLookup is the slice of the OAuth client registry this face needs:
// resolve the token's client to learn its catalog site. *siteRepo.OAuthClientRepository
// satisfies it; the narrow interface is what lets the gate be tested without a
// second database.
type OAuthClientLookup interface {
	FindByClientID(ctx context.Context, clientID string) (*siteModel.OAuthClient, error)
}

// UserGate is the authorization half of the user plane's chain, applied as
// path-scoped Fiber middleware immediately AFTER middleware.JWTAuth (which has
// already rejected an absent/invalid token). It reads only locals the verified
// token produced, and hands the resolved client down to the ops as the S2S face
// does — so the write path below it needs no idea where its site came from.
func UserGate(clients OAuthClientLookup) fiber.Handler {
	return func(c fiber.Ctx) error {
		// A token that names no user cannot act as one. JWTAuth cannot catch
		// this (a claim of 0 is a well-formed token), and the ops must never
		// see a system-attributed ballot, so it is refused as an identity
		// failure rather than an authorization one.
		uid, _ := c.Locals("user_id").(uint)
		if uid == 0 {
			return response.UnauthorizedMsg(c, errors.ErrAuthUnauthorized,
				"the access token carries no user identity")
		}

		scope, _ := c.Locals("user_scope").(string)
		if !hasScope(scope, ScopeCatalogEdit) {
			return response.ForbiddenMsg(c, errors.ErrForbidden,
				"the access token is missing the "+ScopeCatalogEdit+" scope")
		}

		clientID, _ := c.Locals("token_client_id").(string)
		if clientID == "" {
			return response.ForbiddenMsg(c, errors.ErrForbidden,
				"the access token is not bound to an OAuth client; the catalog user plane requires a client-bound token")
		}
		client, err := clients.FindByClientID(c.Context(), clientID)
		if err != nil || client == nil {
			return response.ForbiddenMsg(c, errors.ErrForbidden,
				"the access token's client is not registered")
		}
		if client.CatalogSite == "" {
			return response.ForbiddenMsg(c, errors.ErrForbidden,
				"the access token's client is not bound to a catalog site")
		}

		c.Locals(localClient, client)
		return c.Next()
	}
}

// hasScope is an exact-word match over the space-separated `scope` claim
// (RFC 6749 §3.3). Substring matching would let "catalog:editor" — or a
// hypothetical "no-catalog:edit" — pass.
func hasScope(scope, want string) bool {
	for _, s := range strings.Fields(scope) {
		if s == want {
			return true
		}
	}
	return false
}

// UserBridge lifts the two things the ops are allowed to know about the caller
// — the token's user id and the client UserGate resolved — into the Huma
// context. Both are derived from the verified token; neither can be influenced
// by the request body.
func UserBridge(ctx huma.Context, next func(huma.Context)) {
	fc := humafiber.Unwrap(ctx)
	if id, ok := fc.Locals("user_id").(uint); ok {
		ctx = huma.WithValue(ctx, ctxKeyUserID, int64(id))
	}
	if client, ok := fc.Locals(localClient).(*siteModel.OAuthClient); ok {
		ctx = huma.WithValue(ctx, ctxKeyClient, client)
	}
	next(ctx)
}

func userIDFromCtx(ctx context.Context) int64 {
	id, _ := ctx.Value(ctxKeyUserID).(int64)
	return id
}

// userActor is the ops' single entry point to "who is writing, and for whom".
// It re-checks what UserGate already enforced, because a face that trusts a
// middleware it does not itself install is one route registration away from
// writing unattributed rows.
func userActor(ctx context.Context) (uid int64, site string, he *houseError) {
	uid = userIDFromCtx(ctx)
	if uid <= 0 {
		return 0, "", apiErrMsg(http.StatusUnauthorized, errors.ErrAuthUnauthorized,
			"the access token carries no user identity")
	}
	client := clientFromCtx(ctx)
	if client == nil || client.CatalogSite == "" {
		return 0, "", apiErrMsg(http.StatusForbidden, errors.ErrForbidden,
			"the access token's client is not bound to a catalog site")
	}
	return uid, client.CatalogSite, nil
}
