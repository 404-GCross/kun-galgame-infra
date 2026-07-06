package handler

import (
	"net/http/httptest"
	"testing"

	"api/internal/middleware"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSetup_RegistersS2SOperations: spec smoke — all three S2S operations
// register with nil services (what cmd/gen-openapi -catalog exports).
func TestSetup_RegistersS2SOperations(t *testing.T) {
	api := Setup(fiber.New(), nil, nil)
	paths := api.OpenAPI().Paths
	for _, p := range []string{
		"/api/v1/catalog/resolve",
		"/api/v1/catalog/redirects",
		"/api/v1/catalog/works/claim",
	} {
		assert.NotNilf(t, paths[p], "operation %s must be registered", p)
	}
}

// TestSetupAdmin_RegistersQueueOperations: spec smoke for the three review
// buckets (what cmd/gen-openapi -catalog-admin exports).
func TestSetupAdmin_RegistersQueueOperations(t *testing.T) {
	api := SetupAdmin(fiber.New(), nil, nil)
	paths := api.OpenAPI().Paths
	for _, p := range []string{
		"/api/v1/admin/catalog/candidates",
		"/api/v1/admin/catalog/candidates/decide",
		"/api/v1/admin/catalog/proposals",
		"/api/v1/admin/catalog/proposals/{id}/{action}",
		"/api/v1/admin/catalog/refs/probable",
		"/api/v1/admin/catalog/refs/confirm",
		"/api/v1/admin/catalog/refs/reject",
	} {
		assert.NotNilf(t, paths[p], "operation %s must be registered", p)
	}
}

// TestS2SAuth_Unauthenticated401: the S2S face rejects requests without
// Basic credentials before any handler runs (nil repo is never reached —
// header parsing fails first).
func TestS2SAuth_Unauthenticated401(t *testing.T) {
	app := fiber.New()
	app.Use("/api/v1/catalog", S2SAuth(nil))
	Setup(app, nil, nil)

	for _, header := range []string{"", "Bearer whatever", "Basic not-base64!"} {
		req := httptest.NewRequest("POST", "/api/v1/catalog/resolve", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		resp, err := app.Test(req)
		require.NoError(t, err)
		assert.Equalf(t, fiber.StatusUnauthorized, resp.StatusCode, "header %q must 401", header)
	}
}

// TestAdminGate_403WithoutRole: the admin prefix is gated by RequireRole at
// the Fiber layer — a request that passed JWT parsing but carries no admin
// role must 403 before any catalog handler runs.
func TestAdminGate_403WithoutRole(t *testing.T) {
	app := fiber.New()
	// Stand-in for JWTAuth that authenticates a non-admin user.
	app.Use("/api/v1/admin/catalog", func(c fiber.Ctx) error {
		c.Locals("user_id", uint(42))
		c.Locals("user_roles", []string{"user"})
		return c.Next()
	}, middleware.RequireRole("admin"))
	SetupAdmin(app, nil, nil)

	resp, err := app.Test(httptest.NewRequest("GET", "/api/v1/admin/catalog/candidates", nil))
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusForbidden, resp.StatusCode)
}

// TestRedirectCursorRoundTrip pins the opaque cursor encoding.
func TestRedirectCursorRoundTrip(t *testing.T) {
	c, err := decodeRedirectCursor("")
	require.NoError(t, err)
	assert.Zero(t, c.OldID)

	encoded := encodeRedirectCursor(c)
	_, err = decodeRedirectCursor(encoded)
	require.NoError(t, err)

	_, err = decodeRedirectCursor("!!!not-base64!!!")
	require.Error(t, err)
}
