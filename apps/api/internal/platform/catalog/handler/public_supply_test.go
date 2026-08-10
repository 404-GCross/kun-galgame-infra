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

func supplyApp(db *gorm.DB) *fiber.App {
	resolveSvc := service.NewResolveService(repository.NewRedirectRepository(db))
	publicSvc := service.NewPublicService(db, service.NewReadService(db), resolveSvc, "")
	h := NewPublicHandler(publicSvc, resolveSvc, nil, nil)
	app := fiber.New()
	app.Get("/v1/catalog/works", h.WorksList)
	app.Get("/v1/catalog/works/:id", h.WorkDetail)
	app.Get("/v1/catalog/calendar", h.Calendar)
	app.Get("/v1/catalog/calendar/pending", h.CalendarPending)
	app.Get("/v1/catalog/calendar/tba", h.CalendarTBA)
	return app
}

func TestWorkDetailTagSafetyAxis(t *testing.T) {
	db := openCatalogTestDB(t)
	ensureGalgameStub(t, db)
	ensureGalgameRatingStub(t, db)
	for _, tbl := range []string{"catalog_work_tag", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	var srcBangumi, srcVNDB int16
	db.Raw("SELECT id FROM catalog_source WHERE key='bangumi'").Scan(&srcBangumi)
	require.NotZero(t, srcBangumi)
	db.Raw("SELECT id FROM catalog_source WHERE key='vndb'").Scan(&srcVNDB)
	require.NotZero(t, srcVNDB)

	claimed := model.CatalogWork{
		MediumID: 1, OLang: "ja", DisplayName: "安全軸作品", ContentRating: 0, Status: 0,
		Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(9001),
	}
	require.NoError(t, db.Create(&claimed).Error)
	for _, row := range []model.CatalogWorkTag{
		{WorkID: claimed.ID, Name: "恋愛(a2-1e)", SourceID: srcVNDB, Spoiler: 0, Sexual: false},
		{WorkID: claimed.ID, Name: "エロ(a2-1e)", SourceID: srcVNDB, Spoiler: 0, Sexual: true},
		{WorkID: claimed.ID, Name: "軽ネタバレ(a2-1e)", SourceID: srcVNDB, Spoiler: 1, Sexual: false},
		{WorkID: claimed.ID, Name: "重ネタバレ(a2-1e)", SourceID: srcVNDB, Spoiler: 2, Sexual: true},
		{WorkID: claimed.ID, Name: "百合", Count: 30, SourceID: srcBangumi},
	} {
		require.NoError(t, db.Create(&row).Error)
	}

	app := supplyApp(db)
	tagsAt := func(query string) map[string][2]any {
		t.Helper()
		code, body := getJSON(t, app, "/v1/catalog/works/"+itoa(claimed.ID)+query)
		require.Equal(t, 200, code)
		rows := body["data"].(map[string]any)["tags"].([]any)
		out := make(map[string][2]any, len(rows))
		for _, r := range rows {
			m := r.(map[string]any)
			require.Contains(t, m, "spoiler", "every tag row carries spoiler")
			require.Contains(t, m, "sexual", "every tag row carries sexual")
			out[m["name"].(string)] = [2]any{m["spoiler"], m["sexual"]}
		}
		return out
	}

	def := tagsAt("")
	assert.Len(t, def, 3, "default response carries no spoiler-flagged tag")
	assert.NotContains(t, def, "軽ネタバレ(a2-1e)")
	assert.NotContains(t, def, "重ネタバレ(a2-1e)")
	assert.EqualValues(t, 0, def["恋愛(a2-1e)"][0])
	assert.Equal(t, false, def["恋愛(a2-1e)"][1])
	assert.EqualValues(t, 0, def["エロ(a2-1e)"][0])
	assert.Equal(t, true, def["エロ(a2-1e)"][1], "sexual category surfaces independently of spoiler")
	assert.EqualValues(t, 0, def["百合"][0])
	assert.Equal(t, false, def["百合"][1])

	lvl1 := tagsAt("?spoilers=1")
	assert.Len(t, lvl1, 4)
	assert.EqualValues(t, 1, lvl1["軽ネタバレ(a2-1e)"][0])
	assert.Equal(t, false, lvl1["軽ネタバレ(a2-1e)"][1])
	assert.NotContains(t, lvl1, "重ネタバレ(a2-1e)", "level 1 must not leak a severe spoiler")

	lvl2 := tagsAt("?spoilers=2")
	assert.Len(t, lvl2, 5)
	assert.EqualValues(t, 2, lvl2["重ネタバレ(a2-1e)"][0])
	assert.Equal(t, true, lvl2["重ネタバレ(a2-1e)"][1])

	assert.Len(t, tagsAt("?spoilers=9"), 3)
}

func TestCalendarMetaNavigationFrame(t *testing.T) {
	db := openCatalogTestDB(t)
	for _, tbl := range []string{"catalog_release", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	mk := func(name, olang string, rating int16, y, m, d int16) {
		t.Helper()
		w := model.CatalogWork{
			MediumID: 1, OLang: olang, DisplayName: name,
			ContentRating: rating, Status: model.WorkStatusLive,
		}
		require.NoError(t, db.Create(&w).Error)
		r := model.CatalogRelease{WorkID: w.ID, Kind: model.ReleaseKindDefault, ReleasedY: &y}
		if m != 0 {
			r.ReleasedM = &m
		}
		if d != 0 {
			r.ReleasedD = &d
		}
		require.NoError(t, db.Create(&r).Error)
	}
	mk("メタ古", "ja", model.ContentRatingAllAges, 2020, 3, 14)
	mk("メタ新", "ja", model.ContentRatingAllAges, 2024, 8, 1)
	mk("メタR18", "ja", model.ContentRatingR18, 2026, 5, 2)

	app := supplyApp(db)
	meta := func(url string) map[string]any {
		t.Helper()
		code, body := getJSON(t, app, url)
		require.Equal(t, 200, code, url)
		return body["data"].(map[string]any)["meta"].(map[string]any)
	}

	mid := meta("/v1/catalog/calendar?month=2022-06")
	assert.Len(t, mid["today"], 10, "today is a YYYY-MM-DD JST civil date")
	assert.Equal(t, "2020-03", mid["min_month"])
	assert.Equal(t, "2024-08", mid["max_month"], "sfw caller does not see the 2026 r18 work")
	assert.Equal(t, true, mid["has_prev"])
	assert.Equal(t, true, mid["has_next"])

	first := meta("/v1/catalog/calendar?month=2020-03")
	assert.Equal(t, false, first["has_prev"])
	assert.Equal(t, true, first["has_next"])
	last := meta("/v1/catalog/calendar?month=2024-08")
	assert.Equal(t, true, last["has_prev"])
	assert.Equal(t, false, last["has_next"], "the newest non-empty month ends the walk")

	nsfw := meta("/v1/catalog/calendar?month=2024-08&nsfw=1")
	assert.Equal(t, "2026-05", nsfw["max_month"])
	assert.Equal(t, true, nsfw["has_next"])

	empty := meta("/v1/catalog/calendar?month=2022-06&olang=xx-nonexistent")
	assert.NotContains(t, empty, "min_month")
	assert.NotContains(t, empty, "max_month")
	assert.Equal(t, false, empty["has_prev"])
	assert.Equal(t, false, empty["has_next"])

	for _, url := range []string{"/v1/catalog/calendar/pending?year=2024", "/v1/catalog/calendar/tba"} {
		m := meta(url)
		assert.Len(t, m["today"], 10, url)
		for _, k := range []string{"min_month", "max_month", "has_prev", "has_next"} {
			assert.NotContains(t, m, k, "%s must not carry %s", url, k)
		}
	}
}

func TestWorksListTagIDMultiValueWire(t *testing.T) {
	db := openCatalogTestDB(t)
	app := supplyApp(db)

	for _, tc := range []struct{ url, msg string }{
		{"/v1/catalog/works?tag_id=1,2,0", "tag_id must be up to 10 comma-separated positive integers"},
		{"/v1/catalog/works?tag_id=1,abc", "tag_id must be up to 10 comma-separated positive integers"},
		{"/v1/catalog/works?tag_id=1,-2", "tag_id must be up to 10 comma-separated positive integers"},
		{"/v1/catalog/works?tag_id=1,2,3,4,5,6,7,8,9,10,11", "tag_id must be up to 10 comma-separated positive integers"},
	} {
		code, body := getJSON(t, app, tc.url)
		assert.Equal(t, 400, code, tc.url)
		assert.Equal(t, tc.msg, body["message"], tc.url)
	}

	for _, url := range []string{
		"/v1/catalog/works?tag_id=1,2,3,4,5,6,7,8,9,10",
		"/v1/catalog/works?tag_id=7",
		"/v1/catalog/works",
	} {
		code, _ := getJSON(t, app, url)
		assert.Equal(t, 200, code, url)
	}
}
