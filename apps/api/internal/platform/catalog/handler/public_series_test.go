package handler

import (
	"testing"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"
	"api/internal/platform/catalog/service"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func seriesApp(db *gorm.DB) *fiber.App {
	resolveSvc := service.NewResolveService(repository.NewRedirectRepository(db))
	publicSvc := service.NewPublicService(db, service.NewReadService(db), resolveSvc, "")
	h := NewPublicHandler(publicSvc, resolveSvc, nil, nil)
	app := fiber.New()
	app.Get("/v1/catalog/series", h.SeriesList)
	app.Get("/v1/catalog/series/:id", h.Series)
	return app
}

func TestSeriesListSourceLane(t *testing.T) {
	db := openCatalogTestDB(t)
	seriesID, emptyID, _, _ := seedSeries(t, db)
	derived := model.CatalogSeries{DisplayName: "推論シリーズ", SourceID: derivedSourceID, ExternalID: "DRV0000001"}
	require.NoError(t, db.Create(&derived).Error)
	app := seriesApp(db)

	code, body := getJSON(t, app, "/v1/catalog/series?source=derived")
	require.Equal(t, 200, code)
	data := body["data"].(map[string]any)
	items := data["items"].([]any)
	require.Len(t, items, 1)
	assert.EqualValues(t, derived.ID, items[0].(map[string]any)["id"])
	assert.Equal(t, "derived", items[0].(map[string]any)["source"])
	assert.EqualValues(t, 1, data["total"],
		"total counts the filtered lane, not the whole catalogue")

	code, body = getJSON(t, app, "/v1/catalog/series?source=dlsite,derived")
	require.Equal(t, 200, code)
	data = body["data"].(map[string]any)
	items = data["items"].([]any)
	require.Len(t, items, 3)
	assert.EqualValues(t, seriesID, items[0].(map[string]any)["id"])
	assert.EqualValues(t, emptyID, items[1].(map[string]any)["id"])
	assert.EqualValues(t, derived.ID, items[2].(map[string]any)["id"])
	assert.EqualValues(t, 3, data["total"])

	code, body = getJSON(t, app, "/v1/catalog/series?source=nosuchsource")
	require.Equal(t, 200, code)
	data = body["data"].(map[string]any)
	assert.Empty(t, data["items"])
	assert.EqualValues(t, 0, data["total"])

	code, body = getJSON(t, app, "/v1/catalog/series")
	require.Equal(t, 200, code)
	assert.Len(t, body["data"].(map[string]any)["items"], 3)
}

const derivedSourceID int16 = 18

const dlsiteSourceID int16 = 4

func seedSeries(t *testing.T, db *gorm.DB) (seriesID, emptyID, sfwWorkID, r18WorkID int64) {
	t.Helper()
	for _, tbl := range []string{"catalog_series_intro", "catalog_series_member", "catalog_series"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	ids := seedPublicWorks(t, db, 1)
	sfwWorkID = ids[0]
	r18 := model.CatalogWork{
		MediumID: 1, OLang: "ja", DisplayName: "続編 R18",
		ContentRating: model.ContentRatingR18, Status: model.WorkStatusLive,
	}
	require.NoError(t, db.Create(&r18).Error)
	stub := model.CatalogWork{
		MediumID: 1, OLang: "ja", DisplayName: "スタブ続編",
		ContentRating: model.ContentRatingAllAges, Status: model.WorkStatusStub,
	}
	require.NoError(t, db.Create(&stub).Error)

	series := model.CatalogSeries{DisplayName: "限界シリーズ", SourceID: dlsiteSourceID, ExternalID: "SRI0000001"}
	require.NoError(t, db.Create(&series).Error)
	empty := model.CatalogSeries{DisplayName: "空シリーズ", SourceID: dlsiteSourceID, ExternalID: "SRI0000002"}
	require.NoError(t, db.Create(&empty).Error)
	for _, wid := range []int64{sfwWorkID, r18.ID, stub.ID} {
		require.NoError(t, db.Create(&model.CatalogSeriesMember{SeriesID: series.ID, WorkID: wid}).Error)
	}
	for i, wid := range []int64{sfwWorkID, r18.ID} {
		require.NoError(t, db.Exec(
			`UPDATE catalog_work SET site = 'kungal', product_work_id = ?, claim_state = ? WHERE id = ?`,
			9400+i, model.ClaimStateLive, wid).Error)
	}
	require.NoError(t, db.Create(&model.CatalogSeriesIntro{
		SeriesID: series.ID, Lang: "zh-Hans", Intro: "系列简介", SourceID: dlsiteSourceID,
	}).Error)
	require.NoError(t, db.Create(&model.CatalogSeriesIntro{
		SeriesID: series.ID, Lang: "zh-Hans", Intro: "用户补写的简介", SourceID: 1,
	}).Error)
	return series.ID, empty.ID, sfwWorkID, r18.ID
}

func TestSeriesDetailWire(t *testing.T) {
	db := openCatalogTestDB(t)
	seriesID, emptyID, _, _ := seedSeries(t, db)
	app := seriesApp(db)

	code, _ := getJSON(t, app, "/v1/catalog/series/not-a-number")
	assert.Equal(t, 400, code)

	code, _ = getJSON(t, app, "/v1/catalog/series/999999")
	assert.Equal(t, 404, code)

	code, body := getJSON(t, app, "/v1/catalog/series/"+itoa(seriesID))
	require.Equal(t, 200, code)
	data := body["data"].(map[string]any)
	assert.EqualValues(t, seriesID, data["id"])
	assert.Equal(t, "限界シリーズ", data["display_name"])
	refs := data["refs"].([]any)
	require.Len(t, refs, 1, "a series anchors in-row, so refs carries exactly its source key")
	assert.Equal(t, "dlsite", refs[0].(map[string]any)["source"])
	assert.Equal(t, "SRI0000001", refs[0].(map[string]any)["external_id"])
	intros := data["intros"].([]any)
	require.Len(t, intros, 2)
	assert.Equal(t, "zh-Hans", intros[0].(map[string]any)["lang"])
	assert.Equal(t, "user", intros[0].(map[string]any)["source"], "ordered by (lang, source_id)")
	assert.Equal(t, "dlsite", intros[1].(map[string]any)["source"])
	assert.NotContains(t, data, "works")

	code, body = getJSON(t, app, "/v1/catalog/series/"+itoa(emptyID)+"?include=works")
	require.Equal(t, 200, code)
	data = body["data"].(map[string]any)
	assert.Equal(t, []any{}, data["intros"])
	assert.NotContains(t, data, "works")
}

func TestSeriesDetailWorksAttach(t *testing.T) {
	db := openCatalogTestDB(t)
	seriesID, _, sfwWorkID, r18WorkID := seedSeries(t, db)
	app := seriesApp(db)
	base := "/v1/catalog/series/" + itoa(seriesID)

	code, body := getJSON(t, app, base+"?include=works")
	require.Equal(t, 200, code)
	works := body["data"].(map[string]any)["works"].([]any)
	require.Len(t, works, 1)
	assert.EqualValues(t, sfwWorkID, works[0].(map[string]any)["id"])
	assert.Equal(t, "galgame", works[0].(map[string]any)["medium"])
	assert.Nil(t, body["data"].(map[string]any)["next_offset"], "a short page has no next_offset")

	code, body = getJSON(t, app, base+"?include=works&nsfw=1")
	require.Equal(t, 200, code)
	works = body["data"].(map[string]any)["works"].([]any)
	require.Len(t, works, 2)
	assert.EqualValues(t, r18WorkID, works[1].(map[string]any)["id"])

	code, body = getJSON(t, app, base+"?include=works&nsfw=1&limit=1")
	require.Equal(t, 200, code)
	assert.Len(t, body["data"].(map[string]any)["works"], 1)
	assert.EqualValues(t, 1, body["data"].(map[string]any)["next_offset"])

	code, body = getJSON(t, app, base+"?include=works&nsfw=1&limit=1&offset=1")
	require.Equal(t, 200, code)
	works = body["data"].(map[string]any)["works"].([]any)
	require.Len(t, works, 1)
	assert.EqualValues(t, r18WorkID, works[0].(map[string]any)["id"])

	for _, raw := range []string{"abc", "0", "-1"} {
		code, body = getJSON(t, app, base+"?include=works&limit="+raw)
		require.Equal(t, 400, code)
		assert.Equal(t, msgBadLimit, body["message"])
	}
	code, _ = getJSON(t, app, base+"?include=works&limit=500")
	assert.Equal(t, 200, code)
}

func TestSeriesListLane(t *testing.T) {
	db := openCatalogTestDB(t)
	seriesID, emptyID, _, _ := seedSeries(t, db)
	app := seriesApp(db)

	code, body := getJSON(t, app, "/v1/catalog/series")
	require.Equal(t, 200, code)
	data := body["data"].(map[string]any)
	items := data["items"].([]any)
	require.Len(t, items, 2)

	first := items[0].(map[string]any)
	second := items[1].(map[string]any)
	assert.EqualValues(t, seriesID, first["id"])
	assert.EqualValues(t, emptyID, second["id"])
	assert.Equal(t, "限界シリーズ", first["display_name"])
	assert.Equal(t, "dlsite", first["source"], "the source KEY, never the numeric source_id")
	assert.EqualValues(t, 1, first["work_count"])
	assert.EqualValues(t, 0, second["work_count"], "a memberless series is listed with 0, not hidden")
	assert.EqualValues(t, 2, data["total"])

	code, body = getJSON(t, app, "/v1/catalog/series?nsfw=1")
	require.Equal(t, 200, code)
	items = body["data"].(map[string]any)["items"].([]any)
	assert.EqualValues(t, 2, items[0].(map[string]any)["work_count"],
		"r18 joins the count; the stub never does — it is not in the fetchable set")

	code, body = getJSON(t, app, "/v1/catalog/series?limit=1")
	require.Equal(t, 200, code)
	data = body["data"].(map[string]any)
	require.Len(t, data["items"].([]any), 1)
	cursor, ok := data["next_cursor"].(string)
	require.True(t, ok, "a full page carries a cursor")

	code, body = getJSON(t, app, "/v1/catalog/series?limit=1&cursor="+cursor)
	require.Equal(t, 200, code)
	data = body["data"].(map[string]any)
	items = data["items"].([]any)
	require.Len(t, items, 1)
	assert.EqualValues(t, emptyID, items[0].(map[string]any)["id"])
	assert.NotContains(t, data, "next_cursor", "the last page ends the walk")

	code, _ = getJSON(t, app, "/v1/catalog/series?cursor=not-a-real-cursor")
	assert.Equal(t, 400, code)

	code, _ = getJSON(t, app, "/v1/catalog/series?limit=0")
	assert.Equal(t, 400, code)
}

func setDisplayNSFW(t *testing.T, db *gorm.DB, workID int64, v bool) {
	t.Helper()
	require.NoError(t, db.Exec(`UPDATE catalog_work SET display_nsfw = ? WHERE id = ?`, v, workID).Error)
}

func TestSeriesHasNSFWReadsTheDisplayAxis(t *testing.T) {
	db := openCatalogTestDB(t)
	seriesID, _, sfwWorkID, r18WorkID := seedSeries(t, db)
	app := seriesApp(db)

	code, body := getJSON(t, app, "/v1/catalog/series/"+itoa(seriesID))
	require.Equal(t, 200, code)
	assert.Equal(t, false, body["data"].(map[string]any)["has_nsfw"],
		"an r18 game whose display material is editorially sfw must NOT raise the flag")

	setDisplayNSFW(t, db, r18WorkID, true)
	code, body = getJSON(t, app, "/v1/catalog/series/"+itoa(seriesID))
	require.Equal(t, 200, code)
	assert.Equal(t, true, body["data"].(map[string]any)["has_nsfw"])

	setDisplayNSFW(t, db, r18WorkID, false)
	setDisplayNSFW(t, db, sfwWorkID, true)
	code, body = getJSON(t, app, "/v1/catalog/series/"+itoa(seriesID))
	require.Equal(t, 200, code)
	assert.Equal(t, true, body["data"].(map[string]any)["has_nsfw"],
		"an all-ages game can carry nsfw display material")
}

func TestSeriesHasNSFWIgnoresTheCallersNSFWSetting(t *testing.T) {
	db := openCatalogTestDB(t)
	seriesID, emptyID, _, r18WorkID := seedSeries(t, db)
	setDisplayNSFW(t, db, r18WorkID, true)
	app := seriesApp(db)

	for _, q := range []string{"", "?nsfw=1"} {
		code, body := getJSON(t, app, "/v1/catalog/series"+q)
		require.Equal(t, 200, code, q)
		items := body["data"].(map[string]any)["items"].([]any)
		require.Len(t, items, 2, q)
		first, second := items[0].(map[string]any), items[1].(map[string]any)
		assert.EqualValues(t, seriesID, first["id"], q)
		assert.Equal(t, true, first["has_nsfw"], "same flag for both callers (%s)", q)
		assert.Equal(t, false, second["has_nsfw"], "a memberless series warns about nothing (%s)", q)
	}

	_, body := getJSON(t, app, "/v1/catalog/series")
	assert.EqualValues(t, 1, body["data"].(map[string]any)["items"].([]any)[0].(map[string]any)["work_count"])
	_, body = getJSON(t, app, "/v1/catalog/series?nsfw=1")
	assert.EqualValues(t, 2, body["data"].(map[string]any)["items"].([]any)[0].(map[string]any)["work_count"])

	code, body := getJSON(t, app, "/v1/catalog/series/"+itoa(seriesID))
	require.Equal(t, 200, code)
	data := body["data"].(map[string]any)
	assert.Equal(t, true, data["has_nsfw"])
	assert.NotContains(t, data, "works", "still include-gated")

	code, body = getJSON(t, app, "/v1/catalog/series/"+itoa(emptyID))
	require.Equal(t, 200, code)
	assert.Equal(t, false, body["data"].(map[string]any)["has_nsfw"])
}
