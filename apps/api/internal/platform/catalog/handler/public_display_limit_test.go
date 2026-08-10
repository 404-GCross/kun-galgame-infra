package handler

import (
	"testing"

	"api/internal/platform/catalog/model"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	for _, bad := range []string{"all", "SFW", "NSFW", "r18", "safe", "true", "sfw,bogus", "sfw,", ",sfw"} {
		for _, lane := range lanes {
			t.Run(lane.name+" 400 "+bad, func(t *testing.T) {
				code, body := getJSON(t, lane.app, lane.path+bad)
				require.Equal(t, 400, code)
				assert.Equal(t, msgBadDisplayLimit, body["message"])
			})
		}
	}
	for _, good := range []string{"sfw", "nsfw", "sfw,nsfw", "%20sfw%20,%20nsfw%20"} {
		for _, lane := range lanes {
			t.Run(lane.name+" ok "+good, func(t *testing.T) {
				code, _ := getJSON(t, lane.app, lane.path+good)
				assert.NotEqual(t, 400, code)
			})
		}
	}
}

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

	code, body = getJSON(t, app, "/v1/catalog/works?content_limit=sfw")
	require.Equal(t, 200, code)
	assert.Len(t, body["data"].(map[string]any)["items"], 3)

	code, body = getJSON(t, app, "/v1/catalog/works?content_limit=nsfw")
	require.Equal(t, 200, code)
	assert.Empty(t, body["data"].(map[string]any)["items"])
}

func TestClaimedByContentLimitOnTheWire(t *testing.T) {
	db := openCatalogTestDB(t)
	ensureGalgameStub(t, db)
	ensureGalgameRatingStub(t, db)
	for _, tbl := range []string{"catalog_work_title", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}

	safeR18 := model.CatalogWork{
		MediumID: 1, OLang: "ja", DisplayName: "成人ゲーム・安全素材",
		ContentRating: model.ContentRatingR18, Status: model.WorkStatusLive,
		Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(5401), DisplayNSFW: false,
	}
	require.NoError(t, db.Create(&safeR18).Error)

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

	for _, tc := range []struct {
		id     int64
		limit  string
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

	code, body = getJSON(t, app, "/v1/catalog/works?nsfw=1&content_limit=sfw")
	require.Equal(t, 200, code)
	gated := body["data"].(map[string]any)["items"].([]any)
	require.Len(t, gated, 2, "the claimed r18 work with safe material and the bodyless all_ages one")
	for _, it := range gated {
		assert.NotEqual(t, float64(spicySFW.ID), it.(map[string]any)["id"],
			"content_limit=sfw must exclude the wiki-nsfw work whatever its rating")
	}
}
