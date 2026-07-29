// public_supply_test.go — A2-1e wire-level cases (refs/proj/135): the tag
// SAFETY AXIS (R8) end to end over the claimed-work bridge, the calendar
// navigation meta (R10), and the conjunctive tag_id parameter's 400 posture.
//
// The safety axis lives here rather than in the service package because it
// reads the wiki-side galgame_tag / galgame_tag_relation layer, whose stubs
// (ensureGalgameTagStub & friends) are defined in read_test.go.
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

// supplyApp mounts the routes this file exercises (bare, like publicApp — the
// devapi chain is a separate concern).
func supplyApp(db *gorm.DB) *fiber.App {
	resolveSvc := service.NewResolveService(repository.NewRedirectRepository(db))
	publicSvc := service.NewPublicService(db, service.NewReadService(db), resolveSvc, "")
	h := NewPublicHandler(publicSvc, resolveSvc, nil)
	app := fiber.New()
	app.Get("/v1/catalog/works", h.WorksList)
	app.Get("/v1/catalog/works/:id", h.WorkDetail)
	app.Get("/v1/catalog/calendar", h.Calendar)
	app.Get("/v1/catalog/calendar/pending", h.CalendarPending)
	app.Get("/v1/catalog/calendar/tba", h.CalendarTBA)
	return app
}

// insertGalgameTagCat is insertGalgameTag with an explicit category — the
// column the public `sexual` flag projects from (content / sexual / technical).
func insertGalgameTagCat(t *testing.T, db *gorm.DB, id int64, name, category string) {
	t.Helper()
	require.NoError(t, db.Exec(
		`INSERT INTO galgame_tag (id, name, category) VALUES (?, ?, ?)`, id, name, category).Error)
}

// TestWorkDetailTagSafetyAxis is R8's case. It pins four things at once:
//
//	① the DEFAULT response carries no spoiler-flagged tag (the wire is
//	   byte-compatible with the pre-wave contract for anyone who ignores the
//	   new parameter);
//	② ?spoilers=1 / =2 widen the set monotonically;
//	③ every surfaced row carries the axis, with `sexual` true exactly for the
//	   tags the wiki classifies as the sexual category;
//	④ a catalog-native (bangumi) row — a source with NO spoiler or category
//	   concept — reads 0 / false rather than being dropped or guessed at.
func TestWorkDetailTagSafetyAxis(t *testing.T) {
	db := openCatalogTestDB(t)
	ensureGalgameStub(t, db)
	ensureGalgameCoverStub(t, db)
	ensureGalgameScreenshotStub(t, db)
	ensureGalgameRatingStub(t, db)
	ensureGalgameTagStub(t, db)
	require.NoError(t, db.Exec(`DELETE FROM galgame WHERE id IN ?`, galgameTagStubGalgameIDs).Error)
	insertGalgameBody(t, db, 9001, 0, "", "", "", "")
	for _, tbl := range []string{"catalog_work_tag", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	var srcBangumi int16
	db.Raw("SELECT id FROM catalog_source WHERE key='bangumi'").Scan(&srcBangumi)
	require.NotZero(t, srcBangumi)

	// One safe content tag, one safe SEXUAL-category tag, one minor-spoiler and
	// one severe-spoiler tag.
	insertGalgameTagCat(t, db, 9101, "恋愛(a2-1e)", "content")
	insertGalgameTagCat(t, db, 9102, "エロ(a2-1e)", "sexual")
	insertGalgameTagCat(t, db, 9103, "軽ネタバレ(a2-1e)", "content")
	insertGalgameTagCat(t, db, 9104, "重ネタバレ(a2-1e)", "sexual")

	claimed := model.CatalogWork{
		MediumID: 1, OLang: "ja", DisplayName: "安全軸作品", ContentRating: 0, Status: 0,
		Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(9001),
	}
	require.NoError(t, db.Create(&claimed).Error)
	insertGalgameTagRelation(t, db, 9001, 9101, 0, "vndb")
	insertGalgameTagRelation(t, db, 9001, 9102, 0, "vndb")
	insertGalgameTagRelation(t, db, 9001, 9103, 1, "vndb")
	insertGalgameTagRelation(t, db, 9001, 9104, 2, "vndb")
	// A catalog-native row: bangumi folksonomy, no axis upstream.
	require.NoError(t, db.Create(&model.CatalogWorkTag{
		WorkID: claimed.ID, Name: "百合", Count: 30, SourceID: srcBangumi,
	}).Error)

	app := supplyApp(db)
	// tagsAt returns name → (spoiler, sexual) for one spoilers= setting.
	tagsAt := func(query string) map[string][2]any {
		t.Helper()
		code, body := getJSON(t, app, "/v1/catalog/works/"+itoa(claimed.ID)+query)
		require.Equal(t, 200, code)
		rows := body["data"].(map[string]any)["tags"].([]any)
		out := make(map[string][2]any, len(rows))
		for _, r := range rows {
			m := r.(map[string]any)
			// Both keys must be PRESENT on every row — an omitted key would
			// make a consumer's `tag.sexual === undefined` check silently pass.
			require.Contains(t, m, "spoiler", "every tag row carries spoiler")
			require.Contains(t, m, "sexual", "every tag row carries sexual")
			out[m["name"].(string)] = [2]any{m["spoiler"], m["sexual"]}
		}
		return out
	}

	// ① default: only the two spoiler-0 wiki tags plus the native row.
	def := tagsAt("")
	assert.Len(t, def, 3, "default response carries no spoiler-flagged tag")
	assert.NotContains(t, def, "軽ネタバレ(a2-1e)")
	assert.NotContains(t, def, "重ネタバレ(a2-1e)")
	assert.EqualValues(t, 0, def["恋愛(a2-1e)"][0])
	assert.Equal(t, false, def["恋愛(a2-1e)"][1])
	// ③ sexual-category tag is flagged even though it is spoiler-free.
	assert.EqualValues(t, 0, def["エロ(a2-1e)"][0])
	assert.Equal(t, true, def["エロ(a2-1e)"][1], "sexual category surfaces independently of spoiler")
	// ④ the bangumi row has neither axis upstream → 0 / false, never dropped.
	assert.EqualValues(t, 0, def["百合"][0])
	assert.Equal(t, false, def["百合"][1])

	// ② monotonic widening.
	lvl1 := tagsAt("?spoilers=1")
	assert.Len(t, lvl1, 4)
	assert.EqualValues(t, 1, lvl1["軽ネタバレ(a2-1e)"][0])
	assert.Equal(t, false, lvl1["軽ネタバレ(a2-1e)"][1])
	assert.NotContains(t, lvl1, "重ネタバレ(a2-1e)", "level 1 must not leak a severe spoiler")

	lvl2 := tagsAt("?spoilers=2")
	assert.Len(t, lvl2, 5)
	assert.EqualValues(t, 2, lvl2["重ネタバレ(a2-1e)"][0])
	assert.Equal(t, true, lvl2["重ネタバレ(a2-1e)"][1])

	// An out-of-vocabulary spoilers value degrades to the safe default rather
	// than 400 (spoilersQuery's existing posture, shared with characters/{id}).
	assert.Len(t, tagsAt("?spoilers=9"), 3)
}

// TestCalendarMetaNavigationFrame covers R10 on the wire: `today` on all three
// buckets, month bounds + prev/next on the month bucket only, and bounds that
// move with the caller's population gates.
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

	// Month bucket in the middle of the range: both arrows live.
	mid := meta("/v1/catalog/calendar?month=2022-06")
	assert.Len(t, mid["today"], 10, "today is a YYYY-MM-DD JST civil date")
	assert.Equal(t, "2020-03", mid["min_month"])
	assert.Equal(t, "2024-08", mid["max_month"], "sfw caller does not see the 2026 r18 work")
	assert.Equal(t, true, mid["has_prev"])
	assert.Equal(t, true, mid["has_next"])

	// The two ends.
	first := meta("/v1/catalog/calendar?month=2020-03")
	assert.Equal(t, false, first["has_prev"])
	assert.Equal(t, true, first["has_next"])
	last := meta("/v1/catalog/calendar?month=2024-08")
	assert.Equal(t, true, last["has_prev"])
	assert.Equal(t, false, last["has_next"], "the newest non-empty month ends the walk")

	// The gates move the frame: nsfw reveals the 2026 work, so has_next flips.
	nsfw := meta("/v1/catalog/calendar?month=2024-08&nsfw=1")
	assert.Equal(t, "2026-05", nsfw["max_month"])
	assert.Equal(t, true, nsfw["has_next"])

	// An empty population publishes no bounds and disables both arrows.
	empty := meta("/v1/catalog/calendar?month=2022-06&olang=xx-nonexistent")
	assert.NotContains(t, empty, "min_month")
	assert.NotContains(t, empty, "max_month")
	assert.Equal(t, false, empty["has_prev"])
	assert.Equal(t, false, empty["has_next"])

	// pending / tba are not month-addressed: `today` only.
	for _, url := range []string{"/v1/catalog/calendar/pending?year=2024", "/v1/catalog/calendar/tba"} {
		m := meta(url)
		assert.Len(t, m["today"], 10, url)
		for _, k := range []string{"min_month", "max_month", "has_prev", "has_next"} {
			assert.NotContains(t, m, k, "%s must not carry %s", url, k)
		}
	}
}

// TestWorksListTagIDMultiValueWire pins the parameter's wire posture: a list is
// accepted and ANDed, a single value is unchanged, and every malformed form is
// a 400 rather than a silently dropped filter.
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

	// Exactly at the cap, and the single-value form, are both accepted.
	for _, url := range []string{
		"/v1/catalog/works?tag_id=1,2,3,4,5,6,7,8,9,10",
		"/v1/catalog/works?tag_id=7",
		"/v1/catalog/works",
	} {
		code, _ := getJSON(t, app, url)
		assert.Equal(t, 200, code, url)
	}
}
