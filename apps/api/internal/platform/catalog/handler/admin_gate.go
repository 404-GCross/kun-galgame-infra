package handler

import (
	"strings"

	"api/internal/middleware"
	catperm "api/internal/platform/catalog/perm"
	"api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
)

// AdminClaimsPrefix is the staff claim-review subtree of the admin face.
const AdminClaimsPrefix = "/api/v1/admin/catalog/claims"

// AdminGate is the permission gate for the whole /api/v1/admin/catalog prefix.
//
// TWO authorities live under that one prefix and a single RequirePermission
// cannot express both: the claim review queue is content moderation
// (catalog.claim.review, moderator and up), everything else — candidates,
// merge proposals, probable refs — is curation of the identity registry itself
// (catalog.review, ren only). Fiber runs EVERY prefix middleware that matches a
// nested path, so the narrower branch cannot simply be layered on top of the
// broader one: registering a second Use on .../claims would leave the ren gate
// in the chain and keep moderators out. The choice therefore has to be made
// inside one handler, which is this.
//
// It lives beside the routes rather than in cmd/catalog so the split is pinned
// by the handler package's own tests.
//
// Before EITHER permission is consulted the gate asks which application the
// token was issued through (wave 187b). Both authorities under this prefix are
// properties of the PAIR (person x first-party client), and a staff member's
// roles travel with their token into every app they authorize — so without this
// check any third-party developer application holding a session for a ren could
// drive the identity registry from a UI nobody at the platform wrote. clients
// resolves the token's client id; it is required, and the whole gate is a
// permission gate, so there is nothing sensible to do with a nil registry.
func AdminGate(clients OAuthClientLookup) fiber.Handler {
	curation := middleware.RequirePermission(catperm.Resolver, catperm.Review)
	claims := middleware.RequirePermission(catperm.Resolver, catperm.ClaimReview)
	return func(c fiber.Ctx) error {
		if err := refuseThirdPartyAdminClient(c, clients); err != nil {
			return err
		}
		if strings.HasPrefix(c.Path(), AdminClaimsPrefix) {
			return claims(c)
		}
		return curation(c)
	}
}

// refuseThirdPartyAdminClient rejects an admin call whose token was issued
// through a third-party developer application. Refused BEFORE the permission
// check, so the message never doubles as a probe for who is staff.
//
// The absent-client-id case is deliberately ADMITTED, and that is a real gap,
// not an oversight: this platform's own console signs its operators in through
// /auth/login, whose tokens carry no client_id claim at all (see
// utils.TokenClaims.ClientID — "Empty for first-party /auth/login tokens, which
// have no client"). Failing closed on the empty claim would lock every human
// staff member out of the admin face. The gap it leaves is narrow — a token
// with no client id is one this OP minted for its own first-party session flow,
// which is exactly the surface being admitted — but it is only narrow as long
// as that stays true.
//
// TODO(auth): once every access token this OP issues carries a client id
// (the first-party session flow would need its own registered client, tracked
// with the OIDC standardization work in docs/auth/03), delete the empty-claim
// branch below and fail closed like the user plane's UserGate already does.
func refuseThirdPartyAdminClient(c fiber.Ctx, clients OAuthClientLookup) error {
	clientID, _ := c.Locals("token_client_id").(string)
	if clientID == "" {
		return nil
	}
	client, err := clients.FindByClientID(c.Context(), clientID)
	if err != nil || client == nil {
		// The token names a client this registry does not know: it was minted
		// for an application that has since been deleted, so nothing vouches
		// for the surface any more.
		return response.ForbiddenMsg(c, errors.ErrForbidden,
			"the access token's client is not registered")
	}
	if isThirdPartyClient(client) {
		return response.ForbiddenMsg(c, errors.ErrForbidden,
			"a third-party application is not a moderation surface; the catalog admin face needs a first-party client")
	}
	return nil
}
