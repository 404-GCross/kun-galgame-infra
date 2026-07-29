// public_params_test.go — wire-level parameter validation on the frozen
// /v1/catalog public face (cleanup wave): strict positive-int id filters and
// the clamp-high / 400-low limit semantics. Integration against
// kun_catalog_test (openCatalogTestDB), plus pure unit coverage of the two
// parse helpers so the arithmetic is pinned even without a database.
package handler

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"
	"api/internal/platform/catalog/service"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// publicApp mounts the public projection routes bare — the devapi chain
// (credential / rate / quota / scope) is a separate concern and would only add
// 401s to what these cases assert.
func publicApp(db *gorm.DB) *fiber.App {
	resolveSvc := service.NewResolveService(repository.NewRedirectRepository(db))
	publicSvc := service.NewPublicService(db, service.NewReadService(db), resolveSvc, "")
	h := NewPublicHandler(publicSvc, resolveSvc, nil)
	app := fiber.New()
	app.Get("/v1/catalog/works", h.WorksList)
	app.Get("/v1/catalog/changes", h.Changes)
	app.Get("/v1/catalog/labels/:id", h.Label)
	app.Get("/v1/catalog/lookup", h.Lookup)
	app.Post("/v1/catalog/lookup/batch", h.LookupBatch)
	return app
}

// postJSON posts a JSON body and decodes the envelope (the GET-side getJSON
// lives in read_test.go).
func postJSON(t *testing.T, app *fiber.App, url, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("POST", url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// seedPublicWorks truncates the works tables and inserts n bodyless LIVE
// galgame works. Returns their ids in creation order.
func seedPublicWorks(t *testing.T, db *gorm.DB, n int) []int64 {
	t.Helper()
	for _, tbl := range []string{
		"catalog_credit", "catalog_work_character", "catalog_work_label", "catalog_external_ref",
		"catalog_work_title", "catalog_release", "catalog_work",
	} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	ids := make([]int64, 0, n)
	for i := 0; i < n; i++ {
		w := model.CatalogWork{
			MediumID: 1, OLang: "ja", DisplayName: "限界作品" + itoa(int64(i)),
			ContentRating: model.ContentRatingAllAges, Status: model.WorkStatusLive,
		}
		require.NoError(t, db.Create(&w).Error)
		ids = append(ids, w.ID)
	}
	return ids
}

// TestPublicWorksListStrictIDFilters pins the cleanup-wave rule: an id filter
// that is present but illegal is a 400, never a silently-dropped filter (the
// old atoiOrPub fallback returned the UNFILTERED first page, which looks like a
// successful query with wrong data — the worst failure class).
func TestPublicWorksListStrictIDFilters(t *testing.T) {
	db := openCatalogTestDB(t)
	seedPublicWorks(t, db, 3)
	app := publicApp(db)

	cases := []struct{ name, url, msg string }{
		{"label_id non numeric", "/v1/catalog/works?label_id=abc", "label_id must be a positive integer"},
		{"label_id fractional", "/v1/catalog/works?label_id=1.5", "label_id must be a positive integer"},
		{"tag_id zero", "/v1/catalog/works?tag_id=0", "tag_id must be a positive integer"},
		{"series_id negative", "/v1/catalog/works?series_id=-5", "series_id must be a positive integer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := getJSON(t, app, tc.url)
			require.Equal(t, 400, code)
			assert.Equal(t, tc.msg, body["message"])
		})
	}

	// Absent filters stay absent (no filter) — the unfiltered page still serves.
	code, body := getJSON(t, app, "/v1/catalog/works")
	require.Equal(t, 200, code)
	assert.Len(t, body["data"].(map[string]any)["items"], 3)

	// A legal id filter is accepted (200) and actually filters — no work carries
	// label 999999, so the page is empty rather than the unfiltered 3.
	code, body = getJSON(t, app, "/v1/catalog/works?label_id=999999")
	require.Equal(t, 200, code)
	assert.Empty(t, body["data"].(map[string]any)["items"])
}

// TestPublicLimitSemantics pins limit on all three lanes: non-positive /
// non-numeric = 400, above-max = clamped DOWN to max (NOT reset to default).
func TestPublicLimitSemantics(t *testing.T) {
	db := openCatalogTestDB(t)
	seedPublicWorks(t, db, 25)
	app := publicApp(db)

	for _, url := range []string{
		"/v1/catalog/works?limit=abc", "/v1/catalog/works?limit=0", "/v1/catalog/works?limit=-1",
		"/v1/catalog/changes?limit=abc", "/v1/catalog/changes?limit=0",
		"/v1/catalog/labels/1?limit=abc", "/v1/catalog/labels/1?limit=0",
	} {
		t.Run(url, func(t *testing.T) {
			code, body := getJSON(t, app, url)
			require.Equal(t, 400, code)
			assert.Equal(t, "limit must be a positive integer", body["message"])
		})
	}

	// Clamp: 25 seeded works, default 20, max 100. limit=1000 must serve all 25
	// (clamped to 100) — a reset-to-default would have capped the page at 20.
	code, body := getJSON(t, app, "/v1/catalog/works?limit=1000")
	require.Equal(t, 200, code)
	items := body["data"].(map[string]any)["items"].([]any)
	assert.Len(t, items, 25, "over-max limit clamps to the ceiling, never falls back to the default")

	// The default is still the default when limit is absent.
	code, body = getJSON(t, app, "/v1/catalog/works")
	require.Equal(t, 200, code)
	assert.Len(t, body["data"].(map[string]any)["items"], 20)
}

// TestPublicLookupTypeVocabulary pins the closed `type` vocabulary on both
// lookup faces. A token outside work|name|character|label is a 400 — it is OUR
// vocabulary, so an unknown one is a caller mistake — whereas an unknown SOURCE
// stays a 404 miss because sources are an open registry. In the batch face a
// single illegal pair fails the whole request rather than degrading that slot to
// a silent miss.
func TestPublicLookupTypeVocabulary(t *testing.T) {
	db := openCatalogTestDB(t)
	seedPublicWorks(t, db, 1)
	app := publicApp(db)

	for _, raw := range []string{"person", "org", "release", "works", "WORK", "1"} {
		t.Run("GET type="+raw, func(t *testing.T) {
			code, body := getJSON(t, app, "/v1/catalog/lookup?source=vndb&external_id=v1&type="+raw)
			require.Equal(t, 400, code)
			assert.Equal(t, "type must be one of work, name, character, label", body["message"])
		})
	}

	// Legal tokens (and an absent one) reach the resolver: nothing is anchored,
	// so they 404 — a miss, never a 400.
	for _, url := range []string{
		"/v1/catalog/lookup?source=vndb&external_id=v1",
		"/v1/catalog/lookup?source=vndb&external_id=v1&type=work",
		"/v1/catalog/lookup?source=vndb&external_id=p1&type=label",
		"/v1/catalog/lookup?source=bangumi&external_id=1&type=character",
		"/v1/catalog/lookup?source=bangumi&external_id=1&type=name",
		"/v1/catalog/lookup?source=nosuchsource&external_id=1&type=label",
	} {
		t.Run("GET "+url, func(t *testing.T) {
			code, _ := getJSON(t, app, url)
			assert.Equal(t, 404, code)
		})
	}

	code, body := postJSON(t, app, "/v1/catalog/lookup/batch",
		`{"items":[{"source":"vndb","external_id":"v1"},{"source":"vndb","external_id":"v2","type":"release"}]}`)
	require.Equal(t, 400, code, "one illegal pair fails the whole batch")
	assert.Equal(t, "type must be one of work, name, character, label", body["message"])

	// An all-legal batch resolves normally and echoes the resolved token.
	code, body = postJSON(t, app, "/v1/catalog/lookup/batch",
		`{"items":[{"source":"vndb","external_id":"v1"},{"source":"vndb","external_id":"p1","type":"label"}]}`)
	require.Equal(t, 200, code)
	items := body["data"].(map[string]any)["items"].([]any)
	require.Len(t, items, 2)
	assert.Equal(t, "work", items[0].(map[string]any)["type"])
	assert.Equal(t, "label", items[1].(map[string]any)["type"])
}

// TestLimitPubClampArithmetic unit-pins the helper (no database needed).
func TestLimitPubClampArithmetic(t *testing.T) {
	cases := []struct {
		raw      string
		def, max int
		want     int
		ok       bool
	}{
		{"", 20, 100, 20, true},
		{"  ", 20, 100, 20, true},
		{"1", 20, 100, 1, true},
		{"100", 20, 100, 100, true},
		{"101", 20, 100, 100, true},
		{"999999", 20, 100, 100, true},
		{"501", 100, 500, 500, true},
		{"51", 50, 50, 50, true},
		{"0", 20, 100, 0, false},
		{"-1", 20, 100, 0, false},
		{"abc", 20, 100, 0, false},
		{"1.5", 20, 100, 0, false},
	}
	for _, tc := range cases {
		got, ok := limitPub(tc.raw, tc.def, tc.max)
		assert.Equal(t, tc.ok, ok, "limitPub(%q) ok", tc.raw)
		assert.Equal(t, tc.want, got, "limitPub(%q) value", tc.raw)
	}
}

// TestPosIntQueryPub unit-pins the strict id-filter parser.
func TestPosIntQueryPub(t *testing.T) {
	for _, raw := range []string{"", "   "} {
		v, ok := posIntQueryPub(raw)
		assert.True(t, ok)
		assert.EqualValues(t, 0, v, "absent = no filter")
	}
	v, ok := posIntQueryPub(" 42 ")
	assert.True(t, ok)
	assert.EqualValues(t, 42, v)
	for _, raw := range []string{"abc", "0", "-5", "1.5", "+"} {
		_, ok := posIntQueryPub(raw)
		assert.False(t, ok, "posIntQueryPub(%q) must be rejected", raw)
	}
}
