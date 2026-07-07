package handler

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"api/internal/middleware"
	"api/internal/platform/catalog/migrate"
	"api/internal/platform/catalog/repository"
	"api/internal/platform/catalog/seed"
	"api/internal/platform/catalog/service"
	siteModel "api/internal/platform/site/model"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	glogger "gorm.io/gorm/logger"
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

// TestEnforceSiteBinding covers the three binding states as pure logic
// (unbound / mismatch / match) plus the nil-client guard.
func TestEnforceSiteBinding(t *testing.T) {
	assert.NotNil(t, enforceSiteBinding(nil, "galgame_wiki"), "nil client → forbidden")
	assert.NotNil(t, enforceSiteBinding(&siteModel.OAuthClient{CatalogSite: ""}, "galgame_wiki"), "unbound → forbidden")
	assert.NotNil(t, enforceSiteBinding(&siteModel.OAuthClient{CatalogSite: "kungal"}, "galgame_wiki"), "mismatch → forbidden")
	assert.Nil(t, enforceSiteBinding(&siteModel.OAuthClient{CatalogSite: "galgame_wiki"}, "galgame_wiki"), "match → authorized")

	if he := enforceSiteBinding(nil, "x"); assert.NotNil(t, he) {
		assert.Equal(t, http.StatusForbidden, he.GetStatus())
	}
}

// claimApp builds a Fiber app whose /api/v1/catalog prefix injects the given
// client into Locals (standing in for S2SAuth) so S2SBridge lifts it into the
// Huma context, then registers the S2S handlers over the supplied work service.
func claimApp(client *siteModel.OAuthClient, work *service.WorkService) *fiber.App {
	app := fiber.New()
	app.Use("/api/v1/catalog", func(c fiber.Ctx) error {
		if client != nil {
			c.Locals(localClient, client)
		}
		return c.Next()
	})
	Setup(app, nil, work)
	return app
}

func postClaim(t *testing.T, app *fiber.App, body string) int {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/v1/catalog/works/claim", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	return resp.StatusCode
}

// TestClaimSiteBinding_Forbidden: an unbound or mismatched client is rejected
// at the claim endpoint before any work service call (nil work is never
// reached — enforcement returns first).
func TestClaimSiteBinding_Forbidden(t *testing.T) {
	body := `{"medium_id":1,"site":"galgame_wiki","product_work_id":1,"display_name":"X"}`
	for _, tc := range []struct {
		name   string
		client *siteModel.OAuthClient
	}{
		{"unbound", &siteModel.OAuthClient{ID: "c1", CatalogSite: ""}},
		{"wrong-site", &siteModel.OAuthClient{ID: "c2", CatalogSite: "kungal"}},
	} {
		app := claimApp(tc.client, nil)
		assert.Equalf(t, fiber.StatusForbidden, postClaim(t, app, body), "%s must 403", tc.name)
	}

	// Read faces impose no binding: resolve with an unbound client is NOT 403.
	app := claimApp(&siteModel.OAuthClient{ID: "c3", CatalogSite: ""}, nil)
	req := httptest.NewRequest("POST", "/api/v1/catalog/resolve", strings.NewReader(`{"items":[]}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.NotEqual(t, fiber.StatusForbidden, resp.StatusCode, "resolve is unaffected by site binding")
}

// TestClaimSiteBinding_Allowed: a correctly-bound client claims successfully
// (200). DB-backed — skips if the catalog test database is unreachable.
func TestClaimSiteBinding_Allowed(t *testing.T) {
	db := openCatalogTestDB(t)
	work := service.NewWorkService(db, service.NewResolveService(repository.NewRedirectRepository(db)))
	app := claimApp(&siteModel.OAuthClient{ID: "bound-client", CatalogSite: "galgame_wiki"}, work)

	body := `{"medium_id":1,"site":"galgame_wiki","product_work_id":990016,"display_name":"バインドテスト","olang":"ja"}`
	assert.Equal(t, fiber.StatusOK, postClaim(t, app, body), "bound client claims for its own site → 200")
}

func openCatalogTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		dsn = "host=localhost port=5432 user=postgres password=postgres dbname=kun_catalog_test sslmode=disable"
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: glogger.Default.LogMode(glogger.Silent)})
	if err != nil {
		t.Skipf("catalog test database unreachable: %v", err)
	}
	if err := migrate.Run(db); err != nil {
		t.Skipf("catalog migrate failed: %v", err)
	}
	if err := seed.Run(db); err != nil {
		t.Skipf("catalog seed failed: %v", err)
	}
	return db
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
