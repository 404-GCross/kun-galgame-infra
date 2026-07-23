package middleware

import (
	"log/slog"

	"api/pkg/errors"
	"api/pkg/response"

	"github.com/gofiber/fiber/v3"
)

// DevPortalFence gates the developer self-service face (/dev/*) on the OAuth
// client the caller's token was issued to. It runs AFTER Auth and reads the
// token_client_id local Auth already publishes (setIdentityLocals) — it never
// re-parses the token.
//
// Why (confused-deputy): Auth verifies the token's signature and that the user
// exists/isn't banned — but NOT which OAuth client the access token belongs to.
// Without this fence, any third-party OAuth app holding a user token scoped only
// to `openid profile email` could mint/rotate/revoke API keys on the user's
// behalf at /dev/*. The fence admits only:
//
//   - first-party /auth/login session tokens (empty client_id — they have no
//     OAuth client), and
//   - tokens issued to a client in `allowed` (the developer portal's own
//     confidential client).
//
// Everything else is rejected with 403 and a WARN carrying the client id (never
// the token). `allowed` is built from KUN_DEV_PORTAL_CLIENT_IDS; an EMPTY
// allowlist admits first-party tokens ONLY (fail-closed), mirroring the trust
// forwarder allowlist (KUN_TRUST_FORWARDER_CLIENT_IDS).
//
// A `dev:manage` scope + consent-screen copy is the future upgrade path for
// opening /dev/* to arbitrary third parties; until then the fence is the hard
// pre-condition. See docs/developer-platform/03-auth-and-tiers.md.
func DevPortalFence(allowed map[string]bool) fiber.Handler {
	return func(c fiber.Ctx) error {
		clientID, _ := c.Locals("token_client_id").(string)
		if clientID == "" || allowed[clientID] {
			return c.Next()
		}
		slog.Warn("dev portal fence reject", "client_id", clientID, "path", c.Path())
		return response.ForbiddenMsg(c, errors.ErrForbidden,
			"this application is not authorized to access the developer platform")
	}
}
