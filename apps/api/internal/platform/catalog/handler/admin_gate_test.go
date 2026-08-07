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
		// The image-reference check hangs off the curation branch: it edits the
		// registry's own rows, so a moderator must not reach it.
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

// adminGateApp mounts AdminGate behind a stand-in for JWTAuth that publishes
// the three locals a verified token would: the operator, their role union, and
// the id of the client the token was issued through.
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

// TestAdminGate_ThirdPartyClientIsNotAModerationSurface pins wave 187b: the
// admin face is the third surface where standing belongs to the PAIR (person x
// first-party client). Same ren, same path — only the application the token was
// issued through differs, and that is enough.
func TestAdminGate_ThirdPartyClientIsNotAModerationSurface(t *testing.T) {
	paths := []string{
		"/api/v1/admin/catalog/candidates",     // the curation branch (ren)
		"/api/v1/admin/catalog/claims/pending", // the claim-review branch (moderator+)
	}
	for _, path := range paths {
		// Through a third-party developer application: refused, whatever the
		// roles say — and refused BEFORE the permission, so the answer is the
		// same for a ren and for a nobody, and never tells the caller which.
		resp, err := adminGateApp([]string{"ren"}, "thirdparty-kungal").
			Test(httptest.NewRequest("GET", path, nil))
		require.NoError(t, err)
		assert.Equalf(t, fiber.StatusForbidden, resp.StatusCode, "ren via a third-party app on %s", path)

		resp, err = adminGateApp([]string{"user"}, "thirdparty-kungal").
			Test(httptest.NewRequest("GET", path, nil))
		require.NoError(t, err)
		assert.Equalf(t, fiber.StatusForbidden, resp.StatusCode, "user via a third-party app on %s", path)

		// The identical person through a first-party site client still passes.
		resp, err = adminGateApp([]string{"ren"}, "kungal-client").
			Test(httptest.NewRequest("GET", path, nil))
		require.NoError(t, err)
		assert.Equalf(t, fiber.StatusOK, resp.StatusCode, "ren via a first-party client on %s", path)
	}

	// A token naming a client this registry does not know is refused: nothing
	// vouches for the surface it came from.
	resp, err := adminGateApp([]string{"ren"}, "deleted-app").
		Test(httptest.NewRequest("GET", "/api/v1/admin/catalog/candidates", nil))
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusForbidden, resp.StatusCode, "an unregistered client is refused")

	// The ADMITTED gap, pinned so it cannot change by accident: this platform's
	// own console signs staff in through /auth/login, whose tokens carry no
	// client_id claim at all, so an empty claim must still reach the permission
	// check. See refuseThirdPartyAdminClient's TODO.
	resp, err = adminGateApp([]string{"ren"}, "").
		Test(httptest.NewRequest("GET", "/api/v1/admin/catalog/candidates", nil))
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, resp.StatusCode,
		"a client-less first-party session token still reaches the permission check")
}
