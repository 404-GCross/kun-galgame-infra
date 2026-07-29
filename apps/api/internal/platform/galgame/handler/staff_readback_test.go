// staff_readback_test.go — A2-1e area B: the `/api` staff taxonomy read-back
// pairs and the `/internal` ownership-meta batch.
//
// The load-bearing assertion is the ROUND-TRIP one: each record's field set is
// exactly the matching Update*Request's editable set, so a console that
// prefills from it cannot erase a field on save. That is checked structurally
// (field-by-field over a fully-populated fixture), because the failure it
// guards against is a MISSING key — which no amount of value-comparison on a
// partial fixture would catch.
package handler

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"testing"

	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/repository"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	glogger "gorm.io/gorm/logger"
)

// openStaffTestDB opens the wiki-family test database and migrates the tables
// these ops read. Skips (never fails) when no database is reachable, matching
// the repo-wide integration-test convention.
func openStaffTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		dsn = "host=localhost port=5432 user=postgres password=postgres dbname=kun_catalog_test sslmode=disable"
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: glogger.Default.LogMode(glogger.Silent)})
	if err != nil {
		t.Skipf("no test DB: %v", err)
	}
	require.NoError(t, db.AutoMigrate(
		&model.GalgameTag{}, &model.GalgameTagAlias{},
		&model.GalgameOfficial{}, &model.GalgameOfficialAlias{},
		&model.GalgameEngine{}, &model.GalgameSeries{}, &model.Galgame{},
	))
	return db
}

// staffApp mounts the read-back routes with a stub auth layer: `role` decides
// whether the caller passes the taxonomy-editor gate, so the gate itself is
// exercised without dragging a real JWT verifier in.
func staffApp(db *gorm.DB, userID uint, roles []string) *fiber.App {
	h := NewStaffTaxonomyHandler(
		repository.NewTagRepository(db), repository.NewOfficialRepository(db),
		repository.NewEngineRepository(db), repository.NewSeriesRepository(db),
	)
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		if userID != 0 {
			c.Locals("user_id", userID)
			c.Locals("user_roles", roles)
		}
		return c.Next()
	})
	app.Get("/api/tag/search", h.TagSearch)
	app.Get("/api/tag/:id", h.TagDetail)
	app.Get("/api/official/search", h.OfficialSearch)
	app.Get("/api/official/:id", h.OfficialDetail)
	app.Get("/api/engine/search", h.EngineSearch)
	app.Get("/api/engine/:id", h.EngineDetail)
	app.Get("/api/series/search", h.SeriesSearch)
	app.Get("/api/series/:id", h.SeriesDetail)
	return app
}

func staffGet(t *testing.T, app *fiber.App, url string) (int, map[string]any) {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest("GET", url, nil))
	require.NoError(t, err)
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

// seedStaffTaxonomy wipes and inserts one FULLY-POPULATED row per family — every
// editable field non-empty, so a missing key in the projection is visible.
func seedStaffTaxonomy(t *testing.T, db *gorm.DB) (tagID, officialID, engineID, seriesID int) {
	t.Helper()
	for _, tbl := range []string{
		"galgame_tag_alias", "galgame_tag", "galgame_official_alias", "galgame_official",
		"galgame_engine",
	} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	require.NoError(t, db.Exec(`DELETE FROM galgame WHERE id IN (91001, 91002)`).Error)
	require.NoError(t, db.Exec(`DELETE FROM galgame_series WHERE name LIKE 'a2-1e%'`).Error)

	tag := model.GalgameTag{Name: "純愛(a2-1e)", Category: "content", Description: "タグ説明"}
	require.NoError(t, db.Create(&tag).Error)
	require.NoError(t, db.Create(&model.GalgameTagAlias{Name: "pure-love", GalgameTagID: tag.ID}).Error)

	official := model.GalgameOfficial{
		Name: "みるく(a2-1e)", Original: "MilkSoft", Link: "https://milk.example",
		Category: "company", Lang: "ja", Description: "会社説明",
	}
	require.NoError(t, db.Create(&official).Error)
	require.NoError(t, db.Create(&model.GalgameOfficialAlias{
		Name: "Milk", GalgameOfficialID: official.ID,
	}).Error)

	engine := model.GalgameEngine{
		Name: "KiriKiri(a2-1e)", Description: "エンジン説明",
		Alias: []byte(`["kirikiri2","krkr"]`),
	}
	require.NoError(t, db.Create(&engine).Error)

	series := model.GalgameSeries{Name: "a2-1e シリーズ", Description: "シリーズ説明"}
	require.NoError(t, db.Create(&series).Error)
	for _, gid := range []int{91001, 91002} {
		require.NoError(t, db.Exec(`
			INSERT INTO galgame (id, vndb_id, name_en_us, name_ja_jp, name_zh_cn, name_zh_tw,
			                     intro_en_us, intro_ja_jp, intro_zh_cn, intro_zh_tw,
			                     status, user_id, series_id)
			VALUES (?, '', '', ?, '', '', '', '', '', '', 0, 1, ?)`,
			gid, fmt.Sprintf("member%d", gid), series.ID).Error)
	}
	return tag.ID, official.ID, engine.ID, series.ID
}

// TestStaffTaxonomyReadBackCoversTheWritePayload is the wave's core area-B
// case: every family's record carries EXACTLY the editable field set of its
// update request. A key missing here is a field the console erases on save.
func TestStaffTaxonomyReadBackCoversTheWritePayload(t *testing.T) {
	db := openStaffTestDB(t)
	tagID, officialID, engineID, seriesID := seedStaffTaxonomy(t, db)
	app := staffApp(db, 7, []string{"admin"})

	for _, tc := range []struct {
		name    string
		url     string
		want    []string
		checks  map[string]any
		aliases []any
	}{
		{
			name: "tag", url: fmt.Sprintf("/api/tag/%d", tagID),
			want:    []string{"id", "name", "category", "description", "alias"},
			checks:  map[string]any{"name": "純愛(a2-1e)", "category": "content", "description": "タグ説明"},
			aliases: []any{"pure-love"},
		},
		{
			name: "official", url: fmt.Sprintf("/api/official/%d", officialID),
			want: []string{"id", "name", "original", "link", "lang", "category", "description", "alias"},
			checks: map[string]any{
				"name": "みるく(a2-1e)", "original": "MilkSoft", "link": "https://milk.example",
				"lang": "ja", "category": "company", "description": "会社説明",
			},
			aliases: []any{"Milk"},
		},
		{
			name: "engine", url: fmt.Sprintf("/api/engine/%d", engineID),
			want:    []string{"id", "name", "description", "alias"},
			checks:  map[string]any{"name": "KiriKiri(a2-1e)", "description": "エンジン説明"},
			aliases: []any{"kirikiri2", "krkr"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body := staffGet(t, app, tc.url)
			require.Equal(t, 200, code)
			data := body["data"].(map[string]any)
			// Exactly the write payload's editable set — no more, no less.
			keys := make([]string, 0, len(data))
			for k := range data {
				keys = append(keys, k)
			}
			assert.ElementsMatch(t, tc.want, keys, "read-back must mirror the update payload")
			for k, v := range tc.checks {
				assert.Equal(t, v, data[k], k)
			}
			assert.Equal(t, tc.aliases, data["alias"],
				"alias must round-trip — a console that cannot read it wipes it on save")
		})
	}

	t.Run("series", func(t *testing.T) {
		code, body := staffGet(t, app, fmt.Sprintf("/api/series/%d", seriesID))
		require.Equal(t, 200, code)
		data := body["data"].(map[string]any)
		keys := make([]string, 0, len(data))
		for k := range data {
			keys = append(keys, k)
		}
		assert.ElementsMatch(t, []string{"id", "name", "description", "galgame_ids"}, keys)
		assert.Equal(t, "a2-1e シリーズ", data["name"])
		assert.Equal(t, "シリーズ説明", data["description"])
		assert.Len(t, data["galgame_ids"], 2,
			"membership must round-trip — the update op replaces it wholesale")
	})
}

// TestStaffTaxonomySearchAndErrors covers the picker lane and the failure
// postures: identity-only rows, a blank query, an unknown id (404) and an
// illegal id (400).
func TestStaffTaxonomySearchAndErrors(t *testing.T) {
	db := openStaffTestDB(t)
	seedStaffTaxonomy(t, db)
	app := staffApp(db, 7, []string{"admin"})

	code, body := staffGet(t, app, "/api/tag/search?q=純愛")
	require.Equal(t, 200, code)
	data := body["data"].(map[string]any)
	items := data["items"].([]any)
	require.Len(t, items, 1)
	row := items[0].(map[string]any)
	assert.ElementsMatch(t, []string{"id", "name"}, mapKeys(row),
		"a picker row is identity only; the form reads the record by id")
	assert.EqualValues(t, 1, data["total"])

	// A blank query is an empty list, not the whole table (tag/official).
	code, body = staffGet(t, app, "/api/tag/search?q=%20")
	require.Equal(t, 200, code)
	assert.Empty(t, body["data"].(map[string]any)["items"])

	// engine / series pickers deliberately serve the whole (small) facet when
	// unfiltered — both consoles hydrate them as flat lists.
	for _, url := range []string{"/api/engine/search", "/api/series/search"} {
		code, body = staffGet(t, app, url)
		require.Equal(t, 200, code, url)
		assert.NotEmpty(t, body["data"].(map[string]any)["items"], url)
	}

	// Unknown id → 404; illegal id → 400 (never a degraded lookup of id 0).
	for _, tc := range []struct {
		url  string
		code int
	}{
		{"/api/tag/99999999", 404},
		{"/api/official/99999999", 404},
		{"/api/engine/0", 400},
		{"/api/series/abc", 400},
	} {
		code, _ = staffGet(t, app, tc.url)
		assert.Equal(t, tc.code, code, tc.url)
	}
}

// TestStaffTaxonomyReadBackIsGated pins the auth posture: the read-back sits
// behind the SAME gate the update ops enforce, so it can never become an
// anonymous data lane.
func TestStaffTaxonomyReadBackIsGated(t *testing.T) {
	db := openStaffTestDB(t)
	tagID, _, _, _ := seedStaffTaxonomy(t, db)
	url := fmt.Sprintf("/api/tag/%d", tagID)

	code, _ := staffGet(t, staffApp(db, 0, nil), url)
	assert.Equal(t, 401, code, "no JWT → 401")

	code, _ = staffGet(t, staffApp(db, 7, []string{"user"}), url)
	assert.Equal(t, 403, code, "a signed-in non-editor → 403")

	code, _ = staffGet(t, staffApp(db, 7, []string{"admin"}), url)
	assert.Equal(t, 200, code)

	// The picker lane is gated identically.
	code, _ = staffGet(t, staffApp(db, 7, []string{"user"}), "/api/tag/search?q=x")
	assert.Equal(t, 403, code)
}

// metaRowKeys is the meta op's frozen row shape (A2-1e + its tail): ownership,
// lifecycle, and the four localized names the forum's notification titles are
// built from. Nothing else may appear here — the moment a body field creeps in,
// this credentialed status-blind lane becomes a way to read unpublished
// content.
var metaRowKeys = []string{
	"gid", "user_id", "status",
	"name_zh_cn", "name_zh_tw", "name_ja_jp", "name_en_us",
}

// TestGalgameMetaBatchIsStatusBlind is the area-B ownership case: the op
// answers for UNPUBLISHED entries — which is the whole reason it exists, since
// the published-only batch read silently locks their owners out of their own
// edit lane — and it answers WITH the localized names, since that same read is
// where the notification title comes from.
func TestGalgameMetaBatchIsStatusBlind(t *testing.T) {
	db := openStaffTestDB(t)
	require.NoError(t, db.Exec(`DELETE FROM galgame WHERE id IN (92001,92002,92003,92004)`).Error)
	seed := func(id, status, userID int, zhCN, zhTW, jaJP, enUS string) {
		require.NoError(t, db.Exec(`
			INSERT INTO galgame (id, vndb_id, name_en_us, name_ja_jp, name_zh_cn, name_zh_tw,
			                     intro_en_us, intro_ja_jp, intro_zh_cn, intro_zh_tw, status, user_id)
			VALUES (?, '', ?, ?, ?, ?, '', '', '', '', ?, ?)`,
			id, enUS, jaJP, zhCN, zhTW, status, userID).Error)
	}
	seed(92001, model.GalgameStatusPublished, 11, "简体名", "繁體名", "日本語名", "English Name")
	seed(92002, model.GalgameStatusVNDBDraft, 12, "", "", "草案の名", "")
	seed(92003, model.GalgameStatusPending, 13, "", "", "", "")
	seed(92004, model.GalgameStatusBanned, 14, "封禁名", "", "", "")

	h := NewGalgameMetaHandler(repository.NewGalgameRepository(db))
	app := fiber.New()
	app.Get("/internal/galgame/meta", h.Meta)

	code, body := staffGet(t, app, "/internal/galgame/meta?ids=92001,92002,92003,92004,92999")
	require.Equal(t, 200, code)
	items := body["data"].(map[string]any)["items"].([]any)
	require.Len(t, items, 4, "an unresolvable id is absent, not an error")

	type meta struct {
		userID, status         float64
		zhCN, zhTW, jaJP, enUS string
	}
	want := map[float64]meta{
		92001: {11, float64(model.GalgameStatusPublished), "简体名", "繁體名", "日本語名", "English Name"},
		// An unpublished entry with only ONE locale filled: the notification
		// title falls back to it, which is the whole point of carrying all four.
		92002: {12, float64(model.GalgameStatusVNDBDraft), "", "", "草案の名", ""},
		// A wholly nameless row: every key must still be PRESENT and "", so a
		// consumer's fallback chain never has to tell absent from blank.
		92003: {13, float64(model.GalgameStatusPending), "", "", "", ""},
		92004: {14, float64(model.GalgameStatusBanned), "封禁名", "", "", ""},
	}
	for _, raw := range items {
		row := raw.(map[string]any)
		assert.ElementsMatch(t, metaRowKeys, mapKeys(row),
			"the meta op carries ownership + title only — never a body")
		exp := want[row["gid"].(float64)]
		assert.Equal(t, exp.userID, row["user_id"], "gid %v", row["gid"])
		assert.Equal(t, exp.status, row["status"], "gid %v", row["gid"])
		assert.Equal(t, exp.zhCN, row["name_zh_cn"], "gid %v", row["gid"])
		assert.Equal(t, exp.zhTW, row["name_zh_tw"], "gid %v", row["gid"])
		assert.Equal(t, exp.jaJP, row["name_ja_jp"], "gid %v", row["gid"])
		assert.Equal(t, exp.enUS, row["name_en_us"], "gid %v", row["gid"])
	}

	// Parameter posture: missing / malformed / over-limit are all 400.
	for _, url := range []string{
		"/internal/galgame/meta",
		"/internal/galgame/meta?ids=",
		"/internal/galgame/meta?ids=1,abc",
		"/internal/galgame/meta?ids=1,0",
	} {
		code, _ = staffGet(t, app, url)
		assert.Equal(t, 400, code, url)
	}
	over := "/internal/galgame/meta?ids=1"
	for i := 0; i < 100; i++ {
		over += ",1"
	}
	code, _ = staffGet(t, app, over)
	assert.Equal(t, 400, code, "over-limit is a 400, never a silent truncation")
}

func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
