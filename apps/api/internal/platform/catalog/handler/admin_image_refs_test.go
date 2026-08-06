package handler

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/service"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// The pre-delete reference check over HTTP. What matters here is that an
// operator about to destroy bytes gets an honest answer: an unreferenced hash
// must be a plain 200 with an empty list (a 404 reads as "the check failed" and
// gets clicked through), and a referenced one must name the entities.

const refHash = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

func TestSetupAdmin_RegistersImageReferenceOperations(t *testing.T) {
	api := SetupAdmin(fiber.New(), nil, nil, nil, nil)
	paths := api.OpenAPI().Paths
	for _, p := range []string{
		"/api/v1/admin/catalog/image-references",
		"/api/v1/admin/catalog/image-references/detach",
	} {
		assert.NotNilf(t, paths[p], "operation %s must be registered", p)
	}
}

// TestValidImageHash: the guard that keeps a malformed hash from reaching six
// table scans, and keeps an uppercase or truncated hash from silently
// answering "no references" for an image that has some.
func TestValidImageHash(t *testing.T) {
	assert.True(t, validImageHash(refHash))
	assert.False(t, validImageHash(""), "empty")
	assert.False(t, validImageHash(refHash[:63]), "too short")
	assert.False(t, validImageHash(refHash+"0"), "too long")
	assert.False(t, validImageHash(strings.ToUpper(refHash)), "uppercase is not the image service's currency")
	assert.False(t, validImageHash(strings.Repeat("z", 64)), "non-hex")
}

// imageRefApp wires the admin face over a real service, the way cmd/catalog
// does (the gate is exercised separately in admin_gate_test.go).
func imageRefApp(db *gorm.DB) *fiber.App {
	app := fiber.New()
	SetupAdmin(app, nil, nil, nil, service.NewImageReferenceService(db))
	return app
}

func adminGet(t *testing.T, app *fiber.App, url string) (int, map[string]any) {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest("GET", url, nil))
	require.NoError(t, err)
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

func adminPost(t *testing.T, app *fiber.App, url, payload string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("POST", url, strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

// TestImageReferences_BadHash400: a malformed hash is rejected before any
// service call (a nil service proves nothing downstream ran).
func TestImageReferences_BadHash400(t *testing.T) {
	app := imageRefApp(nil)
	status, _ := adminGet(t, app, "/api/v1/admin/catalog/image-references?hash=nope")
	assert.Equal(t, fiber.StatusBadRequest, status)
	status, _ = adminPost(t, app, "/api/v1/admin/catalog/image-references/detach", `{"hash":"nope"}`)
	assert.Equal(t, fiber.StatusBadRequest, status)
}

// TestImageReferences_ListThenDetach walks the console's whole flow against a
// real database: ask who holds the hash, let go, ask again.
func TestImageReferences_ListThenDetach(t *testing.T) {
	db := openCatalogTestDB(t)
	for _, tbl := range []string{"catalog_work_cover", "catalog_work", "catalog_person"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	work := &model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "参照作品", Status: model.WorkStatusStub}
	require.NoError(t, db.Create(work).Error)
	require.NoError(t, db.Create(&model.CatalogWorkCover{WorkID: work.ID, ImageHash: refHash, SourceID: 1}).Error)
	require.NoError(t, db.Create(&model.CatalogPerson{DisplayName: "参照人物", PhotoHash: refHash}).Error)

	app := imageRefApp(db)

	status, body := adminGet(t, app, "/api/v1/admin/catalog/image-references?hash="+refHash)
	require.Equal(t, fiber.StatusOK, status)
	data := body["data"].(map[string]any)
	assert.Equal(t, float64(2), data["total"])
	kinds := map[string]bool{}
	for _, raw := range data["items"].([]any) {
		item := raw.(map[string]any)
		kinds[item["kind"].(string)] = true
		assert.NotEmpty(t, item["label"], "each reference names its entity")
	}
	assert.Equal(t, map[string]bool{"work_cover": true, "person_photo": true}, kinds)

	status, body = adminPost(t, app, "/api/v1/admin/catalog/image-references/detach", `{"hash":"`+refHash+`"}`)
	require.Equal(t, fiber.StatusOK, status)
	data = body["data"].(map[string]any)
	assert.Equal(t, float64(2), data["total_removed"])
	assert.Equal(t, float64(1), data["removed"].(map[string]any)["work_cover"])

	// The console's normal case: nothing references the hash → 200 + empty list.
	status, body = adminGet(t, app, "/api/v1/admin/catalog/image-references?hash="+refHash)
	require.Equal(t, fiber.StatusOK, status)
	data = body["data"].(map[string]any)
	assert.Equal(t, float64(0), data["total"])
	assert.Empty(t, data["items"])
}
