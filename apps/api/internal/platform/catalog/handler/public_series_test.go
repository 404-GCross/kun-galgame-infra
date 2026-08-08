// public_series_test.go — wire-level coverage of the series read face
// (GET /v1/catalog/series/{id}, 149c): the 400/404 split, the always-present
// head blocks (refs + intros), and the include=works member lane inheriting the
// tags/{id} paging + nsfw posture verbatim.
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

// seriesApp mounts the series detail lane bare, as taxonomyApp does for its own.
func seriesApp(db *gorm.DB) *fiber.App {
	resolveSvc := service.NewResolveService(repository.NewRedirectRepository(db))
	publicSvc := service.NewPublicService(db, service.NewReadService(db), resolveSvc, "")
	h := NewPublicHandler(publicSvc, resolveSvc, nil, nil)
	app := fiber.New()
	// Both routes, in the order cmd/catalog/main.go registers them: the browse
	// lane before its own /:id. TestSeriesListLane calls GET /v1/catalog/series
	// and this helper only carried the detail route, so the lane 404'd against
	// a router that production does not have — the endpoint itself was fine.
	app.Get("/v1/catalog/series", h.SeriesList)
	app.Get("/v1/catalog/series/:id", h.Series)
	return app
}

// TestSeriesListSourceLane pins source=: the browse lane can be asked for one
// provenance lane at a time, on the same key the rows print. Without it a
// consumer cannot tell a hand-filed series from a machine-inferred one except
// by fetching every row and sorting client-side.
func TestSeriesListSourceLane(t *testing.T) {
	db := openCatalogTestDB(t)
	seriesID, emptyID, _, _ := seedSeries(t, db)
	// A third series on the MACHINE INFERENCE lane, so the filter has two
	// populations to tell apart rather than one to echo back.
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

	// Comma-separated union, still in id ASC.
	code, body = getJSON(t, app, "/v1/catalog/series?source=dlsite,derived")
	require.Equal(t, 200, code)
	data = body["data"].(map[string]any)
	items = data["items"].([]any)
	require.Len(t, items, 3)
	assert.EqualValues(t, seriesID, items[0].(map[string]any)["id"])
	assert.EqualValues(t, emptyID, items[1].(map[string]any)["id"])
	assert.EqualValues(t, derived.ID, items[2].(map[string]any)["id"])
	assert.EqualValues(t, 3, data["total"])

	// OPEN vocabulary: an unknown lane is an empty page, never a 400 — which
	// sources file series is registry data, not a code-level enum.
	code, body = getJSON(t, app, "/v1/catalog/series?source=nosuchsource")
	require.Equal(t, 200, code)
	data = body["data"].(map[string]any)
	assert.Empty(t, data["items"])
	assert.EqualValues(t, 0, data["total"])

	// Absent = no gate, byte-identical to a pre-filter caller.
	code, body = getJSON(t, app, "/v1/catalog/series")
	require.Equal(t, 200, code)
	assert.Len(t, body["data"].(map[string]any)["items"], 3)
}

// derivedSourceID is the machine-inference lane (wave 184 series builder).
const derivedSourceID int16 = 18

// dlsiteSourceID is the source the series lane is fed from today (the step-94
// importer is dlsite-only); its public key is "dlsite".
const dlsiteSourceID int16 = 4

// seedSeries wipes the series tables and builds one series with two zh-Hans
// intros (two sources), three member works — one all-ages, one r18, one stub
// (never a member of the fetchable set) — plus a second, empty series.
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
	// work_count counts LIVE CLAIMS only (wave 146) — the same gate the sibling
	// labels/tags/engines fixtures satisfy, and for the same reason: a bodyless
	// work is not on the public face, so a lane counting it would advertise a
	// number the member list cannot deliver. Left unclaimed, every count here is
	// legitimately 0, which is what this fixture used to assert against.
	// The stub stays unclaimed: it is excluded by status anyway, and leaving it
	// out keeps it a stub rather than quietly making it countable.
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

// TestSeriesDetailWire pins the 400 / 404 split and the head blocks that are
// always present — refs (the in-row source anchor) and intros.
func TestSeriesDetailWire(t *testing.T) {
	db := openCatalogTestDB(t)
	seriesID, emptyID, _, _ := seedSeries(t, db)
	app := seriesApp(db)

	code, _ := getJSON(t, app, "/v1/catalog/series/not-a-number")
	assert.Equal(t, 400, code)

	// A series has no merge machinery, so an unknown id is a plain 404 — never
	// a redirect.
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
	// Both rows of the same language survive: a rescued hand-written body is
	// content that exists nowhere else, not a worse copy of another source's.
	intros := data["intros"].([]any)
	require.Len(t, intros, 2)
	assert.Equal(t, "zh-Hans", intros[0].(map[string]any)["lang"])
	assert.Equal(t, "user", intros[0].(map[string]any)["source"], "ordered by (lang, source_id)")
	assert.Equal(t, "dlsite", intros[1].(map[string]any)["source"])
	// Members are include-gated: absent without include=works.
	assert.NotContains(t, data, "works")

	// A series with no members and no intros still renders: intros is [] (never
	// null), and an empty works block is omitted entirely — the tags/{id}
	// convention verbatim (its Works field is omitempty too).
	code, body = getJSON(t, app, "/v1/catalog/series/"+itoa(emptyID)+"?include=works")
	require.Equal(t, 200, code)
	data = body["data"].(map[string]any)
	assert.Equal(t, []any{}, data["intros"])
	assert.NotContains(t, data, "works")
}

// TestSeriesDetailWorksAttach pins the member lane: the LIVE galgame fetchable
// set only, the nsfw switch travelling from the query string into the briefs,
// and the tags/{id} paging semantics (clamp-high limit, 400 on an illegal one,
// next_offset only on a full page).
func TestSeriesDetailWorksAttach(t *testing.T) {
	db := openCatalogTestDB(t)
	seriesID, _, sfwWorkID, r18WorkID := seedSeries(t, db)
	app := seriesApp(db)
	base := "/v1/catalog/series/" + itoa(seriesID)

	// sfw caller: the r18 member is dropped, the stub member was never in the
	// fetchable set → one work.
	code, body := getJSON(t, app, base+"?include=works")
	require.Equal(t, 200, code)
	works := body["data"].(map[string]any)["works"].([]any)
	require.Len(t, works, 1)
	assert.EqualValues(t, sfwWorkID, works[0].(map[string]any)["id"])
	assert.Equal(t, "galgame", works[0].(map[string]any)["medium"])
	assert.Nil(t, body["data"].(map[string]any)["next_offset"], "a short page has no next_offset")

	// nsfw=1 opts into the r18 member.
	code, body = getJSON(t, app, base+"?include=works&nsfw=1")
	require.Equal(t, 200, code)
	works = body["data"].(map[string]any)["works"].([]any)
	require.Len(t, works, 2)
	assert.EqualValues(t, r18WorkID, works[1].(map[string]any)["id"])

	// A full page advertises the next offset (limit=1 over two nsfw members).
	code, body = getJSON(t, app, base+"?include=works&nsfw=1&limit=1")
	require.Equal(t, 200, code)
	assert.Len(t, body["data"].(map[string]any)["works"], 1)
	assert.EqualValues(t, 1, body["data"].(map[string]any)["next_offset"])

	// offset pages forward.
	code, body = getJSON(t, app, base+"?include=works&nsfw=1&limit=1&offset=1")
	require.Equal(t, 200, code)
	works = body["data"].(map[string]any)["works"].([]any)
	require.Len(t, works, 1)
	assert.EqualValues(t, r18WorkID, works[0].(map[string]any)["id"])

	// The shared limit posture: illegal is a 400, above the cap is clamped.
	for _, raw := range []string{"abc", "0", "-1"} {
		code, body = getJSON(t, app, base+"?include=works&limit="+raw)
		require.Equal(t, 400, code)
		assert.Equal(t, msgBadLimit, body["message"])
	}
	code, _ = getJSON(t, app, base+"?include=works&limit=500")
	assert.Equal(t, 200, code)
}

// TestSeriesListLane pins the browse lane against its three siblings: rows in id
// order, an nsfw-aware work_count that equals what works?series_id= would hand
// back to the SAME caller, and a series with no members rendering 0 rather than
// being hidden.
//
// The count is the whole reason the lane is not just a name dump: a picker that
// says "限界シリーズ (3)" while the link behind it lists 1 work is worse than one
// that says nothing.
func TestSeriesListLane(t *testing.T) {
	db := openCatalogTestDB(t)
	seriesID, emptyID, _, _ := seedSeries(t, db)
	app := seriesApp(db)

	code, body := getJSON(t, app, "/v1/catalog/series")
	require.Equal(t, 200, code)
	data := body["data"].(map[string]any)
	items := data["items"].([]any)
	require.Len(t, items, 2)

	// id ASC — the lane's ordering contract, and what the cursor walks.
	first := items[0].(map[string]any)
	second := items[1].(map[string]any)
	assert.EqualValues(t, seriesID, first["id"])
	assert.EqualValues(t, emptyID, second["id"])
	assert.Equal(t, "限界シリーズ", first["display_name"])
	assert.Equal(t, "dlsite", first["source"], "the source KEY, never the numeric source_id")
	// Three members: one live all-ages, one live r18, one stub. An sfw caller
	// reaches exactly the first.
	assert.EqualValues(t, 1, first["work_count"])
	assert.EqualValues(t, 0, second["work_count"], "a memberless series is listed with 0, not hidden")
	assert.EqualValues(t, 2, data["total"])

	code, body = getJSON(t, app, "/v1/catalog/series?nsfw=1")
	require.Equal(t, 200, code)
	items = body["data"].(map[string]any)["items"].([]any)
	assert.EqualValues(t, 2, items[0].(map[string]any)["work_count"],
		"r18 joins the count; the stub never does — it is not in the fetchable set")

	// The keyset walk: one row per page, and the last page ends it.
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

	// Lane-pinned cursors are why the four lanes can share one encoder: a cursor
	// from another facet replayed here would silently page the wrong thing.
	code, _ = getJSON(t, app, "/v1/catalog/series?cursor=not-a-real-cursor")
	assert.Equal(t, 400, code)

	// A non-positive limit is a 400, not a silent default — the shared posture.
	code, _ = getJSON(t, app, "/v1/catalog/series?limit=0")
	assert.Equal(t, 400, code)
}

// setDisplayNSFW flips the EDITORIAL display flag on a work — the axis
// has_nsfw reads, which is not the age rating.
func setDisplayNSFW(t *testing.T, db *gorm.DB, workID int64, v bool) {
	t.Helper()
	require.NoError(t, db.Exec(`UPDATE catalog_work SET display_nsfw = ? WHERE id = ?`, v, workID).Error)
}

// TestSeriesHasNSFWReadsTheDisplayAxis is the whole point of the field: "nsfw"
// on this face means content_limit, the editorial judgement about the material
// a consumer would RENDER — not content_rating, which answers who may buy the
// game.
//
// The seeded series already holds a live r18 member. If the flag were read off
// the age axis it would light up here, and it must not: on the production
// registry 61,690 live claimed works are r18 while only 13,664 are editorially
// nsfw, so an age-derived badge would over-mark 48,299 works whose material an
// editor judged safe to show.
func TestSeriesHasNSFWReadsTheDisplayAxis(t *testing.T) {
	db := openCatalogTestDB(t)
	seriesID, _, sfwWorkID, r18WorkID := seedSeries(t, db)
	app := seriesApp(db)

	code, body := getJSON(t, app, "/v1/catalog/series/"+itoa(seriesID))
	require.Equal(t, 200, code)
	assert.Equal(t, false, body["data"].(map[string]any)["has_nsfw"],
		"an r18 game whose display material is editorially sfw must NOT raise the flag")

	// Now let an editor mark that same work's material nsfw. Nothing about its
	// age rating changed; the flag flips because the display axis did.
	setDisplayNSFW(t, db, r18WorkID, true)
	code, body = getJSON(t, app, "/v1/catalog/series/"+itoa(seriesID))
	require.Equal(t, 200, code)
	assert.Equal(t, true, body["data"].(map[string]any)["has_nsfw"])

	// The reverse case is real too (273 works in production): an all-ages game
	// whose material the wiki marked nsfw raises it just as well.
	setDisplayNSFW(t, db, r18WorkID, false)
	setDisplayNSFW(t, db, sfwWorkID, true)
	code, body = getJSON(t, app, "/v1/catalog/series/"+itoa(seriesID))
	require.Equal(t, 200, code)
	assert.Equal(t, true, body["data"].(map[string]any)["has_nsfw"],
		"an all-ages game can carry nsfw display material")
}

// TestSeriesHasNSFWIgnoresTheCallersNSFWSetting pins the second property: the
// flag does not obey the nsfw query parameter.
//
// A badge derived from the filtered work_count would read false for exactly the
// callers who need it — an sfw caller would be told "nothing here" about a
// series whose adult works were subtracted before the flag was computed.
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

	// work_count still moves with the caller — the two fields answer different
	// questions and only one of them is the caller's to filter.
	_, body := getJSON(t, app, "/v1/catalog/series")
	assert.EqualValues(t, 1, body["data"].(map[string]any)["items"].([]any)[0].(map[string]any)["work_count"])
	_, body = getJSON(t, app, "/v1/catalog/series?nsfw=1")
	assert.EqualValues(t, 2, body["data"].(map[string]any)["items"].([]any)[0].(map[string]any)["work_count"])

	// Detail carries it unconditionally — no include=works needed, since the
	// point is to decide whether to ask for the works at all.
	code, body := getJSON(t, app, "/v1/catalog/series/"+itoa(seriesID))
	require.Equal(t, 200, code)
	data := body["data"].(map[string]any)
	assert.Equal(t, true, data["has_nsfw"])
	assert.NotContains(t, data, "works", "still include-gated")

	code, body = getJSON(t, app, "/v1/catalog/series/"+itoa(emptyID))
	require.Equal(t, 200, code)
	assert.Equal(t, false, body["data"].(map[string]any)["has_nsfw"])
}
