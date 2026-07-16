package handler

import (
	"encoding/base64"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"api/internal/platform/ai/dbtest"
	aiMigrate "api/internal/platform/ai/migrate"
	aiModel "api/internal/platform/ai/model"
	"api/internal/platform/ai/service"
	"api/internal/platform/ai/upstream"
	siteModel "api/internal/platform/site/model"
	siteRepo "api/internal/platform/site/repository"

	"github.com/gofiber/fiber/v3"
	"gorm.io/datatypes"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// The handler package tests the S2S auth end-to-end (Basic client credentials →
// catalog_site tenant derivation) over a real Fiber request, plus the spec
// smoke. TestMain provisions the ai schema (migrate.Run) AND the oauth_clients /
// sites tables the S2S auth reads (AutoMigrate) in ONE database.

var testDB *gorm.DB

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		dsn = "host=localhost port=5432 user=postgres password=postgres dbname=kun_ai_test sslmode=disable"
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: cannot connect to test database: %v\n", err)
		os.Exit(0)
	}
	sqlDB, _ := db.DB()
	release := dbtest.AcquireSuiteLock(sqlDB)

	if err := aiMigrate.Run(db); err != nil {
		release()
		fmt.Fprintf(os.Stderr, "SKIP: ai migration failed: %v\n", err)
		os.Exit(0)
	}
	// The S2S auth reads oauth_clients (+ its sites FK) from the main DB; in the
	// shared test DB we provision just those tables so authenticateBasic works.
	if err := db.AutoMigrate(&siteModel.Site{}, &siteModel.OAuthClient{}); err != nil {
		release()
		fmt.Fprintf(os.Stderr, "SKIP: oauth_clients migration failed: %v\n", err)
		os.Exit(0)
	}

	testDB = db
	code := m.Run()
	release()
	os.Exit(code)
}

// TestSpecExport is the S2S spec smoke: Setup with a nil service (the
// gen-openapi path) must produce a valid OpenAPI document with the moderate-text
// operation.
func TestSpecExport(t *testing.T) {
	api := Setup(fiber.New(), nil)
	b, err := api.OpenAPI().YAML()
	if err != nil {
		t.Fatalf("marshal spec: %v", err)
	}
	spec := string(b)
	for _, want := range []string{
		"/api/v1/ai/moderate-text",
		"operationId: moderateText",
		"degraded",
		"flagged",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("spec missing %q", want)
		}
	}
}

// seedClient upserts an oauth_clients row with a hashed secret and a
// catalog_site binding. SiteID is nil (nullable) so no FK row is needed.
func seedClient(t *testing.T, id, secret, catalogSite string) {
	t.Helper()
	testDB.Where("id = ?", id).Delete(&siteModel.OAuthClient{})
	c := &siteModel.OAuthClient{
		ID:           id,
		Name:         "ai s2s test " + id,
		Secret:       siteModel.HashOAuthClientSecret(secret),
		RedirectURIs: datatypes.JSON([]byte("[]")),
		Grants:       datatypes.JSON([]byte("[]")),
		CatalogSite:  catalogSite,
	}
	if err := testDB.Create(c).Error; err != nil {
		t.Fatalf("seed client %s: %v", id, err)
	}
}

// basicAuth builds the "Basic <b64(id:secret)>" header value.
func basicAuth(id, secret string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(id+":"+secret))
}

// buildApp wires a Fiber app exactly like cmd/ai: S2SAuth path-scoped before the
// Huma moderate-text route, backed by an UNCONFIGURED upstream (degraded — so a
// successful auth returns 200 with degraded:true without any network call).
func buildApp() *fiber.App {
	app := fiber.New()
	repo := siteRepo.NewOAuthClientRepository(testDB)
	app.Use("/api/v1/ai", S2SAuth(repo))
	svc := service.NewModerationService(testDB, upstream.NewOmniClient("", "", ""), upstream.NewClient("", "", ""), service.ModerationOptions{})
	Setup(app, svc)
	return app
}

// TestS2SAuthHTTP exercises the four S2S outcomes end-to-end: correct creds on a
// site-bound client → 200; wrong secret → 401; unknown client → 401; a client
// bound to NO catalog_site → 403. The tenant is derived from the binding, never
// the body.
func TestS2SAuthHTTP(t *testing.T) {
	if err := testDB.Exec("TRUNCATE ai_usage RESTART IDENTITY").Error; err != nil {
		t.Fatalf("truncate ai_usage: %v", err)
	}
	seedClient(t, "ai-test-bound", "s3cr3t", "letmoe")
	seedClient(t, "ai-test-unbound", "s3cr3t", "")
	app := buildApp()

	// A valid body carries NO site — the tenant is derived from the client
	// binding. (Huma's strict schema also 422s an extra "site" field, so the wire
	// simply has no way to smuggle a tenant.)
	const body = `{"text":"hello world"}`

	post := func(auth string) int {
		req := httptest.NewRequest("POST", "/api/v1/ai/moderate-text", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		return resp.StatusCode
	}

	if code := post(basicAuth("ai-test-bound", "s3cr3t")); code != 200 {
		t.Fatalf("correct creds → %d, want 200", code)
	}
	if code := post(basicAuth("ai-test-bound", "wrong")); code != 401 {
		t.Fatalf("wrong secret → %d, want 401", code)
	}
	if code := post(basicAuth("ai-test-nope", "s3cr3t")); code != 401 {
		t.Fatalf("unknown client → %d, want 401", code)
	}
	if code := post(""); code != 401 {
		t.Fatalf("no auth → %d, want 401", code)
	}
	if code := post(basicAuth("ai-test-unbound", "s3cr3t")); code != 403 {
		t.Fatalf("unbound catalog_site → %d, want 403", code)
	}

	// Site derivation: the ONE successful call metered the tenant from the client
	// binding (letmoe), NOT the "site" the request body tried to smuggle in.
	var rows []aiModel.AIUsage
	if err := testDB.Order("id").Find(&rows).Error; err != nil {
		t.Fatalf("load usage: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want exactly 1 metered row (only the authed call), got %d", len(rows))
	}
	if rows[0].Site != "letmoe" {
		t.Fatalf("metered site = %q, want letmoe (derived from binding, not body)", rows[0].Site)
	}
	if rows[0].Status != aiModel.StatusDegraded {
		t.Fatalf("metered status = %d, want degraded(4) (unconfigured upstream)", rows[0].Status)
	}
}
