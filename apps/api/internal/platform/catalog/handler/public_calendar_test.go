// public_calendar_test.go — A2-1c wire-level coverage of the three calendar
// buckets: the ETag / If-None-Match 304 round trip and its invalidation, the
// JST-anchored month/year defaults, the malformed-parameter 400s, the olang
// gate on the wire, and the envelope echo. Integration against kun_catalog_test
// (openCatalogTestDB).
package handler

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"
	"api/internal/platform/catalog/service"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// calendarApp mounts the three buckets bare — the devapi chain is a separate
// concern, exactly as in publicApp / taxonomyApp.
func calendarApp(db *gorm.DB) *fiber.App {
	resolveSvc := service.NewResolveService(repository.NewRedirectRepository(db))
	publicSvc := service.NewPublicService(db, service.NewReadService(db), resolveSvc, "")
	h := NewPublicHandler(publicSvc, resolveSvc, nil, nil)
	app := fiber.New()
	app.Get("/v1/catalog/calendar", h.Calendar)
	app.Get("/v1/catalog/calendar/pending", h.CalendarPending)
	app.Get("/v1/catalog/calendar/tba", h.CalendarTBA)
	return app
}

// getWithHeaders issues a GET with optional request headers and returns the
// status, the response headers and the decoded envelope (empty on a 304).
func getWithHeaders(t *testing.T, app *fiber.App, url string, headers map[string]string) (int, map[string]string, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("GET", url, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := app.Test(req)
	require.NoError(t, err)
	out := map[string]string{"ETag": resp.Header.Get("ETag"), "Cache-Control": resp.Header.Get("Cache-Control")}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, out, body
}

// seedCalendar wipes the works tables and plants one work per bucket:
// 2024-06-14 (day), 2024-06 (month), 2024 (year → pending), undated (tba), and
// an en-language June work the default olang gate must hide.
func seedCalendar(t *testing.T, db *gorm.DB) (day, month, year, tba, en int64) {
	t.Helper()
	for _, tbl := range []string{
		"catalog_credit", "catalog_work_character", "catalog_work_label", "catalog_external_ref",
		"catalog_work_title", "catalog_release", "catalog_work",
	} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	mk := func(name, olang string, y, m, d int16, dated bool) int64 {
		w := model.CatalogWork{
			MediumID: 1, OLang: olang, DisplayName: name,
			ContentRating: model.ContentRatingAllAges, Status: model.WorkStatusLive,
		}
		require.NoError(t, db.Create(&w).Error)
		r := model.CatalogRelease{WorkID: w.ID, Kind: model.ReleaseKindDefault}
		if dated {
			r.ReleasedY = &y
			if m != 0 {
				r.ReleasedM = &m
			}
			if d != 0 {
				r.ReleasedD = &d
			}
		}
		require.NoError(t, db.Create(&r).Error)
		return w.ID
	}
	day = mk("CalDay", "ja", 2024, 6, 14, true)
	month = mk("CalMonth", "ja", 2024, 6, 0, true)
	year = mk("CalYear", "ja", 2024, 0, 0, true)
	tba = mk("CalTBA", "ja", 0, 0, 0, false)
	en = mk("CalEn", "en", 2024, 6, 20, true)
	return day, month, year, tba, en
}

// TestCalendarBucketsWire pins the three envelopes end to end: which work lands
// in which bucket, the month/year echo, the bucket count, and the default olang
// gate hiding the en-language work.
func TestCalendarBucketsWire(t *testing.T) {
	db := openCatalogTestDB(t)
	day, month, year, tba, en := seedCalendar(t, db)
	app := calendarApp(db)

	code, _, body := getWithHeaders(t, app, "/v1/catalog/calendar?month=2024-06", nil)
	require.Equal(t, 200, code)
	data := body["data"].(map[string]any)
	assert.Equal(t, "2024-06", data["month"], "the served window is echoed back")
	assert.Nil(t, data["year"], "the year key belongs to the pending bucket only")
	assert.EqualValues(t, 2, data["count"])
	items := data["items"].([]any)
	require.Len(t, items, 2)
	// Month precision (ordinal 20240600) sorts ahead of day precision the 14th.
	assert.EqualValues(t, month, items[0].(map[string]any)["id"])
	assert.Equal(t, "2024-06", items[0].(map[string]any)["release_date"])
	assert.EqualValues(t, day, items[1].(map[string]any)["id"])
	assert.Equal(t, "2024-06-14", items[1].(map[string]any)["release_date"])
	assert.Nil(t, data["next_cursor"])

	code, _, body = getWithHeaders(t, app, "/v1/catalog/calendar/pending?year=2024", nil)
	require.Equal(t, 200, code)
	data = body["data"].(map[string]any)
	assert.Equal(t, "2024", data["year"])
	assert.Nil(t, data["month"])
	items = data["items"].([]any)
	require.Len(t, items, 1)
	assert.EqualValues(t, year, items[0].(map[string]any)["id"])
	assert.Equal(t, "2024", items[0].(map[string]any)["release_date"])

	code, _, body = getWithHeaders(t, app, "/v1/catalog/calendar/tba", nil)
	require.Equal(t, 200, code)
	data = body["data"].(map[string]any)
	assert.Nil(t, data["month"])
	assert.Nil(t, data["year"])
	items = data["items"].([]any)
	require.Len(t, items, 1)
	assert.EqualValues(t, tba, items[0].(map[string]any)["id"])
	assert.Nil(t, items[0].(map[string]any)["release_date"], "a TBA work has no date to print")

	// olang: default hides the en work, all reveals it, an explicit set selects it.
	code, _, body = getWithHeaders(t, app, "/v1/catalog/calendar?month=2024-06&olang=all", nil)
	require.Equal(t, 200, code)
	assert.Len(t, body["data"].(map[string]any)["items"], 3)
	code, _, body = getWithHeaders(t, app, "/v1/catalog/calendar?month=2024-06&olang=en", nil)
	require.Equal(t, 200, code)
	items = body["data"].(map[string]any)["items"].([]any)
	require.Len(t, items, 1)
	assert.EqualValues(t, en, items[0].(map[string]any)["id"])
	// An olang nobody uses is an empty bucket, never a 400 — the vocabulary is
	// open, unlike content_rating / kind / tier.
	code, _, body = getWithHeaders(t, app, "/v1/catalog/calendar?month=2024-06&olang=xx-Nope", nil)
	require.Equal(t, 200, code)
	assert.Empty(t, body["data"].(map[string]any)["items"])
}

// TestCalendarETagRoundTrip pins the wave's caching contract: every bucket
// serves a weak validator, a matching If-None-Match 304s, and a change inside
// the bucket invalidates it.
func TestCalendarETagRoundTrip(t *testing.T) {
	db := openCatalogTestDB(t)
	day, _, _, _, _ := seedCalendar(t, db)
	app := calendarApp(db)

	for _, url := range []string{
		"/v1/catalog/calendar?month=2024-06",
		"/v1/catalog/calendar/pending?year=2024",
		"/v1/catalog/calendar/tba",
	} {
		t.Run(url, func(t *testing.T) {
			code, h, _ := getWithHeaders(t, app, url, nil)
			require.Equal(t, 200, code)
			require.NotEmpty(t, h["ETag"])
			assert.Equal(t, cacheSearch, h["Cache-Control"])

			code, h2, body := getWithHeaders(t, app, url, map[string]string{"If-None-Match": h["ETag"]})
			assert.Equal(t, 304, code)
			assert.Equal(t, h["ETag"], h2["ETag"], "a 304 still carries the validator")
			assert.Empty(t, body, "a 304 carries no body")

			// A stale validator gets the full page back.
			code, _, body = getWithHeaders(t, app, url, map[string]string{"If-None-Match": `W/"cal-stale"`})
			assert.Equal(t, 200, code)
			assert.NotNil(t, body["data"])
		})
	}

	// Population gates ride in the validator: sfw and nsfw are different sets,
	// and so are two different olang gates.
	_, base, _ := getWithHeaders(t, app, "/v1/catalog/calendar?month=2024-06", nil)
	_, nsfw, _ := getWithHeaders(t, app, "/v1/catalog/calendar?month=2024-06&nsfw=1", nil)
	_, all, _ := getWithHeaders(t, app, "/v1/catalog/calendar?month=2024-06&olang=all", nil)
	assert.NotEqual(t, base["ETag"], nsfw["ETag"])
	assert.NotEqual(t, base["ETag"], all["ETag"])

	// Two different months never share a validator.
	_, other, _ := getWithHeaders(t, app, "/v1/catalog/calendar?month=2024-07", nil)
	assert.NotEqual(t, base["ETag"], other["ETag"])

	// A member work changing invalidates the bucket.
	require.NoError(t, db.Exec(`UPDATE catalog_work SET updated_at = now() + interval '2 hours' WHERE id = ?`, day).Error)
	_, after, _ := getWithHeaders(t, app, "/v1/catalog/calendar?month=2024-06", nil)
	assert.NotEqual(t, base["ETag"], after["ETag"], "a touched member work must bust the calendar validator")
	code, _, _ := getWithHeaders(t, app, "/v1/catalog/calendar?month=2024-06", map[string]string{"If-None-Match": base["ETag"]})
	assert.Equal(t, 200, code, "the stale validator must no longer 304")
}

// TestCalendarParamValidation pins the malformed-parameter 400s and the shared
// limit / cursor posture.
func TestCalendarParamValidation(t *testing.T) {
	db := openCatalogTestDB(t)
	seedCalendar(t, db)
	app := calendarApp(db)

	for _, tc := range []struct{ url, msg string }{
		{"/v1/catalog/calendar?month=2024", msgBadCalendarMonth},
		{"/v1/catalog/calendar?month=2024-13", msgBadCalendarMonth},
		{"/v1/catalog/calendar?month=2024-00", msgBadCalendarMonth},
		{"/v1/catalog/calendar?month=2024-6", msgBadCalendarMonth},
		{"/v1/catalog/calendar?month=june", msgBadCalendarMonth},
		{"/v1/catalog/calendar?month=2024-06-14", msgBadCalendarMonth},
		{"/v1/catalog/calendar/pending?year=24", msgBadCalendarYear},
		{"/v1/catalog/calendar/pending?year=2024-06", msgBadCalendarYear},
		{"/v1/catalog/calendar/pending?year=abcd", msgBadCalendarYear},
		{"/v1/catalog/calendar?limit=0", msgBadLimit},
		{"/v1/catalog/calendar?limit=abc", msgBadLimit},
		{"/v1/catalog/calendar/tba?limit=-1", msgBadLimit},
		{"/v1/catalog/calendar?cursor=!!!nope!!!", msgBadCursor},
		{"/v1/catalog/calendar/pending?cursor=!!!nope!!!", msgBadCursor},
		{"/v1/catalog/calendar/tba?cursor=!!!nope!!!", msgBadCursor},
	} {
		t.Run(tc.url, func(t *testing.T) {
			code, _, body := getWithHeaders(t, app, tc.url, nil)
			require.Equal(t, 400, code)
			assert.Equal(t, tc.msg, body["message"])
		})
	}

	// Legal windows serve, empty ones included.
	for _, url := range []string{
		"/v1/catalog/calendar?month=2024-01", "/v1/catalog/calendar?month=2024-12",
		"/v1/catalog/calendar?month=1970-01", "/v1/catalog/calendar/pending?year=1970",
		"/v1/catalog/calendar?limit=100", "/v1/catalog/calendar?include=names,nonsense",
	} {
		t.Run("legal "+url, func(t *testing.T) {
			code, _, _ := getWithHeaders(t, app, url, nil)
			assert.Equal(t, 200, code)
		})
	}
}

// TestCalendarKeysetWire walks the month bucket one row at a time over the wire.
func TestCalendarKeysetWire(t *testing.T) {
	db := openCatalogTestDB(t)
	_, month, _, _, _ := seedCalendar(t, db)
	app := calendarApp(db)

	code, _, body := getWithHeaders(t, app, "/v1/catalog/calendar?month=2024-06&limit=1", nil)
	require.Equal(t, 200, code)
	data := body["data"].(map[string]any)
	require.Len(t, data["items"], 1)
	assert.EqualValues(t, month, data["items"].([]any)[0].(map[string]any)["id"])
	assert.EqualValues(t, 2, data["count"], "count is the bucket, not the page")
	cursor, ok := data["next_cursor"].(string)
	require.True(t, ok, "a full page must mint a cursor")

	code, _, body = getWithHeaders(t, app, "/v1/catalog/calendar?month=2024-06&limit=1&cursor="+cursor, nil)
	require.Equal(t, 200, code)
	data = body["data"].(map[string]any)
	require.Len(t, data["items"], 1)
	cursor2 := data["next_cursor"].(string)

	code, _, body = getWithHeaders(t, app, "/v1/catalog/calendar?month=2024-06&limit=1&cursor="+cursor2, nil)
	require.Equal(t, 200, code)
	data = body["data"].(map[string]any)
	assert.Empty(t, data["items"])
	assert.Nil(t, data["next_cursor"], "a short page ends the walk")

	// A month cursor is refused on the pending / tba lanes.
	for _, url := range []string{
		"/v1/catalog/calendar/pending?cursor=" + cursor,
		"/v1/catalog/calendar/tba?cursor=" + cursor,
	} {
		code, _, body := getWithHeaders(t, app, url, nil)
		require.Equal(t, 400, code)
		assert.Equal(t, msgBadCursor, body["message"])
	}
}

// TestCalendarDefaultsAreJST pins the timezone rule WITHOUT pinning a real
// "today": the defaults are pure functions of an instant, and the instants
// chosen here straddle a JST boundary that UTC has not crossed yet.
func TestCalendarDefaultsAreJST(t *testing.T) {
	cases := []struct {
		utc         string
		month, year string
	}{
		// 15:30 UTC on the last day of June is already 00:30 on July 1st in JST.
		{"2026-06-30T15:30:00Z", "2026-07", "2026"},
		// ...and the last day of December rolls the YEAR over too.
		{"2026-12-31T15:00:00Z", "2027-01", "2027"},
		// Just before the boundary both are still the earlier window.
		{"2026-06-30T14:59:59Z", "2026-06", "2026"},
		{"2026-12-31T14:59:59Z", "2026-12", "2026"},
		// A non-UTC input resolves by instant, not by its own wall clock.
		{"2026-07-01T00:30:00+09:00", "2026-07", "2026"},
	}
	for _, tc := range cases {
		at, err := time.Parse(time.RFC3339, tc.utc)
		require.NoError(t, err)
		assert.Equal(t, tc.month, defaultCalendarMonth(at), "month default at %s", tc.utc)
		assert.Equal(t, tc.year, defaultCalendarYear(at), "year default at %s", tc.utc)
	}

	// The live handler uses the same helpers, so an omitted parameter echoes the
	// JST window rather than the UTC one.
	db := openCatalogTestDB(t)
	seedCalendar(t, db)
	app := calendarApp(db)
	code, _, body := getWithHeaders(t, app, "/v1/catalog/calendar", nil)
	require.Equal(t, 200, code)
	assert.Equal(t, defaultCalendarMonth(time.Now()), body["data"].(map[string]any)["month"])
	code, _, body = getWithHeaders(t, app, "/v1/catalog/calendar/pending", nil)
	require.Equal(t, 200, code)
	assert.Equal(t, defaultCalendarYear(time.Now()), body["data"].(map[string]any)["year"])
}

// TestParsePublicOLang unit-pins the gate parser (no database needed).
func TestParsePublicOLang(t *testing.T) {
	assert.Equal(t, service.PublicOLang{}, parsePublicOLang(""))
	assert.Equal(t, service.PublicOLang{}, parsePublicOLang("   "))
	assert.Equal(t, service.PublicOLang{All: true}, parsePublicOLang("all"))
	assert.Equal(t, service.PublicOLang{Values: []string{"ja"}}, parsePublicOLang("ja"))
	assert.Equal(t, service.PublicOLang{Values: []string{"ja", "zh-Hans"}}, parsePublicOLang(" ja , zh-Hans "))
	// An all-blank list degrades to the default rather than to an impossible IN ().
	assert.Equal(t, service.PublicOLang{}, parsePublicOLang(" , , "))

	assert.Equal(t, "jazh", service.PublicOLang{}.Key())
	assert.Equal(t, "all", service.PublicOLang{All: true}.Key())
	assert.Equal(t, "ja+en", service.PublicOLang{Values: []string{"ja", "en"}}.Key())
	// The population key carries one segment per gate that decides membership.
	// A2-R5 appended the editorial display axis, `all` when it is ungated — two
	// different gates must never share a cache validator.
	assert.Equal(t, "sfw-jazh-all", service.CalendarFilter{}.PopulationKey())
	assert.Equal(t, "nsfw-all-all", service.CalendarFilter{NSFW: true, OLang: service.PublicOLang{All: true}}.PopulationKey())
	assert.Equal(t, "sfw-jazh-sfw",
		service.CalendarFilter{DisplayLimits: []string{"sfw"}}.PopulationKey())
	assert.Equal(t, "sfw-jazh-sfw+nsfw",
		service.CalendarFilter{DisplayLimits: []string{"sfw", "nsfw"}}.PopulationKey())
}
