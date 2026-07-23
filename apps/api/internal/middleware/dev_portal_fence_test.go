package middleware

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// TestDevPortalFence pins the /dev/* client fence: first-party tokens (empty
// client_id) and allow-listed clients pass; a stranger client is 403; and an
// empty allowlist is fail-closed — it rejects every client token while still
// admitting first-party ones. The token_client_id local is set by a preceding
// handler here, exactly as middleware.Auth publishes it in production.
func TestDevPortalFence(t *testing.T) {
	portal := map[string]bool{"portal-client": true}
	empty := map[string]bool{}

	cases := []struct {
		name     string
		clientID string
		allowed  map[string]bool
		want     int
	}{
		{"first-party token (empty client) passes", "", portal, fiber.StatusOK},
		{"allow-listed portal client passes", "portal-client", portal, fiber.StatusOK},
		{"stranger third-party client 403", "evil-app", portal, fiber.StatusForbidden},
		{"empty allowlist rejects any client token", "portal-client", empty, fiber.StatusForbidden},
		{"empty allowlist still admits first-party", "", empty, fiber.StatusOK},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := fiber.New()
			app.Get("/dev/apps",
				func(c fiber.Ctx) error {
					// Simulate middleware.Auth publishing the client id local.
					c.Locals("token_client_id", tc.clientID)
					return c.Next()
				},
				DevPortalFence(tc.allowed),
				func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) },
			)

			resp, err := app.Test(httptest.NewRequest("GET", "/dev/apps", nil))
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != tc.want {
				t.Errorf("status = %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
}
