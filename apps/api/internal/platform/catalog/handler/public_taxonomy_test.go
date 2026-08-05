// public_taxonomy_test.go — A2-1b wire-level coverage of the taxonomy read
// faces: the closed filter vocabularies (an unknown token is a 400, never a
// silently-dropped filter), the works-list engine_id filter's strict parsing,
// the shared limit / cursor semantics, and the engine detail's 400/404 split.
// Integration against kun_catalog_test (openCatalogTestDB).
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

// taxonomyApp mounts the taxonomy lanes (plus the works list, for the
// engine_id filter) bare — the devapi chain is a separate concern, exactly as
// in publicApp.
func taxonomyApp(db *gorm.DB) *fiber.App {
	resolveSvc := service.NewResolveService(repository.NewRedirectRepository(db))
	publicSvc := service.NewPublicService(db, service.NewReadService(db), resolveSvc, "")
	h := NewPublicHandler(publicSvc, resolveSvc, nil, nil)
	app := fiber.New()
	app.Get("/v1/catalog/works", h.WorksList)
	app.Get("/v1/catalog/labels", h.LabelsList)
	app.Get("/v1/catalog/tags", h.TagsList)
	app.Get("/v1/catalog/engines", h.EnginesList)
	app.Get("/v1/catalog/engines/:id", h.EngineDetail)
	return app
}

// seedTaxonomy wipes and refills the taxonomy tables: one label, one canonical
// tag, one engine, and one LIVE galgame work wired to all three.
func seedTaxonomy(t *testing.T, db *gorm.DB) (labelID, tagID, engineID int64) {
	t.Helper()
	for _, tbl := range []string{
		"catalog_work_engine", "catalog_engine", "catalog_tag_intro",
		"catalog_work_tag", "catalog_tag_source_map", "catalog_tag",
		"catalog_work_label", "catalog_label",
	} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	ids := seedPublicWorks(t, db, 1)
	// work_count counts LIVE claims only (146) — an entity page's member list is
	// works?<filter>=&claim_state=live, so the fixture work must be one or every
	// count on this face is legitimately 0.
	require.NoError(t, db.Exec(
		`UPDATE catalog_work SET site = 'galgame_wiki', product_work_id = 9350, claim_state = ? WHERE id = ?`,
		model.ClaimStateLive, ids[0]).Error)

	label := model.CatalogLabel{DisplayName: "Wire Brand", Kind: model.LabelKindGameBrand, LogoHash: fixtureLogoHash}
	require.NoError(t, db.Create(&label).Error)
	require.NoError(t, db.Create(&model.CatalogWorkLabel{
		WorkID: ids[0], LabelID: label.ID, Kind: model.WorkLabelKindBrand,
	}).Error)

	tag := model.CatalogTag{Name: "wire-tag", Tier: model.TagTierCore, Kind: model.TagKindContent}
	require.NoError(t, db.Create(&tag).Error)

	engine := model.CatalogEngine{Name: "WireEngine", Description: "", Aliases: []byte("[]")}
	require.NoError(t, db.Create(&engine).Error)
	require.NoError(t, db.Create(&model.CatalogWorkEngine{
		WorkID: ids[0], EngineID: engine.ID, SourceID: 2,
	}).Error)

	return label.ID, tag.ID, engine.ID
}

// TestTaxonomyClosedVocabularies pins the wave's 400 rule: a filter token
// outside our own closed set fails loudly with the legal values spelled out.
// Serving the unfiltered page instead would be a plausible-looking 200 whose
// rows do not answer the question that was asked.
func TestTaxonomyClosedVocabularies(t *testing.T) {
	db := openCatalogTestDB(t)
	seedTaxonomy(t, db)
	app := taxonomyApp(db)

	cases := []struct{ name, url, msg string }{
		{"label kind unknown", "/v1/catalog/labels?kind=studio", msgBadLabelKind},
		{"label kind numeric", "/v1/catalog/labels?kind=0", msgBadLabelKind},
		{"label kind uppercase", "/v1/catalog/labels?kind=GAME_BRAND", msgBadLabelKind},
		// "other" is an output-only fallback, never an accepted filter value.
		{"label kind other", "/v1/catalog/labels?kind=other", msgBadLabelKind},
		{"tag tier unknown", "/v1/catalog/tags?tier=popular", msgBadTagTier},
		{"tag tier numeric", "/v1/catalog/tags?tier=1", msgBadTagTier},
		{"tag kind unknown", "/v1/catalog/tags?kind=platform", msgBadTagKind},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := getJSON(t, app, tc.url)
			require.Equal(t, 400, code)
			assert.Equal(t, tc.msg, body["message"])
		})
	}

	// Every legal token is accepted (200) — the vocabulary check must not be
	// stricter than the vocabulary it advertises.
	for _, url := range []string{
		"/v1/catalog/labels", "/v1/catalog/labels?kind=game_brand", "/v1/catalog/labels?kind=bunko",
		"/v1/catalog/labels?kind=publisher", "/v1/catalog/labels?kind=anime_studio",
		"/v1/catalog/labels?kind=doujin_circle", "/v1/catalog/labels?kind=group",
		"/v1/catalog/tags", "/v1/catalog/tags?tier=core", "/v1/catalog/tags?tier=longtail",
		"/v1/catalog/tags?tier=hidden", "/v1/catalog/tags?kind=content", "/v1/catalog/tags?kind=meta",
		"/v1/catalog/tags?tier=core&kind=content", "/v1/catalog/engines",
	} {
		t.Run("legal "+url, func(t *testing.T) {
			code, _ := getJSON(t, app, url)
			assert.Equal(t, 200, code)
		})
	}
}

// TestTaxonomyLimitAndCursorSemantics pins that the three lanes inherit the
// works-list posture verbatim: non-positive / non-numeric limit is a 400, and a
// malformed cursor is a 400 (never a 500 — it is caller error).
func TestTaxonomyLimitAndCursorSemantics(t *testing.T) {
	db := openCatalogTestDB(t)
	seedTaxonomy(t, db)
	app := taxonomyApp(db)

	for _, url := range []string{
		"/v1/catalog/labels?limit=abc", "/v1/catalog/labels?limit=0", "/v1/catalog/labels?limit=-1",
		"/v1/catalog/tags?limit=abc", "/v1/catalog/tags?limit=0",
		"/v1/catalog/engines?limit=abc", "/v1/catalog/engines?limit=0",
	} {
		t.Run(url, func(t *testing.T) {
			code, body := getJSON(t, app, url)
			require.Equal(t, 400, code)
			assert.Equal(t, msgBadLimit, body["message"])
		})
	}
	for _, url := range []string{
		"/v1/catalog/labels?cursor=!!!nope!!!",
		"/v1/catalog/tags?cursor=!!!nope!!!",
		"/v1/catalog/engines?cursor=!!!nope!!!",
	} {
		t.Run(url, func(t *testing.T) {
			code, body := getJSON(t, app, url)
			require.Equal(t, 400, code)
			assert.Equal(t, msgBadCursor, body["message"])
		})
	}
}

// TestTaxonomyItemsCarryWorkCount checks the wire shape end to end, including
// the nsfw switch travelling from the query string into the count.
func TestTaxonomyItemsCarryWorkCount(t *testing.T) {
	db := openCatalogTestDB(t)
	labelID, tagID, engineID := seedTaxonomy(t, db)
	app := taxonomyApp(db)

	code, body := getJSON(t, app, "/v1/catalog/labels")
	require.Equal(t, 200, code)
	items := body["data"].(map[string]any)["items"].([]any)
	require.Len(t, items, 1)
	row := items[0].(map[string]any)
	assert.EqualValues(t, labelID, row["id"])
	assert.Equal(t, "Wire Brand", row["display_name"])
	assert.Equal(t, "game_brand", row["kind"])
	assert.EqualValues(t, 1, row["work_count"])
	// wave 170: the 会社 browse row carries the brand logo hash, always present
	// (a label with no logo reports "" rather than omitting the key).
	assert.Equal(t, fixtureLogoHash, row["logo_hash"])
	assert.Nil(t, body["data"].(map[string]any)["next_cursor"])

	code, body = getJSON(t, app, "/v1/catalog/tags")
	require.Equal(t, 200, code)
	row = body["data"].(map[string]any)["items"].([]any)[0].(map[string]any)
	assert.EqualValues(t, tagID, row["id"])
	assert.Equal(t, "core", row["tier"])
	assert.Equal(t, "content", row["kind"])
	assert.EqualValues(t, 0, row["work_count"], "an unmapped canonical tag reaches no work")

	code, body = getJSON(t, app, "/v1/catalog/engines?nsfw=1")
	require.Equal(t, 200, code)
	row = body["data"].(map[string]any)["items"].([]any)[0].(map[string]any)
	assert.EqualValues(t, engineID, row["id"])
	assert.Equal(t, "WireEngine", row["name"])
	assert.EqualValues(t, 1, row["work_count"])
}

// TestEngineDetailWire pins the 400 (non-numeric id) / 404 (unknown id) split
// and the always-present refs[].
func TestEngineDetailWire(t *testing.T) {
	db := openCatalogTestDB(t)
	_, _, engineID := seedTaxonomy(t, db)
	app := taxonomyApp(db)

	code, _ := getJSON(t, app, "/v1/catalog/engines/not-a-number")
	assert.Equal(t, 400, code)

	code, _ = getJSON(t, app, "/v1/catalog/engines/999999")
	assert.Equal(t, 404, code)

	code, body := getJSON(t, app, "/v1/catalog/engines/"+itoa(engineID))
	require.Equal(t, 200, code)
	data := body["data"].(map[string]any)
	assert.Equal(t, "WireEngine", data["name"])
	assert.EqualValues(t, 1, data["work_count"])
	assert.Equal(t, []any{}, data["refs"], "refs is always present, [] when the engine has no anchor")
}

// TestWorksListEngineIDFilter pins the ninth works-list filter: strict positive
// integer (an illegal value is a 400, never a silently-dropped filter) and a
// legal one actually narrows the page.
func TestWorksListEngineIDFilter(t *testing.T) {
	db := openCatalogTestDB(t)
	_, _, engineID := seedTaxonomy(t, db)
	app := taxonomyApp(db)

	for _, raw := range []string{"abc", "0", "-5", "1.5"} {
		t.Run("engine_id="+raw, func(t *testing.T) {
			code, body := getJSON(t, app, "/v1/catalog/works?engine_id="+raw)
			require.Equal(t, 400, code)
			assert.Equal(t, "engine_id must be a positive integer", body["message"])
		})
	}

	code, body := getJSON(t, app, "/v1/catalog/works?engine_id="+itoa(engineID))
	require.Equal(t, 200, code)
	assert.Len(t, body["data"].(map[string]any)["items"], 1)

	code, body = getJSON(t, app, "/v1/catalog/works?engine_id=999999")
	require.Equal(t, 200, code)
	assert.Empty(t, body["data"].(map[string]any)["items"], "no work uses engine 999999")
}
