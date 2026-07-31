// public_display_limit_test.go — A2-R5 wire-level cases (refs/proj/140): the
// EDITORIAL DISPLAY axis on the frozen /v1/catalog face.
//
// Two things are pinned here that the service suite cannot see: the `content_limit=`
// vocabulary posture on all FIVE lanes that carry it (works list, works search,
// the three calendar buckets — one parser, one message), and the JSON shape of
// claimed_by, whose new key is what the sister waves 141 / 142 consume.
//
// It lives in the handler package for the reason the title suite does: it shares
// that suite's stubs (ensureGalgameStub & friends), which are defined in
// read_test.go.
package handler

import (
	"testing"

	"api/internal/platform/catalog/model"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDisplayLimitVocabularyOnEveryLane pins the wire posture: content_limit is
// OUR closed vocabulary, so a token outside {sfw, nsfw} is a LOUD 400 with ONE
// shared message on every lane that accepts it. A silently-ignored token would
// answer 200 with exactly the adult display material the caller asked to
// exclude — which is the mirror image of the incident this axis closes.
func TestDisplayLimitVocabularyOnEveryLane(t *testing.T) {
	db := openCatalogTestDB(t)
	ensureGalgameStub(t, db)
	seedPublicWorks(t, db, 3)
	app := publicApp(db)
	search := worksSearchApp(db)
	calendar := calendarApp(db)

	lanes := []struct {
		name string
		app  *fiber.App
		path string
	}{
		{"works list", app, "/v1/catalog/works?content_limit="},
		{"works search", search, "/v1/catalog/works/search?content_limit="},
		{"calendar month", calendar, "/v1/catalog/calendar?content_limit="},
		{"calendar pending", calendar, "/v1/catalog/calendar/pending?content_limit="},
		{"calendar tba", calendar, "/v1/catalog/calendar/tba?content_limit="},
	}
	// `all` is the WIKI face's third token and must NOT be quietly accepted here:
	// absence already means both, so honouring it would imply the two faces'
	// content_limit parameters are the same parameter, which they are not.
	for _, bad := range []string{"all", "SFW", "NSFW", "r18", "safe", "true", "sfw,bogus", "sfw,", ",sfw"} {
		for _, lane := range lanes {
			t.Run(lane.name+" 400 "+bad, func(t *testing.T) {
				code, body := getJSON(t, lane.app, lane.path+bad)
				require.Equal(t, 400, code)
				assert.Equal(t, msgBadDisplayLimit, body["message"])
			})
		}
	}
	// The whole vocabulary is accepted, in any combination and with whitespace.
	// (works search has no indexer wired here, so it 500s past validation — the
	// point is only that validation let it through.)
	for _, good := range []string{"sfw", "nsfw", "sfw,nsfw", "%20sfw%20,%20nsfw%20"} {
		for _, lane := range lanes {
			t.Run(lane.name+" ok "+good, func(t *testing.T) {
				code, _ := getJSON(t, lane.app, lane.path+good)
				assert.NotEqual(t, 400, code)
			})
		}
	}
}

// TestDisplayLimitAbsentIsNoGate: the three bodyless seeds still serve without
// the parameter and under both values named — absent = no gate, so every
// pre-existing caller's page is byte-identical.
func TestDisplayLimitAbsentIsNoGate(t *testing.T) {
	db := openCatalogTestDB(t)
	ensureGalgameStub(t, db)
	seedPublicWorks(t, db, 3)
	app := publicApp(db)

	code, body := getJSON(t, app, "/v1/catalog/works")
	require.Equal(t, 200, code)
	assert.Len(t, body["data"].(map[string]any)["items"], 3)

	code, body = getJSON(t, app, "/v1/catalog/works?content_limit=sfw,nsfw")
	require.Equal(t, 200, code)
	assert.Len(t, body["data"].(map[string]any)["items"], 3, "both values named = the ungated set")

	// The three seeds are all_ages bodyless rows, so they are all sfw and none of
	// them is nsfw — the age fallback for a row with no editorial flag.
	code, body = getJSON(t, app, "/v1/catalog/works?content_limit=sfw")
	require.Equal(t, 200, code)
	assert.Len(t, body["data"].(map[string]any)["items"], 3)

	code, body = getJSON(t, app, "/v1/catalog/works?content_limit=nsfw")
	require.Equal(t, 200, code)
	assert.Empty(t, body["data"].(map[string]any)["items"])
}

// TestClaimedByContentLimitOnTheWire is the shape case the sister waves consume:
// every claimed_by object carries content_limit, its value is the EDITORIAL
// display flag (not the game's rating), and an unclaimed row is still a bare null
// rather than an object.
//
// The flag is catalog_work.display_nsfw. It used to be mirrored there from the
// wiki bodies' content_limit column by step q; wave 161 retired that column with
// the rest of the galgame family, so the works below carry the two values the
// wave-140 mirror wrote for the old 'sfw' / 'nsfw' fixtures, set directly.
func TestClaimedByContentLimitOnTheWire(t *testing.T) {
	db := openCatalogTestDB(t)
	ensureGalgameStub(t, db)
	ensureGalgameCoverStub(t, db)
	ensureGalgameScreenshotStub(t, db)
	ensureGalgameRatingStub(t, db)
	for _, tbl := range []string{"catalog_work_title", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}

	// An r18 GAME whose display material an editor marked safe — the 5,568-row
	// production majority the age axis was mis-hiding.
	safeR18 := model.CatalogWork{
		MediumID: 1, OLang: "ja", DisplayName: "成人ゲーム・安全素材",
		ContentRating: model.ContentRatingR18, Status: model.WorkStatusLive,
		Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(5401), DisplayNSFW: false,
	}
	require.NoError(t, db.Create(&safeR18).Error)

	// The reverse leak: an all_ages GAME an editor marked nsfw.
	spicySFW := model.CatalogWork{
		MediumID: 1, OLang: "ja", DisplayName: "全年齢ゲーム・成人素材",
		ContentRating: model.ContentRatingAllAges, Status: model.WorkStatusLive,
		Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(5402), DisplayNSFW: true,
	}
	require.NoError(t, db.Create(&spicySFW).Error)

	bodyless := model.CatalogWork{
		MediumID: 1, OLang: "ja", DisplayName: "無認領作品",
		ContentRating: model.ContentRatingAllAges, Status: model.WorkStatusLive,
	}
	require.NoError(t, db.Create(&bodyless).Error)

	app := supplyApp(db)

	// ── the DETAIL face ──
	for _, tc := range []struct {
		id    int64
		limit string
		// rating is the OTHER axis, asserted alongside so the case proves the two
		// keys carry different answers on the same record.
		rating string
	}{
		{safeR18.ID, "sfw", "r18"},
		{spicySFW.ID, "nsfw", "all_ages"},
	} {
		code, body := getJSON(t, app, "/v1/catalog/works/"+itoa(tc.id)+"?nsfw=1")
		require.Equal(t, 200, code)
		data := body["data"].(map[string]any)
		claim, ok := data["claimed_by"].(map[string]any)
		require.True(t, ok, "claimed_by must be an object on a claimed work")
		assert.Equal(t, tc.limit, claim["content_limit"], "content_limit is the editorial display flag, not the rating")
		assert.Equal(t, tc.rating, data["content_rating"], "content_rating is the GAME's age rating")
		assert.Equal(t, "live", claim["state"], "the display axis does not disturb the visibility axis")
	}

	code, body := getJSON(t, app, "/v1/catalog/works/"+itoa(bodyless.ID))
	require.Equal(t, 200, code)
	assert.Nil(t, body["data"].(map[string]any)["claimed_by"],
		"an unclaimed row carries no claimed_by object at all — the consumer falls back to the age axis")

	// ── the LIST face (also the search face's and the calendar's item shape) ──
	code, body = getJSON(t, app, "/v1/catalog/works?nsfw=1")
	require.Equal(t, 200, code)
	items := body["data"].(map[string]any)["items"].([]any)
	require.Len(t, items, 3)
	byID := map[int64]map[string]any{}
	for _, it := range items {
		m := it.(map[string]any)
		byID[int64(m["id"].(float64))] = m
	}
	assert.Equal(t, "sfw", byID[safeR18.ID]["claimed_by"].(map[string]any)["content_limit"])
	assert.Equal(t, "nsfw", byID[spicySFW.ID]["claimed_by"].(map[string]any)["content_limit"])
	assert.Nil(t, byID[bodyless.ID]["claimed_by"])

	// ── and the gate selects by that very value, on this very page ──
	code, body = getJSON(t, app, "/v1/catalog/works?nsfw=1&content_limit=sfw")
	require.Equal(t, 200, code)
	gated := body["data"].(map[string]any)["items"].([]any)
	require.Len(t, gated, 2, "the claimed r18 work with safe material and the bodyless all_ages one")
	for _, it := range gated {
		assert.NotEqual(t, float64(spicySFW.ID), it.(map[string]any)["id"],
			"content_limit=sfw must exclude the wiki-nsfw work whatever its rating")
	}
}
