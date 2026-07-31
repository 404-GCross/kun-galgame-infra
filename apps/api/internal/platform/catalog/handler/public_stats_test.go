// public_stats_test.go — wire-level coverage of the slim public counts lane
// (GET /v1/catalog/stats, 149b): what it counts (LIVE rows only, r18
// included), what it renders (medium id AND key), and — the point of the whole
// endpoint — what it must NEVER carry (the internal dashboard's telemetry).
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
	"gorm.io/gorm"
)

// statsApp mounts the counts lane bare (the devapi chain is a separate
// concern), with a real StatsService — the very service the internal dashboard
// uses, reached through its public-only method.
func statsApp(db *gorm.DB) *fiber.App {
	resolveSvc := service.NewResolveService(repository.NewRedirectRepository(db))
	publicSvc := service.NewPublicService(db, service.NewReadService(db), resolveSvc, "")
	h := NewPublicHandler(publicSvc, resolveSvc, nil, service.NewStatsService(db))
	app := fiber.New()
	app.Get("/v1/catalog/stats", h.Stats)
	return app
}

// seedStatsFixture wipes the counted tables and inserts a population whose
// every exclusion rule is exercised: one live all-ages galgame work, one live
// r18 galgame work, one live work of another medium, one stub, one merged and
// one soft-deleted row — plus one live and one soft-deleted label / character /
// person, and one credit name.
func seedStatsFixture(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, tbl := range []string{
		"catalog_work_label", "catalog_work", "catalog_label",
		"catalog_character", "catalog_credit_name", "catalog_person",
	} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	works := []model.CatalogWork{
		{MediumID: 1, OLang: "ja", DisplayName: "生きてる作品", ContentRating: model.ContentRatingAllAges, Status: model.WorkStatusLive},
		{MediumID: 1, OLang: "ja", DisplayName: "R18 作品", ContentRating: model.ContentRatingR18, Status: model.WorkStatusLive},
		{MediumID: 5, OLang: "ja", DisplayName: "別媒介", ContentRating: model.ContentRatingAllAges, Status: model.WorkStatusLive},
		{MediumID: 1, OLang: "ja", DisplayName: "スタブ", ContentRating: model.ContentRatingAllAges, Status: model.WorkStatusStub},
		{MediumID: 1, OLang: "ja", DisplayName: "マージ済", ContentRating: model.ContentRatingAllAges, Status: model.WorkStatusMerged},
		{MediumID: 1, OLang: "ja", DisplayName: "削除済", ContentRating: model.ContentRatingAllAges, Status: model.WorkStatusLive},
	}
	for i := range works {
		require.NoError(t, db.Create(&works[i]).Error)
	}
	require.NoError(t, db.Delete(&works[5]).Error) // soft delete

	live := model.CatalogLabel{DisplayName: "生きてるブランド", Kind: model.LabelKindGameBrand}
	gone := model.CatalogLabel{DisplayName: "消えたブランド", Kind: model.LabelKindGameBrand}
	require.NoError(t, db.Create(&live).Error)
	require.NoError(t, db.Create(&gone).Error)
	require.NoError(t, db.Delete(&gone).Error)

	ch := model.CatalogCharacter{DisplayName: "キャラ", Lang: "ja"}
	chGone := model.CatalogCharacter{DisplayName: "消えたキャラ", Lang: "ja"}
	require.NoError(t, db.Create(&ch).Error)
	require.NoError(t, db.Create(&chGone).Error)
	require.NoError(t, db.Delete(&chGone).Error)

	require.NoError(t, db.Create(&model.CatalogCreditName{Name: "名義", Lang: "ja"}).Error)
	require.NoError(t, db.Create(&model.CatalogPerson{DisplayName: "人物"}).Error)
}

// TestPublicStatsCountsLiveRowsOnly pins the population: LIVE, non-deleted rows
// only, r18 included, medium breakdown summing to total.
func TestPublicStatsCountsLiveRowsOnly(t *testing.T) {
	db := openCatalogTestDB(t)
	seedStatsFixture(t, db)
	app := statsApp(db)

	code, body := getJSON(t, app, "/v1/catalog/stats")
	require.Equal(t, 200, code)
	data := body["data"].(map[string]any)

	works := data["works"].(map[string]any)
	// 2 live galgame (one of them r18 — counted) + 1 live other medium; the
	// stub, the merged row and the soft-deleted row are all excluded.
	assert.EqualValues(t, 3, works["total"])
	byMedium := works["by_medium"].([]any)
	require.Len(t, byMedium, 2, "one row per medium that has live works")
	first := byMedium[0].(map[string]any)
	assert.EqualValues(t, 1, first["medium_id"])
	assert.Equal(t, "galgame", first["medium"], "the public face labels the row with the medium KEY, not a bare enum int")
	assert.EqualValues(t, 2, first["count"], "r18 works are counted — an aggregate exposes no content")
	assert.EqualValues(t, 1, byMedium[1].(map[string]any)["count"])

	// total is the sum of the breakdown — the two can never disagree.
	var sum float64
	for _, row := range byMedium {
		sum += row.(map[string]any)["count"].(float64)
	}
	assert.EqualValues(t, works["total"], sum)

	entities := data["entities"].(map[string]any)
	assert.EqualValues(t, 1, entities["labels"], "the soft-deleted label is not part of the catalogue")
	assert.EqualValues(t, 1, entities["characters"])
	assert.EqualValues(t, 1, entities["credit_names"])
	assert.EqualValues(t, 1, entities["persons"])
}

// TestPublicStatsExposesNoInternalTelemetry is the wave's red line: the public
// payload carries exactly two blocks. A future edit that "reuses" the internal
// dashboard DTO would leak review queue levels, LLM verdicts, the anchor tier
// matrix and source freshness onto the frozen contract — this test fails first.
func TestPublicStatsExposesNoInternalTelemetry(t *testing.T) {
	db := openCatalogTestDB(t)
	seedStatsFixture(t, db)
	app := statsApp(db)

	code, body := getJSON(t, app, "/v1/catalog/stats")
	require.Equal(t, 200, code)
	data := body["data"].(map[string]any)

	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	assert.ElementsMatch(t, []string{"works", "entities"}, keys)
	for _, forbidden := range []string{
		"queues", "llm_bid_verdicts", "anchors_by_source_tier", "source_freshness",
		"credits_by_source", "attributions_by_kind",
	} {
		assert.NotContains(t, data, forbidden)
	}
	// The entity block is the slim one: no orphan / org curation counters.
	entities := data["entities"].(map[string]any)
	assert.NotContains(t, entities, "orphan_credit_names")
	assert.NotContains(t, entities, "orgs")
}

// TestPublicStatsCachesLong pins the long public cache window — the payload is
// parameterless and moves by a handful of rows a day.
func TestPublicStatsCachesLong(t *testing.T) {
	db := openCatalogTestDB(t)
	seedStatsFixture(t, db)
	app := statsApp(db)

	resp, err := app.Test(httptest.NewRequest("GET", "/v1/catalog/stats", nil))
	require.NoError(t, err)
	assert.Equal(t, cacheStats, resp.Header.Get("Cache-Control"))
}
