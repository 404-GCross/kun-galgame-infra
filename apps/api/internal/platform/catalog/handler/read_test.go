package handler

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	searchInfra "api/internal/infrastructure/search"
	"api/internal/platform/catalog/model"
	catsearch "api/internal/platform/catalog/search"
	"api/internal/platform/catalog/service"
	"api/pkg/config"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// readApp builds the S2S app with only the read surface wired (no auth — the
// read handlers impose none; auth is a separate prefix concern, covered by
// TestReadEndpoints_401).
func readApp(readSvc *service.ReadService, searcher *catsearch.Indexer) *fiber.App {
	app := fiber.New()
	Setup(app, nil, nil, readSvc, searcher)
	return app
}

func getJSON(t *testing.T, app *fiber.App, url string) (int, map[string]any) {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest("GET", url, nil))
	require.NoError(t, err)
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

// seedReadFixture wipes the read-relevant tables and inserts one fully-formed
// work: release (dlsite RJTEST anchor) + official/kana titles + circle label
// (attribution edge) + a voice credit (with character) + a scenario credit
// (orphan name, no character). Returns the work id.
func seedReadFixture(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	for _, tbl := range []string{
		"catalog_credit", "catalog_work_label", "catalog_external_ref", "catalog_work_title",
		"catalog_release", "catalog_work", "catalog_label", "catalog_credit_name", "catalog_character",
	} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	work := model.CatalogWork{MediumID: 5, OLang: "ja", DisplayName: "テスト音声", ContentRating: 2, Status: 0}
	require.NoError(t, db.Create(&work).Error)
	require.NoError(t, db.Create(&model.CatalogWorkTitle{WorkID: work.ID, Lang: "ja", Title: "テスト音声", Kind: model.WorkTitleKindOfficial}).Error)
	require.NoError(t, db.Create(&model.CatalogWorkTitle{WorkID: work.ID, Lang: "ja", Title: "テストオンセイ", Kind: model.WorkTitleKindSearchHint}).Error)
	rel := model.CatalogRelease{WorkID: work.ID, Kind: model.ReleaseKindDigital, ReleasedY: ptrI16(2024)}
	require.NoError(t, db.Create(&rel).Error)
	dlsite := int16(4)
	require.NoError(t, db.Create(&model.CatalogExternalRef{
		EntityType: model.EntityTypeRelease, EntityID: rel.ID, SourceID: dlsite, ExternalID: "RJTEST",
		LinkKind: model.LinkKindExact, MatchedBy: "rule:test",
	}).Error)
	label := model.CatalogLabel{DisplayName: "テスト社団", Kind: model.LabelKindDoujinCircle, Lang: "ja"}
	require.NoError(t, db.Create(&label).Error)
	require.NoError(t, db.Create(&model.CatalogWorkLabel{WorkID: work.ID, LabelID: label.ID, Kind: model.WorkLabelKindCircle, SourceID: &dlsite}).Error)

	va := model.CatalogCreditName{Name: "声優テスト", Lang: "ja"}
	require.NoError(t, db.Create(&va).Error)
	writer := model.CatalogCreditName{Name: "脚本テスト", Lang: "ja"}
	require.NoError(t, db.Create(&writer).Error)
	char := model.CatalogCharacter{DisplayName: "キャラテスト", Lang: "ja"}
	require.NoError(t, db.Create(&char).Error)
	require.NoError(t, db.Create(&model.CatalogCredit{WorkID: work.ID, CreditNameID: va.ID, RoleID: roleVoiceActor, CharacterID: &char.ID, SourceID: &dlsite}).Error)
	require.NoError(t, db.Create(&model.CatalogCredit{WorkID: work.ID, CreditNameID: writer.ID, RoleID: roleScenario, SourceID: &dlsite}).Error)
	return work.ID
}

// role ids seeded by seed.Run: voice-actor is a reserved hand-added id (1);
// scenario is a generated id — resolve it by key at fixture time.
const roleVoiceActor int64 = 1

var roleScenario int64

func TestWorkByAnchor(t *testing.T) {
	db := openCatalogTestDB(t)
	db.Raw("SELECT id FROM catalog_role WHERE key='scenario'").Scan(&roleScenario)
	workID := seedReadFixture(t, db)
	app := readApp(service.NewReadService(db), nil)

	// via the RELEASE anchor → traces back to the work, labels present.
	code, body := getJSON(t, app, "/api/v1/catalog/works/by-anchor?source=dlsite&external_id=RJTEST")
	require.Equal(t, 200, code)
	data := body["data"].(map[string]any)
	assert.EqualValues(t, workID, data["work"].(map[string]any)["id"])
	assert.EqualValues(t, 2, data["work"].(map[string]any)["content_rating"])
	assert.Len(t, data["titles"], 2)
	labels := data["labels"].([]any)
	require.Len(t, labels, 1)
	assert.Equal(t, "テスト社団", labels[0].(map[string]any)["display_name"])
	assert.EqualValues(t, model.WorkLabelKindCircle, labels[0].(map[string]any)["kind"])
	releases := data["releases"].([]any)
	require.Len(t, releases, 1)
	anchors := releases[0].(map[string]any)["anchors"].([]any)
	assert.Equal(t, "RJTEST", anchors[0].(map[string]any)["external_id"])

	// unknown anchor → 404; unknown source key → 404.
	code, _ = getJSON(t, app, "/api/v1/catalog/works/by-anchor?source=dlsite&external_id=RJNOPE")
	assert.Equal(t, 404, code)
	code, _ = getJSON(t, app, "/api/v1/catalog/works/by-anchor?source=bogus&external_id=RJTEST")
	assert.Equal(t, 404, code)
}

func TestWorkCredits(t *testing.T) {
	db := openCatalogTestDB(t)
	db.Raw("SELECT id FROM catalog_role WHERE key='scenario'").Scan(&roleScenario)
	workID := seedReadFixture(t, db)
	app := readApp(service.NewReadService(db), nil)

	code, body := getJSON(t, app, "/api/v1/catalog/works/"+itoa(workID)+"/credits")
	require.Equal(t, 200, code)
	data := body["data"].(map[string]any)
	groups := data["groups"].([]any)
	assert.Len(t, groups, 2, "two roles → two groups")
	// find the voice-actor group; its single credit carries the character.
	var vaGroup map[string]any
	for _, g := range groups {
		gm := g.(map[string]any)
		if gm["role_key"] == "voice-actor" {
			vaGroup = gm
		}
	}
	require.NotNil(t, vaGroup)
	vaCredit := vaGroup["credits"].([]any)[0].(map[string]any)
	assert.Equal(t, "声優テスト", vaCredit["name"])
	assert.Equal(t, "キャラテスト", vaCredit["character"])
}

// TestEntitySearch hits the REAL local catalog_credit_names index (populated by
// reindex-catalog, step 15). Skips if Meili is unreachable or the index is empty.
func TestEntitySearch(t *testing.T) {
	host := os.Getenv("MEILISEARCH_TEST_HOST")
	if host == "" {
		host = "http://127.0.0.1:7700"
	}
	client, err := searchInfra.NewClient(config.MeilisearchConfig{Host: host, APIKey: os.Getenv("MEILISEARCH_TEST_API_KEY")})
	if err != nil || client.Health() != nil {
		t.Skip("meilisearch unreachable")
	}
	n, _ := catsearch.NewIndexer(client).Count(catsearch.IndexCreditNames)
	if n == 0 {
		t.Skip("catalog_credit_names empty — run reindex-catalog")
	}
	app := readApp(nil, catsearch.NewIndexer(client))

	// "麻枝" → the two 麻枝准 rows (bangumi + eg).
	code, body := getJSON(t, app, "/api/v1/catalog/search/entities?q=%E9%BA%BB%E6%9E%9D&type=names&locale=ja&limit=5")
	require.Equal(t, 200, code)
	data := body["data"].(map[string]any)
	assert.GreaterOrEqual(t, int(data["total"].(float64)), 1)

	// limit is capped at 20.
	_, body = getJSON(t, app, "/api/v1/catalog/search/entities?q=&type=labels&limit=999")
	assert.LessOrEqual(t, len(body["data"].(map[string]any)["items"].([]any)), 20)

	// an invalid type is rejected (Huma enum validation → 422).
	code, _ = getJSON(t, app, "/api/v1/catalog/search/entities?q=x&type=bogus")
	assert.Equal(t, 422, code)
}

// TestReadEndpoints_401: the read surface sits under /api/v1/catalog and is
// therefore gated by S2SAuth — no credentials → 401 before any handler.
func TestReadEndpoints_401(t *testing.T) {
	app := fiber.New()
	app.Use("/api/v1/catalog", S2SAuth(nil))
	Setup(app, nil, nil, nil, nil)
	for _, url := range []string{
		"/api/v1/catalog/works/by-anchor?source=dlsite&external_id=RJTEST",
		"/api/v1/catalog/works/1/credits",
		"/api/v1/catalog/search/entities?q=x&type=names",
	} {
		resp, err := app.Test(httptest.NewRequest("GET", url, nil))
		require.NoError(t, err)
		assert.Equalf(t, 401, resp.StatusCode, "%s must 401", url)
	}
}

func ptrI16(v int16) *int16 { return &v }

func itoa(v int64) string { return strconv.FormatInt(v, 10) }
