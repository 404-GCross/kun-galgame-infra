package middleware

import (
	"fmt"
	"log/slog"
	"strings"

	authService "api/internal/platform/auth/service"

	"github.com/gofiber/fiber/v3"
)

// bearerRealm names the protection space in the WWW-Authenticate challenge.
const bearerRealm = "kungal"

// splitBearer extracts the credentials from an Authorization header carrying
// the Bearer scheme.
//
// RFC 7235 §2.1 makes the auth-scheme case-INSENSITIVE, so `bearer x` and
// `BEARER x` are exactly as valid as `Bearer x`. Standard OAuth client
// libraries emit all three in the wild, and rejecting the lowercase form is a
// spec violation that reads to the caller as "your token is bad" — an
// expensive thing to debug from the outside.
func splitBearer(header string) (token string, ok bool) {
	scheme, rest, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	return rest, true
}

// bearerChallenge writes the RFC 6750 §3 WWW-Authenticate challenge.
//
// Without it a 401 body is guesswork for a standard client: it cannot tell
// "this token is dead, go refresh" from "your request was malformed", which is
// precisely how an Auth0 integration burned a day against this server. Pass an
// empty errCode for the no-credentials case — RFC 6750 §3 says a request that
// carried no authentication information SHOULD NOT be given an error code.
func bearerChallenge(c fiber.Ctx, errCode, desc string) {
	if errCode == "" {
		c.Set(fiber.HeaderWWWAuthenticate, fmt.Sprintf("Bearer realm=%q", bearerRealm))
		return
	}
	c.Set(fiber.HeaderWWWAuthenticate,
		fmt.Sprintf("Bearer realm=%q, error=%q, error_description=%q", bearerRealm, errCode, desc))
}

// BearerError writes an RFC 6750 §3 error: the challenge header plus the
// matching JSON body. The no-credentials case carries no body at all, per the
// same rule that suppresses its error code.
//
// Exported because /oauth/userinfo's handler needs it too — OIDC Core §5.3.3
// routes UserInfo failures through RFC 6750 rather than RFC 6749 §5.2, and
// having one writer keeps the realm and header grammar from drifting apart.
func BearerError(c fiber.Ctx, status int, errCode, desc string) error {
	bearerChallenge(c, errCode, desc)
	if errCode == "" {
		// Status + challenge only. Deliberately NOT SendStatus, which would put
		// the status text ("Unauthorized") in the body — plain-text noise for a
		// client that parses every response as JSON.
		c.Status(status)
		return nil
	}
	return c.Status(status).JSON(fiber.Map{
		"error":             errCode,
		"error_description": desc,
	})
}

// BearerAuth is the protected-resource guard for OIDC *protocol* endpoints —
// today /oauth/userinfo.
//
// It performs the same checks as Auth (signature, expiry, live ban status) but
// answers in the RFC 6750 §3 error format that OIDC Core §5.3.3 mandates for
// the UserInfo endpoint, instead of the house {code,message} body. That house
// body is correct for our own API surface and stays there; it is simply not
// what a standard OIDC client can parse.
//
// A banned account has no dedicated RFC 6750 code, so it surfaces as
// invalid_token on HTTP 403 — the status is what first-party RPs key on to
// keep showing a distinct banned page rather than a re-login loop.
func BearerAuth(authSvc *authService.AuthService) fiber.Handler {
	return func(c fiber.Ctx) error {
		authHeader := c.Get(fiber.HeaderAuthorization)
		if authHeader == "" {
			return BearerError(c, fiber.StatusUnauthorized, "", "")
		}

		token, ok := splitBearer(authHeader)
		if !ok || token == "" {
			// RFC 6750 §3.1: a malformed request is invalid_request / 400,
			// not invalid_token — the credential was never legible enough to
			// judge.
			return BearerError(c, fiber.StatusBadRequest, "invalid_request",
				"Authorization header must use the Bearer scheme")
		}

		claims, err := authSvc.ValidateAccessToken(token)
		if err != nil {
			// Debug, not Warn: every user hits this every 15 min when their
			// access token expires normally and the client refreshes.
			slog.Debug("bearer reject", "stage", "token_invalid", "path", c.Path(), "err", err)
			return BearerError(c, fiber.StatusUnauthorized, "invalid_token",
				"The access token is expired, revoked or malformed")
		}

		// Live status check — the JWT can still be valid while the account was
		// banned inside its 15-minute window.
		user, err := authSvc.GetCurrentUser(c.Context(), claims.UserUUID)
		if err != nil || user == nil {
			slog.Warn("bearer reject", "stage", "get_current_user",
				"path", c.Path(), "user_uuid", claims.UserUUID, "err", err)
			return BearerError(c, fiber.StatusUnauthorized, "invalid_token",
				"The access token does not identify a known user")
		}
		if user.IsBanned() {
			return BearerError(c, fiber.StatusForbidden, "invalid_token",
				"The account is banned")
		}

		setIdentityLocals(c, claims)

		return c.Next()
	}
}
