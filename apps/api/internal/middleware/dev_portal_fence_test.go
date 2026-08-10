package middleware

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

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
