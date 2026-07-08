package middleware

import (
	stderrors "errors"
	"strings"

	"api/pkg/errors"
	"api/pkg/oidctoken"

	"github.com/gofiber/fiber/v3"
)

// keyUnavailable writes the 503 for a key-store outage (JWKS unreachable):
// the service's failure, not the caller's — a 401 would make RPs kill live
// sessions, and OptionalJWT would silently serve logged-in users anonymous
// responses.
func keyUnavailable(c fiber.Ctx) error {
	return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
		"code":    errors.ErrOperationFailed,
		"message": "令牌验证暂不可用，请稍后重试",
	})
}

// JWTAuth is a lightweight JWT middleware backed by an accept-both verifier
// (ES256/RS256 via JWKS + legacy HS256). Unlike Auth(), it does not depend on
// AuthService — suitable for services that only need to verify tokens.
func JWTAuth(verifier *oidctoken.Verifier) fiber.Handler {
	return func(c fiber.Ctx) error {
		authHeader := c.Get("Authorization")
		if authHeader == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"code":    errors.ErrAuthUnauthorized,
				"message": errors.GetMessage(errors.ErrAuthUnauthorized),
			})
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"code":    errors.ErrAuthInvalidToken,
				"message": errors.GetMessage(errors.ErrAuthInvalidToken),
			})
		}

		claims, err := verifier.Parse(c.Context(), parts[1])
		if err != nil {
			if stderrors.Is(err, oidctoken.ErrKeyUnavailable) {
				return keyUnavailable(c)
			}
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"code":    errors.ErrAuthTokenExpired,
				"message": errors.GetMessage(errors.ErrAuthTokenExpired),
			})
		}

		c.Locals("user_uuid", claims.UserUUID)
		c.Locals("user_id", claims.ID)
		c.Locals("user_roles", unionRoles(claims.Roles, claims.SiteRoles))
		c.Locals("user_scope", claims.Scope)
		c.Locals("user_site", claims.SiteID)

		return c.Next()
	}
}

// unionRoles merges the caller's global `roles` claim with its `site_roles`
// claim (already scoped by the JWT signer to the token's client site) into one
// effective role set for permission resolution — this is the whole of the
// site-scoped-roles wiring on the infra consumer side, so no call site changes.
//
// Safety: site_roles can NEVER contain user/admin/ren (the grant API rejects
// those names), so this union only ever ADDS site-local moderator/creator/
// custom-bundle names. It can never let a site grant reach an admin/ren-only
// permission — the catalog / oauth-console / artifact bundles key only on
// admin/ren, which site_roles structurally cannot carry. A site "moderator"
// thus gains moderator capabilities, but only via a token issued to that site's
// client. See docs/integration/oauth/12-site-roles.md.
func unionRoles(global, site []string) []string {
	if len(site) == 0 {
		return global
	}
	seen := make(map[string]struct{}, len(global)+len(site))
	out := make([]string, 0, len(global)+len(site))
	for _, r := range global {
		if _, ok := seen[r]; !ok {
			seen[r] = struct{}{}
			out = append(out, r)
		}
	}
	for _, r := range site {
		if _, ok := seen[r]; !ok {
			seen[r] = struct{}{}
			out = append(out, r)
		}
	}
	return out
}

// OptionalJWT is like JWTAuth but never blocks the request: when the
// Authorization header is missing or invalid, the request proceeds with
// no user_id in Locals. When valid, it populates the same locals as JWTAuth.
//
// Useful for endpoints whose response shape changes for authenticated
// callers (e.g. /galgame/search ?include_pending=true,
// /galgame/batch returning the caller's pending drafts) without forcing
// every anonymous caller to authenticate.
func OptionalJWT(verifier *oidctoken.Verifier) fiber.Handler {
	return func(c fiber.Ctx) error {
		authHeader := c.Get("Authorization")
		if authHeader == "" {
			return c.Next()
		}
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			return c.Next()
		}
		claims, err := verifier.Parse(c.Context(), parts[1])
		if err != nil {
			// A key-store outage must not demote a presented token to anonymous
			// (users would see their own pending drafts silently vanish) — fail
			// loud so the caller retries.
			if stderrors.Is(err, oidctoken.ErrKeyUnavailable) {
				return keyUnavailable(c)
			}
			return c.Next()
		}
		c.Locals("user_uuid", claims.UserUUID)
		c.Locals("user_id", claims.ID)
		c.Locals("user_roles", unionRoles(claims.Roles, claims.SiteRoles))
		c.Locals("user_scope", claims.Scope)
		c.Locals("user_site", claims.SiteID)
		return c.Next()
	}
}
