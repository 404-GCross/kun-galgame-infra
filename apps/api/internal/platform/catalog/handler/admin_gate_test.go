package handler

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAdminGate_SplitsClaimReviewFromCuration pins wave 157's whole point: a
// moderator staffs the claim queue but may not touch the identity registry,
// while ren keeps both. The gate is exercised on its own (a terminal handler
// that just answers 200) so the assertion is about authority, not about any
// queue's SQL.
func TestAdminGate_SplitsClaimReviewFromCuration(t *testing.T) {
	cases := []struct {
		roles      []string
		path       string
		wantStatus int
	}{
		{[]string{"moderator"}, "/api/v1/admin/catalog/claims/pending", fiber.StatusOK},
		{[]string{"moderator"}, "/api/v1/admin/catalog/claims/7/approve", fiber.StatusOK},
		{[]string{"moderator"}, "/api/v1/admin/catalog/candidates", fiber.StatusForbidden},
		{[]string{"admin"}, "/api/v1/admin/catalog/claims/pending", fiber.StatusOK},
		{[]string{"admin"}, "/api/v1/admin/catalog/candidates", fiber.StatusForbidden},
		{[]string{"ren"}, "/api/v1/admin/catalog/claims/pending", fiber.StatusOK},
		{[]string{"ren"}, "/api/v1/admin/catalog/candidates", fiber.StatusOK},
		{[]string{"user"}, "/api/v1/admin/catalog/claims/pending", fiber.StatusForbidden},
		{[]string{"creator"}, "/api/v1/admin/catalog/claims/pending", fiber.StatusForbidden},
	}
	for _, tc := range cases {
		app := fiber.New()
		app.Use("/api/v1/admin/catalog", func(c fiber.Ctx) error {
			c.Locals("user_id", uint(42))
			c.Locals("user_roles", tc.roles)
			return c.Next()
		}, AdminGate())
		app.All("/api/v1/admin/catalog/*", func(c fiber.Ctx) error { return c.SendString("ok") })

		resp, err := app.Test(httptest.NewRequest("GET", tc.path, nil))
		require.NoError(t, err)
		assert.Equalf(t, tc.wantStatus, resp.StatusCode, "roles %v on %s", tc.roles, tc.path)
	}
}
