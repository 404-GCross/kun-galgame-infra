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
		c.Locals("user_roles", claims.Roles)
		c.Locals("user_scope", claims.Scope)

		return c.Next()
	}
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
		c.Locals("user_roles", claims.Roles)
		c.Locals("user_scope", claims.Scope)
		return c.Next()
	}
}
