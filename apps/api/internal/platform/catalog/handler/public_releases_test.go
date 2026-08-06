// public_releases_test.go — wave 174 wire-level coverage of the release-grain
// timeline: which release rows are in the feed at all (the month-precision
// floor, the trial/patch default exclusion, the parent-work gates), the
// is_first port discriminator, the four release-level filters, both sort
// directions with their cursors, the malformed-parameter 400s and the ETag /
// If-None-Match round trip. Integration against kun_catalog_test
// (openCatalogTestDB).
package handler

import (
	"net/http/httptest"
	"testing"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"
	"api/internal/platform/catalog/service"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// releasesApp mounts the timeline bare — the devapi chain is a separate
// concern, exactly as in calendarApp / publicApp.
func releasesApp(db *gorm.DB) *fiber.App {
	resolveSvc := service.NewResolveService(repository.NewRedirectRepository(db))
	publicSvc := service.NewPublicService(db, service.NewReadService(db), resolveSvc, "")
	h := NewPublicHandler(publicSvc, resolveSvc, nil, nil)
	app := fiber.New()
	app.Get("/v1/catalog/releases", h.Releases)
	return app
}

// releaseSeed is the plan for one seeded release row. A nil month means year
// precision (below the feed's floor); y = 0 means no date at all.
type releaseSeed struct {
	name       string
	y, m, d    int16
	kind       int16
	lang       *string
	platform   *string
	extra      string
	wantInFeed bool
}

// seedReleases wipes the works tables and plants one work per gate plus a work
// carrying a whole family of releases — the port/re-edition case the feed
// exists for. Returns the ids by label.
func seedReleases(t *testing.T, db *gorm.DB) map[string]int64 {
	t.Helper()
	for _, tbl := range []string{
		"catalog_credit", "catalog_work_character", "catalog_work_label", "catalog_external_ref",
		"catalog_work_title", "catalog_release", "catalog_work",
	} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	ids := map[string]int64{}
	mkWork := func(label, olang string, rating int16) int64 {
		w := model.CatalogWork{
			MediumID: 1, OLang: olang, DisplayName: label,
			ContentRating: rating, Status: model.WorkStatusLive,
		}
		require.NoError(t, db.Create(&w).Error)
		ids["work:"+label] = w.ID
		return w.ID
	}
	mkRelease := func(workID int64, s releaseSeed) {
		r := model.CatalogRelease{WorkID: workID, Kind: s.kind, Lang: s.lang, Platform: s.platform}
		if s.extra != "" {
			r.Extra = datatypes.JSON(s.extra)
		}
		if s.y != 0 {
			y := s.y
			r.ReleasedY = &y
			if s.m != 0 {
				m := s.m
				r.ReleasedM = &m
				if s.d != 0 {
					d := s.d
					r.ReleasedD = &d
				}
			}
		}
		require.NoError(t, db.Create(&r).Error)
		ids[s.name] = r.ID
	}
	ja, en := "ja", "en"
	win := "win"

	// The port family: one work, five releases spanning every axis under test.
	a := mkWork("Alpha", "ja", model.ContentRatingAllAges)
	mkRelease(a, releaseSeed{name: "a_original", y: 2024, m: 6, d: 14, kind: model.ReleaseKindDefault, lang: &ja, platform: &win})
	mkRelease(a, releaseSeed{name: "a_fanpatch", y: 2025, m: 3, d: 1, kind: model.ReleaseKindDigital, lang: &en, extra: `{"official":false}`})
	mkRelease(a, releaseSeed{name: "a_trial", y: 2025, m: 8, d: 1, kind: model.ReleaseKindTrial, lang: &ja})
	mkRelease(a, releaseSeed{name: "a_patch", y: 2025, m: 9, d: 1, kind: model.ReleaseKindPatch, lang: &ja})
	// Year precision and no date: both BELOW the feed's floor, and deliberately
	// also outside the is_first computation — a_original stays the first release
	// even though 2020 is earlier, because 2020 is not a position on a timeline.
	mkRelease(a, releaseSeed{name: "a_yearonly", y: 2020, kind: model.ReleaseKindDefault})
	mkRelease(a, releaseSeed{name: "a_undated", kind: model.ReleaseKindDefault})

	// The dlsite-shaped store SKU: month precision, no language of its own.
	b := mkWork("Bravo", "ja", model.ContentRatingAllAges)
	mkRelease(b, releaseSeed{name: "b_sku", y: 2024, m: 6, kind: model.ReleaseKindPhysical})

	// An English work (hidden by the curated olang default) and an r18 one
	// (hidden unless nsfw=1).
	c := mkWork("Charlie", "en", model.ContentRatingAllAges)
	mkRelease(c, releaseSeed{name: "c_en", y: 2024, m: 7, d: 4, kind: model.ReleaseKindDefault, lang: &en})
	d := mkWork("Delta", "ja", model.ContentRatingR18)
	mkRelease(d, releaseSeed{name: "d_r18", y: 2024, m: 5, d: 5, kind: model.ReleaseKindDefault, lang: &ja})
	return ids
}

// feedIDs decodes a timeline response into its release ids, in wire order.
func feedIDs(t *testing.T, body map[string]any) []int64 {
	t.Helper()
	data, ok := body["data"].(map[string]any)
	require.True(t, ok, "envelope carries data")
	items, ok := data["items"].([]any)
	require.True(t, ok, "data carries items")
	out := make([]int64, len(items))
	for i, it := range items {
		out[i] = int64(it.(map[string]any)["id"].(float64))
	}
	return out
}

// getFeed is the one-liner every case below uses.
func getFeed(t *testing.T, app *fiber.App, url string) (int, map[string]any) {
	t.Helper()
	code, _, body := getWithHeaders(t, app, url, nil)
	return code, body
}

// TestReleaseFeedDefaultPopulation pins what an unparameterized call returns:
// the trial and the patch are out, the year-only and undated rows are out, the
// en work is out (curated olang), the r18 work is out — and what is left is
// ordered newest first with the port discriminator set.
func TestReleaseFeedDefaultPopulation(t *testing.T) {
	db := openCatalogTestDB(t)
	ids := seedReleases(t, db)
	app := releasesApp(db)

	code, body := getFeed(t, app, "/v1/catalog/releases")
	require.Equal(t, 200, code)
	data := body["data"].(map[string]any)
	assert.EqualValues(t, 3, data["count"], "count is the whole filtered set")
	assert.Equal(t, []int64{ids["a_fanpatch"], ids["a_original"], ids["b_sku"]}, feedIDs(t, body),
		"date_desc: 2025-03-01, 2024-06-14, then the 2024-06 month-precision SKU")
	assert.Nil(t, data["next_cursor"], "a short page ends the walk")

	items := data["items"].([]any)
	first := items[1].(map[string]any)
	assert.EqualValues(t, ids["a_original"], first["id"])
	assert.Equal(t, "2024-06-14", first["date"])
	assert.Equal(t, "default", first["kind"])
	assert.Equal(t, true, first["is_first"], "the work's earliest DATED release")
	assert.Equal(t, []any{}, first["refs"], "release refs are always present, [] when none")
	work := first["work"].(map[string]any)
	assert.EqualValues(t, ids["work:Alpha"], work["id"])
	assert.Equal(t, "Alpha", work["display_name"])
	// The work block is a works-list row VERBATIM, which means its release_date
	// is the work-grain anchor: the earliest release carrying a YEAR, year-only
	// rows included. It is therefore legitimately EARLIER than the item's own
	// date — two grains, two questions, and this is the assertion that keeps the
	// difference deliberate rather than a bug someone "fixes" later.
	assert.Equal(t, "2020", work["release_date"], "work grain: earliest year-carrying release")
	assert.Nil(t, work["names"], "include= blocks stay absent by default")

	port := items[0].(map[string]any)
	assert.EqualValues(t, ids["a_fanpatch"], port["id"])
	assert.Equal(t, false, port["is_first"], "a later edition of an already-released work")
	assert.EqualValues(t, ids["work:Alpha"], port["work"].(map[string]any)["id"],
		"two rows of one work carry the same work block")

	// The month-precision SKU prints its month and nothing it does not know.
	sku := items[2].(map[string]any)
	assert.Equal(t, "2024-06", sku["date"])
	assert.Equal(t, "physical", sku["kind"])
	assert.Nil(t, sku["lang"], "an unrecorded release language is omitted, never guessed")
	assert.Equal(t, true, sku["is_first"])
}

// TestReleaseFeedKindGate pins the CLOSED kind vocabulary: the default excludes
// trial and patch, an explicit set reaches them, and an unknown token is a 400.
func TestReleaseFeedKindGate(t *testing.T) {
	db := openCatalogTestDB(t)
	ids := seedReleases(t, db)
	app := releasesApp(db)

	_, body := getFeed(t, app, "/v1/catalog/releases?kind=trial")
	assert.Equal(t, []int64{ids["a_trial"]}, feedIDs(t, body))
	_, body = getFeed(t, app, "/v1/catalog/releases?kind=patch")
	assert.Equal(t, []int64{ids["a_patch"]}, feedIDs(t, body))
	// The genuinely unfiltered feed has to be asked for by name.
	_, body = getFeed(t, app, "/v1/catalog/releases?kind=default,digital,physical,trial,patch")
	assert.Len(t, feedIDs(t, body), 5)
	// A duplicate token is the same request, not a wider one.
	_, body = getFeed(t, app, "/v1/catalog/releases?kind=trial,trial")
	assert.Equal(t, []int64{ids["a_trial"]}, feedIDs(t, body))
}

// TestReleaseFeedWorkGates pins the parent-work gates: olang (curated default,
// OPEN vocabulary), nsfw and the editorial display axis.
func TestReleaseFeedWorkGates(t *testing.T) {
	db := openCatalogTestDB(t)
	ids := seedReleases(t, db)
	app := releasesApp(db)

	_, body := getFeed(t, app, "/v1/catalog/releases?olang=all")
	assert.Contains(t, feedIDs(t, body), ids["c_en"], "olang=all reaches the English work")
	_, body = getFeed(t, app, "/v1/catalog/releases?olang=en")
	assert.Equal(t, []int64{ids["c_en"]}, feedIDs(t, body))
	// OPEN vocabulary: an olang nobody uses is an empty feed, never a 400.
	code, body := getFeed(t, app, "/v1/catalog/releases?olang=xx-Nope")
	require.Equal(t, 200, code)
	assert.Empty(t, feedIDs(t, body))

	_, body = getFeed(t, app, "/v1/catalog/releases?nsfw=1")
	assert.Contains(t, feedIDs(t, body), ids["d_r18"])
	_, body = getFeed(t, app, "/v1/catalog/releases")
	assert.NotContains(t, feedIDs(t, body), ids["d_r18"])

	// content_limit is the EDITORIAL axis: these works are bodyless, so it falls
	// back to the age axis — sfw keeps the all-ages ones, nsfw keeps only r18.
	_, body = getFeed(t, app, "/v1/catalog/releases?content_limit=sfw")
	assert.Equal(t, []int64{ids["a_fanpatch"], ids["a_original"], ids["b_sku"]}, feedIDs(t, body))
	_, body = getFeed(t, app, "/v1/catalog/releases?nsfw=1&content_limit=nsfw")
	assert.Equal(t, []int64{ids["d_r18"]}, feedIDs(t, body))
}

// TestReleaseFeedLangCoalesce pins the release-language gate, and specifically
// that a store SKU with NO language of its own matches its work's original
// language — half the registry's release rows are shaped that way.
func TestReleaseFeedLangCoalesce(t *testing.T) {
	db := openCatalogTestDB(t)
	ids := seedReleases(t, db)
	app := releasesApp(db)

	_, body := getFeed(t, app, "/v1/catalog/releases?lang=ja")
	assert.Equal(t, []int64{ids["a_original"], ids["b_sku"]}, feedIDs(t, body),
		"b_sku carries lang NULL and its work is olang=ja")
	_, body = getFeed(t, app, "/v1/catalog/releases?lang=en")
	assert.Equal(t, []int64{ids["a_fanpatch"]}, feedIDs(t, body))
	_, body = getFeed(t, app, "/v1/catalog/releases?lang=ja,en")
	assert.Len(t, feedIDs(t, body), 3)
	// `all` and absence are the same request; an unknown tag is empty, not 400.
	_, body = getFeed(t, app, "/v1/catalog/releases?lang=all")
	assert.Len(t, feedIDs(t, body), 3)
	code, body := getFeed(t, app, "/v1/catalog/releases?lang=xx")
	require.Equal(t, 200, code)
	assert.Empty(t, feedIDs(t, body))
}

// TestReleaseFeedOfficialGate pins the three-way official flag, including the
// ruling that a row WITHOUT the key counts as official.
func TestReleaseFeedOfficialGate(t *testing.T) {
	db := openCatalogTestDB(t)
	ids := seedReleases(t, db)
	app := releasesApp(db)

	_, body := getFeed(t, app, "/v1/catalog/releases")
	assert.Len(t, feedIDs(t, body), 3, "absent = no gate")
	_, body = getFeed(t, app, "/v1/catalog/releases?official=true")
	assert.Equal(t, []int64{ids["a_original"], ids["b_sku"]}, feedIDs(t, body),
		"the keyless rows are official; only the explicit false drops out")
	_, body = getFeed(t, app, "/v1/catalog/releases?official=false")
	assert.Equal(t, []int64{ids["a_fanpatch"]}, feedIDs(t, body))
}

// TestReleaseFeedPlatformGate pins the platform filter's open posture.
func TestReleaseFeedPlatformGate(t *testing.T) {
	db := openCatalogTestDB(t)
	ids := seedReleases(t, db)
	app := releasesApp(db)

	_, body := getFeed(t, app, "/v1/catalog/releases?platform=win")
	assert.Equal(t, []int64{ids["a_original"]}, feedIDs(t, body))
	code, body := getFeed(t, app, "/v1/catalog/releases?platform=nope")
	require.Equal(t, 200, code)
	assert.Empty(t, feedIDs(t, body))
}

// TestReleaseFeedDateWindow pins both bounds' inclusivity AND the precision
// rule a month-precision row lives under: it sits at its month's START, so a
// date_from on the 1st is already past it.
func TestReleaseFeedDateWindow(t *testing.T) {
	db := openCatalogTestDB(t)
	ids := seedReleases(t, db)
	app := releasesApp(db)

	// Both bounds inclusive on the same day.
	_, body := getFeed(t, app, "/v1/catalog/releases?date_from=2024-06-14&date_to=2024-06-14")
	assert.Equal(t, []int64{ids["a_original"]}, feedIDs(t, body))
	// The month-precision row is at 2024-06-00: the 1st excludes it...
	_, body = getFeed(t, app, "/v1/catalog/releases?date_from=2024-06-01&date_to=2024-06-30")
	assert.Equal(t, []int64{ids["a_original"]}, feedIDs(t, body))
	// ...and the last day of the previous month collects it.
	_, body = getFeed(t, app, "/v1/catalog/releases?date_from=2024-05-31&date_to=2024-06-30")
	assert.Equal(t, []int64{ids["a_original"], ids["b_sku"]}, feedIDs(t, body))
	// An open-ended lower bound still excludes everything before it.
	_, body = getFeed(t, app, "/v1/catalog/releases?date_from=2025-01-01")
	assert.Equal(t, []int64{ids["a_fanpatch"]}, feedIDs(t, body))
	_, body = getFeed(t, app, "/v1/catalog/releases?date_to=2024-06-13")
	assert.Equal(t, []int64{ids["b_sku"]}, feedIDs(t, body))
}

// TestReleaseFeedSortAndCursor walks both directions one row at a time and
// pins that a cursor never crosses between them.
func TestReleaseFeedSortAndCursor(t *testing.T) {
	db := openCatalogTestDB(t)
	ids := seedReleases(t, db)
	app := releasesApp(db)

	_, body := getFeed(t, app, "/v1/catalog/releases?sort=date_asc")
	assert.Equal(t, []int64{ids["b_sku"], ids["a_original"], ids["a_fanpatch"]}, feedIDs(t, body),
		"date_asc is the exact reverse of the default here")

	// Walk the descending lane a page at a time.
	var walked []int64
	url := "/v1/catalog/releases?limit=1"
	for range 4 {
		code, body := getFeed(t, app, url)
		require.Equal(t, 200, code)
		data := body["data"].(map[string]any)
		assert.EqualValues(t, 3, data["count"], "count is the feed, not the page")
		got := feedIDs(t, body)
		walked = append(walked, got...)
		cur, ok := data["next_cursor"].(string)
		if !ok {
			break
		}
		url = "/v1/catalog/releases?limit=1&cursor=" + cur
	}
	assert.Equal(t, []int64{ids["a_fanpatch"], ids["a_original"], ids["b_sku"]}, walked)

	// A descending cursor is refused on the ascending lane: the same position
	// means the opposite thing there.
	_, body = getFeed(t, app, "/v1/catalog/releases?limit=1")
	cur := body["data"].(map[string]any)["next_cursor"].(string)
	code, body := getFeed(t, app, "/v1/catalog/releases?sort=date_asc&limit=1&cursor="+cur)
	require.Equal(t, 400, code)
	assert.Equal(t, msgBadCursor, body["message"])
}

// TestReleaseFeedIncludeBlocks pins that include= reaches the attached WORK
// block (and that an unknown token is ignored rather than fatal).
func TestReleaseFeedIncludeBlocks(t *testing.T) {
	db := openCatalogTestDB(t)
	ids := seedReleases(t, db)
	app := releasesApp(db)
	require.NoError(t, db.Create(&model.CatalogWorkTitle{
		WorkID: ids["work:Alpha"], Lang: "ja", Title: "アルファ", Kind: model.WorkTitleKindOfficial,
	}).Error)

	code, body := getFeed(t, app, "/v1/catalog/releases?include=names,nonsense")
	require.Equal(t, 200, code)
	items := body["data"].(map[string]any)["items"].([]any)
	work := items[0].(map[string]any)["work"].(map[string]any)
	names, ok := work["names"].(map[string]any)
	require.True(t, ok, "include=names attaches the block to the work")
	assert.Equal(t, "アルファ", names["ja-jp"])
}

// TestReleaseFeedParamValidation pins the CLOSED vocabularies' 400s and the
// shared limit / cursor posture.
func TestReleaseFeedParamValidation(t *testing.T) {
	db := openCatalogTestDB(t)
	seedReleases(t, db)
	app := releasesApp(db)

	for _, tc := range []struct{ url, msg string }{
		{"/v1/catalog/releases?sort=date", msgBadReleaseSort},
		{"/v1/catalog/releases?sort=popularity", msgBadReleaseSort},
		{"/v1/catalog/releases?kind=demo", msgBadReleaseKind},
		{"/v1/catalog/releases?kind=digital,demo", msgBadReleaseKind},
		{"/v1/catalog/releases?official=maybe", msgBadOfficialFlag},
		{"/v1/catalog/releases?official=1", msgBadOfficialFlag},
		{"/v1/catalog/releases?date_from=2024-06", msgBadReleaseDate},
		{"/v1/catalog/releases?date_to=nope", msgBadReleaseDate},
		{"/v1/catalog/releases?content_limit=all", msgBadDisplayLimit},
		{"/v1/catalog/releases?limit=0", msgBadLimit},
		{"/v1/catalog/releases?limit=abc", msgBadLimit},
		{"/v1/catalog/releases?limit=-1", msgBadLimit},
		{"/v1/catalog/releases?cursor=!!!nope!!!", msgBadCursor},
	} {
		t.Run(tc.url, func(t *testing.T) {
			code, body := getFeed(t, app, tc.url)
			require.Equal(t, 400, code)
			assert.Equal(t, tc.msg, body["message"])
		})
	}

	// Legal requests, empty results included.
	for _, url := range []string{
		"/v1/catalog/releases?kind=", "/v1/catalog/releases?sort=", "/v1/catalog/releases?official=",
		"/v1/catalog/releases?limit=100", "/v1/catalog/releases?date_from=1970-01-01",
		"/v1/catalog/releases?include=names,intros,labels,ratings,covers,refs",
	} {
		t.Run("legal "+url, func(t *testing.T) {
			code, _ := getFeed(t, app, url)
			assert.Equal(t, 200, code)
		})
	}
}

// TestReleaseFeedETagRoundTrip pins the caching contract: a weak validator, a
// 304 on a match, a different validator per population, and invalidation when
// a row enters the feed.
func TestReleaseFeedETagRoundTrip(t *testing.T) {
	db := openCatalogTestDB(t)
	ids := seedReleases(t, db)
	app := releasesApp(db)

	code, h, _ := getWithHeaders(t, app, "/v1/catalog/releases", nil)
	require.Equal(t, 200, code)
	require.NotEmpty(t, h["ETag"])
	assert.Equal(t, cacheSearch, h["Cache-Control"])

	code, h2, body := getWithHeaders(t, app, "/v1/catalog/releases", map[string]string{"If-None-Match": h["ETag"]})
	assert.Equal(t, 304, code)
	assert.Equal(t, h["ETag"], h2["ETag"], "a 304 still carries the validator")
	assert.Empty(t, body, "a 304 carries no body")

	// A stale validator gets the full page back.
	code, _, body = getWithHeaders(t, app, "/v1/catalog/releases", map[string]string{"If-None-Match": `W/"relfeed-stale"`})
	assert.Equal(t, 200, code)
	assert.NotNil(t, body["data"])

	// Every membership gate rides in the key; the SORT deliberately does not
	// (reversing the order does not change the set).
	for _, url := range []string{
		"/v1/catalog/releases?nsfw=1",
		"/v1/catalog/releases?olang=all",
		"/v1/catalog/releases?content_limit=sfw",
		"/v1/catalog/releases?kind=trial",
		"/v1/catalog/releases?lang=ja",
		"/v1/catalog/releases?official=true",
		"/v1/catalog/releases?platform=win",
		"/v1/catalog/releases?date_from=2024-01-01",
		"/v1/catalog/releases?date_to=2024-12-31",
	} {
		_, other, _ := getWithHeaders(t, app, url, nil)
		assert.NotEqual(t, h["ETag"], other["ETag"], "%s must not share a validator with the ungated feed", url)
	}
	_, reversed, _ := getWithHeaders(t, app, "/v1/catalog/releases?sort=date_asc", nil)
	assert.Equal(t, h["ETag"], reversed["ETag"], "one population, one validator — sort is not a gate")

	// Dating a previously year-only release moves it INTO the feed, which must
	// bust the validator.
	require.NoError(t, db.Exec(`UPDATE catalog_release SET released_m = 4 WHERE id = ?`, ids["a_yearonly"]).Error)
	code, after, _ := getWithHeaders(t, app, "/v1/catalog/releases", nil)
	require.Equal(t, 200, code)
	assert.NotEqual(t, h["ETag"], after["ETag"])
	code, _, _ = getWithHeaders(t, app, "/v1/catalog/releases", map[string]string{"If-None-Match": h["ETag"]})
	assert.Equal(t, 200, code, "the stale validator must no longer 304")

	// ...and that newly dated 2020 row is now the work's FIRST release, so the
	// original loses the flag. is_first is a fact about the data, not the query.
	_, body = getFeed(t, app, "/v1/catalog/releases?sort=date_asc")
	items := body["data"].(map[string]any)["items"].([]any)
	assert.EqualValues(t, ids["a_yearonly"], items[0].(map[string]any)["id"])
	assert.Equal(t, true, items[0].(map[string]any)["is_first"])
	for _, it := range items[1:] {
		if int64(it.(map[string]any)["id"].(float64)) == ids["a_original"] {
			assert.Equal(t, false, it.(map[string]any)["is_first"],
				"a genuinely earlier release demotes the former first")
		}
	}
}

// TestReleaseKindVocabularyIsSymmetric pins the parser against the projection:
// every string the items print must be a string a caller may send back, and
// nothing else may be.
func TestReleaseKindVocabularyIsSymmetric(t *testing.T) {
	for _, k := range []int16{
		model.ReleaseKindDefault, model.ReleaseKindDigital, model.ReleaseKindPhysical,
		model.ReleaseKindTrial, model.ReleaseKindPatch,
	} {
		key := releaseKindKeyForTest(t, k)
		back, ok := service.ReleaseKindFromKey(key)
		require.True(t, ok, "the printed key %q must parse back", key)
		assert.Equal(t, k, back)
	}
	for _, bad := range []string{"", "demo", "Digital", "release", "0"} {
		_, ok := service.ReleaseKindFromKey(bad)
		assert.False(t, ok, "%q is outside the closed vocabulary", bad)
	}
	// The default set is the FULL-release kinds — trial and patch are excluded
	// by design, and this is the assertion that keeps them so.
	assert.Equal(t,
		[]int16{model.ReleaseKindDefault, model.ReleaseKindDigital, model.ReleaseKindPhysical},
		service.DefaultReleaseFeedKinds)
}

// releaseKindKeyForTest reads the projected key off a served item, so the test
// compares against what the WIRE prints rather than re-implementing the map.
func releaseKindKeyForTest(t *testing.T, kind int16) string {
	t.Helper()
	switch kind {
	case model.ReleaseKindDigital:
		return "digital"
	case model.ReleaseKindPhysical:
		return "physical"
	case model.ReleaseKindTrial:
		return "trial"
	case model.ReleaseKindPatch:
		return "patch"
	default:
		return "default"
	}
}

// TestReleaseFeedRouteOrder guards the registration order the live service
// uses: /releases is a static path and must not be swallowed by a sibling.
func TestReleaseFeedRouteOrder(t *testing.T) {
	db := openCatalogTestDB(t)
	seedReleases(t, db)
	resolveSvc := service.NewResolveService(repository.NewRedirectRepository(db))
	publicSvc := service.NewPublicService(db, service.NewReadService(db), resolveSvc, "")
	h := NewPublicHandler(publicSvc, resolveSvc, nil, nil)
	app := fiber.New()
	// Same order as setupPublicCatalog.
	app.Get("/v1/catalog/releases", h.Releases)
	app.Get("/v1/catalog/calendar", h.Calendar)

	resp, err := app.Test(httptest.NewRequest("GET", "/v1/catalog/releases", nil))
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, resp.StatusCode)
}
