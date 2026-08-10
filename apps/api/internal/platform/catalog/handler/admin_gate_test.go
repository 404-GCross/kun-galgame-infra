package handler

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
		{[]string{"moderator"}, "/api/v1/admin/catalog/image-references", fiber.StatusForbidden},
		{[]string{"ren"}, "/api/v1/admin/catalog/image-references", fiber.StatusOK},
		{[]string{"ren"}, "/api/v1/admin/catalog/image-references/detach", fiber.StatusOK},
		{[]string{"user"}, "/api/v1/admin/catalog/claims/pending", fiber.StatusForbidden},
		{[]string{"creator"}, "/api/v1/admin/catalog/claims/pending", fiber.StatusForbidden},
	}
	for _, tc := range cases {
		app := fiber.New()
		app.Use("/api/v1/admin/catalog", func(c fiber.Ctx) error {
			c.Locals("user_id", uint(42))
			c.Locals("user_roles", tc.roles)
			return c.Next()
		}, AdminGate(userEditClients()))
		app.All("/api/v1/admin/catalog/*", func(c fiber.Ctx) error { return c.SendString("ok") })

		resp, err := app.Test(httptest.NewRequest("GET", tc.path, nil))
		require.NoError(t, err)
		assert.Equalf(t, tc.wantStatus, resp.StatusCode, "roles %v on %s", tc.roles, tc.path)
	}
}

func adminGateApp(roles []string, clientID string) *fiber.App {
	app := fiber.New()
	app.Use("/api/v1/admin/catalog", func(c fiber.Ctx) error {
		c.Locals("user_id", uint(42))
		c.Locals("user_roles", roles)
		c.Locals("token_client_id", clientID)
		return c.Next()
	}, AdminGate(userEditClients()))
	app.All("/api/v1/admin/catalog/*", func(c fiber.Ctx) error { return c.SendString("ok") })
	return app
}

func TestAdminGate_ThirdPartyClientIsNotAModerationSurface(t *testing.T) {
	paths := []string{
		"/api/v1/admin/catalog/candidates",
		"/api/v1/admin/catalog/claims/pending",
	}
	for _, path := range paths {
		resp, err := adminGateApp([]string{"ren"}, "thirdparty-kungal").
			Test(httptest.NewRequest("GET", path, nil))
		require.NoError(t, err)
		assert.Equalf(t, fiber.StatusForbidden, resp.StatusCode, "ren via a third-party app on %s", path)

		resp, err = adminGateApp([]string{"user"}, "thirdparty-kungal").
			Test(httptest.NewRequest("GET", path, nil))
		require.NoError(t, err)
		assert.Equalf(t, fiber.StatusForbidden, resp.StatusCode, "user via a third-party app on %s", path)

		resp, err = adminGateApp([]string{"ren"}, "kungal-client").
			Test(httptest.NewRequest("GET", path, nil))
		require.NoError(t, err)
		assert.Equalf(t, fiber.StatusOK, resp.StatusCode, "ren via a first-party client on %s", path)
	}

	resp, err := adminGateApp([]string{"ren"}, "deleted-app").
		Test(httptest.NewRequest("GET", "/api/v1/admin/catalog/candidates", nil))
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusForbidden, resp.StatusCode, "an unregistered client is refused")

	resp, err = adminGateApp([]string{"ren"}, "").
		Test(httptest.NewRequest("GET", "/api/v1/admin/catalog/candidates", nil))
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, resp.StatusCode,
		"a client-less first-party session token still reaches the permission check")
}
