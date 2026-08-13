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

func readApp(readSvc *service.ReadService, searcher *catsearch.Indexer) *fiber.App {
	app := fiber.New()
	Setup(app, nil, nil, readSvc, searcher, nil)
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

const fixtureLogoHash = "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"

const fixturePhotoHash = "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0"

func seedReadFixture(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	for _, tbl := range []string{
		"catalog_credit", "catalog_work_character", "catalog_work_label", "catalog_external_ref", "catalog_work_title",
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
	label := model.CatalogLabel{DisplayName: "テスト社団", Kind: model.LabelKindDoujinCircle, Lang: "ja", LogoHash: fixtureLogoHash}
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

const roleVoiceActor int64 = 1

var roleScenario int64

func TestWorkByAnchor(t *testing.T) {
	db := openCatalogTestDB(t)
	db.Raw("SELECT id FROM catalog_role WHERE key='scenario'").Scan(&roleScenario)
	workID := seedReadFixture(t, db)
	app := readApp(service.NewReadService(db), nil)

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

	code, _ = getJSON(t, app, "/api/v1/catalog/works/by-anchor?source=dlsite&external_id=RJNOPE")
	assert.Equal(t, 404, code)
	code, _ = getJSON(t, app, "/api/v1/catalog/works/by-anchor?source=bogus&external_id=RJTEST")
	assert.Equal(t, 404, code)
}

func TestWorkRefsBlock(t *testing.T) {
	db := openCatalogTestDB(t)
	db.Raw("SELECT id FROM catalog_role WHERE key='scenario'").Scan(&roleScenario)
	workID := seedReadFixture(t, db)
	var relID int64
	require.NoError(t, db.Raw("SELECT id FROM catalog_release LIMIT 1").Scan(&relID).Error)

	require.NoError(t, db.Create(&model.CatalogExternalRef{
		EntityType: model.EntityTypeWork, EntityID: workID, SourceID: 2, ExternalID: "v100",
		LinkKind: model.LinkKindExact, MatchedBy: "rule:test",
	}).Error)
	require.NoError(t, db.Create(&model.CatalogExternalRef{
		EntityType: model.EntityTypeWork, EntityID: workID, SourceID: 5, ExternalID: "eg-work",
		LinkKind: model.LinkKindProbable, MatchedBy: "rule:test",
	}).Error)
	require.NoError(t, db.Create(&model.CatalogExternalRef{
		EntityType: model.EntityTypeRelease, EntityID: relID, SourceID: 5, ExternalID: "eg-rel",
		LinkKind: model.LinkKindProbable, MatchedBy: "rule:test",
	}).Error)

	app := readApp(service.NewReadService(db), nil)
	code, body := getJSON(t, app, "/api/v1/catalog/works/"+itoa(workID))
	require.Equal(t, 200, code)
	refs := body["data"].(map[string]any)["refs"].([]any)

	type key struct {
		source, ext, level string
		relID              int64
	}
	got := map[key]bool{}
	for _, r := range refs {
		m := r.(map[string]any)
		k := key{source: m["source"].(string), ext: m["external_id"].(string), level: m["level"].(string)}
		if rid, ok := m["release_id"]; ok {
			k.relID = int64(rid.(float64))
		}
		got[k] = true
	}
	assert.Len(t, refs, 2, "only the two exact refs; both probable refs excluded")
	assert.True(t, got[key{source: "vndb", ext: "v100", level: "work"}], "work-level exact ref present")
	assert.True(t, got[key{source: "dlsite", ext: "RJTEST", level: "release", relID: relID}], "release-level exact ref carries its release id")
	assert.False(t, got[key{source: "erogamescape", ext: "eg-work", level: "work"}], "probable work ref excluded")
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

func TestSearchWorks(t *testing.T) {
	db := openCatalogTestDB(t)
	db.Raw("SELECT id FROM catalog_role WHERE key='scenario'").Scan(&roleScenario)
	workID := seedReadFixture(t, db)

	other := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "音声ゲーム", ContentRating: 0, Status: 0,
		Site: strptr("letmoe"), ProductWorkID: ptrI64(42)}
	require.NoError(t, db.Create(&other).Error)
	require.NoError(t, db.Create(&model.CatalogWorkTitle{WorkID: other.ID, Lang: "ja", Title: "音声ゲーム", Kind: model.WorkTitleKindOfficial}).Error)

	app := readApp(service.NewReadService(db), nil)

	code, body := getJSON(t, app, "/api/v1/catalog/works/search?q=%E9%9F%B3%E5%A3%B0")
	require.Equal(t, 200, code)
	items := body["data"].(map[string]any)["items"].([]any)
	require.Len(t, items, 2)
	byID := map[int64]map[string]any{}
	for _, it := range items {
		m := it.(map[string]any)
		byID[int64(m["work_id"].(float64))] = m
	}
	fx := byID[workID]
	require.NotNil(t, fx)
	assert.Equal(t, "RJTEST", fx["dlsite_id"])
	_, hasSite := fx["site"]
	assert.False(t, hasSite, "unclaimed work has no site key")
	oth := byID[other.ID]
	require.NotNil(t, oth)
	assert.Equal(t, "letmoe", oth["site"])
	_, hasDl := oth["dlsite_id"]
	assert.False(t, hasDl, "no dlsite anchor → omitted")

	code, body = getJSON(t, app, "/api/v1/catalog/works/search?q=%E9%9F%B3%E5%A3%B0&medium_id=1")
	require.Equal(t, 200, code)
	items = body["data"].(map[string]any)["items"].([]any)
	require.Len(t, items, 1)
	assert.EqualValues(t, other.ID, items[0].(map[string]any)["work_id"])

	code, body = getJSON(t, app, "/api/v1/catalog/works/search?q=%E3%83%86%E3%82%B9%E3%83%88")
	require.Equal(t, 200, code)
	assert.Len(t, body["data"].(map[string]any)["items"].([]any), 1)
	code, body = getJSON(t, app, "/api/v1/catalog/works/search?q=zzznomatch")
	require.Equal(t, 200, code)
	assert.Len(t, body["data"].(map[string]any)["items"].([]any), 0)
}

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
	app := readApp(service.NewReadService(openCatalogTestDB(t)), catsearch.NewIndexer(client))

	code, body := getJSON(t, app, "/api/v1/catalog/search/entities?q=%E9%BA%BB%E6%9E%9D&type=names&locale=ja&limit=5")
	require.Equal(t, 200, code)
	data := body["data"].(map[string]any)
	assert.GreaterOrEqual(t, int(data["total"].(float64)), 1)

	_, body = getJSON(t, app, "/api/v1/catalog/search/entities?q=&type=labels&limit=999")
	assert.LessOrEqual(t, len(body["data"].(map[string]any)["items"].([]any)), 20)

	code, _ = getJSON(t, app, "/api/v1/catalog/search/entities?q=x&type=bogus")
	assert.Equal(t, 422, code)
}

func TestWorkByIDAndLabelWorks(t *testing.T) {
	db := openCatalogTestDB(t)
	db.Raw("SELECT id FROM catalog_role WHERE key='scenario'").Scan(&roleScenario)
	workID := seedReadFixture(t, db)
	read := service.NewReadService(db)
	app := fiber.New()
	Setup(app, nil, nil, read, nil, nil)

	code, body := getJSON(t, app, "/api/v1/catalog/works/"+itoa(workID))
	require.Equal(t, 200, code)
	assert.EqualValues(t, workID, body["data"].(map[string]any)["work"].(map[string]any)["id"])
	code, _ = getJSON(t, app, "/api/v1/catalog/works/99999999")
	assert.Equal(t, 404, code)

	var labelID int64
	db.Raw("SELECT id FROM catalog_label LIMIT 1").Scan(&labelID)
	code, body = getJSON(t, app, "/api/v1/catalog/labels/"+itoa(labelID)+"/works")
	require.Equal(t, 200, code)
	data := body["data"].(map[string]any)
	assert.EqualValues(t, 1, data["total"])
	assert.EqualValues(t, workID, data["items"].([]any)[0].(map[string]any)["work_id"])
	assert.Equal(t, fixtureLogoHash, data["label"].(map[string]any)["logo_hash"])

	code, _ = getJSON(t, app, "/api/v1/catalog/labels/99999999/works")
	assert.Equal(t, 404, code)
}

func TestLabelLogoHashes(t *testing.T) {
	db := openCatalogTestDB(t)
	db.Raw("SELECT id FROM catalog_role WHERE key='scenario'").Scan(&roleScenario)
	seedReadFixture(t, db)

	var withLogo int64
	db.Raw("SELECT id FROM catalog_label LIMIT 1").Scan(&withLogo)
	noLogo := model.CatalogLabel{DisplayName: "ロゴ無し", Kind: model.LabelKindDoujinCircle, Lang: "ja"}
	require.NoError(t, db.Create(&noLogo).Error)
	dead := model.CatalogLabel{DisplayName: "合併済み", Kind: model.LabelKindDoujinCircle, Lang: "ja", LogoHash: fixtureLogoHash}
	require.NoError(t, db.Create(&dead).Error)
	require.NoError(t, db.Delete(&dead).Error)

	got, err := service.NewReadService(db).LabelLogoHashes(t.Context(), []int64{withLogo, noLogo.ID, dead.ID, 99999999})
	require.NoError(t, err)
	assert.Equal(t, map[int64]string{withLogo: fixtureLogoHash}, got)

	got, err = service.NewReadService(db).LabelLogoHashes(t.Context(), nil)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestLabelLogosSkipsNonLabelHits(t *testing.T) {
	s := &S2SServer{}
	got, err := s.labelLogos(t.Context(), []catsearch.EntityDoc{
		{ID: "n1", EntityType: "credit_name"},
		{ID: "c2", EntityType: "character"},
		{ID: "w3", EntityType: "work"},
	})
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestStats(t *testing.T) {
	db := openCatalogTestDB(t)
	db.Raw("SELECT id FROM catalog_role WHERE key='scenario'").Scan(&roleScenario)
	seedReadFixture(t, db)
	app := fiber.New()
	Setup(app, nil, nil, nil, nil, service.NewStatsService(db))

	code, body := getJSON(t, app, "/api/v1/catalog/stats")
	require.Equal(t, 200, code)
	data := body["data"].(map[string]any)
	assert.EqualValues(t, 1, data["works"].(map[string]any)["total"])
	ents := data["entities"].(map[string]any)
	assert.EqualValues(t, 2, ents["credit_names"])
	assert.EqualValues(t, 2, ents["orphan_credit_names"], "no person layer → all names orphan (doctrine)")
	assert.EqualValues(t, 0, ents["persons"], "person=0 surfaced honestly")
	anchors := data["anchors_by_source_tier"].([]any)
	require.Len(t, anchors, 1)
	assert.Equal(t, "dlsite", anchors[0].(map[string]any)["source"])
	attrs := data["attributions_by_kind"].([]any)
	require.Len(t, attrs, 1)
	assert.EqualValues(t, model.WorkLabelKindCircle, attrs[0].(map[string]any)["kind"])
}

func TestReadEndpoints_401(t *testing.T) {
	app := fiber.New()
	app.Use("/api/v1/catalog", S2SAuth(nil))
	Setup(app, nil, nil, nil, nil, nil)
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

type reverseFixture struct {
	personP, personQ           int64
	nameA, nameB, nameC, nameD int64
	char1, char2               int64
	work1, work2, work3        int64
}

func seedReverseFixture(t *testing.T, db *gorm.DB) reverseFixture {
	t.Helper()
	for _, tbl := range []string{
		"catalog_credit", "catalog_work_character", "catalog_work_label", "catalog_external_ref", "catalog_work_title",
		"catalog_release", "catalog_work", "catalog_label", "catalog_credit_name",
		"catalog_character", "catalog_person",
	} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	mk := func(name string, status int16) int64 {
		w := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: name, ContentRating: 0, Status: status}
		require.NoError(t, db.Create(&w).Error)
		return w.ID
	}
	var f reverseFixture
	f.work1, f.work2, f.work3 = mk("作品1", model.WorkStatusLive), mk("作品2", model.WorkStatusLive), mk("作品3", model.WorkStatusLive)

	i16 := func(v int16) *int16 { return &v }
	p := model.CatalogPerson{DisplayName: "人物P", PhotoHash: fixturePhotoHash,
		Gender: i16(1), BirthY: i16(1975), BirthM: i16(1), BirthD: i16(3)}
	require.NoError(t, db.Create(&p).Error)
	q := model.CatalogPerson{DisplayName: "人物Q", PhotoHash: fixturePhotoHash, Gender: i16(2)}
	require.NoError(t, db.Create(&q).Error)
	f.personP, f.personQ = p.ID, q.ID

	name := func(n string, personID int64, vis int16) int64 {
		cn := model.CatalogCreditName{Name: n, Lang: "ja", PersonID: &personID, LinkVisibility: vis}
		require.NoError(t, db.Create(&cn).Error)
		return cn.ID
	}
	f.nameA = name("名義A", p.ID, model.LinkVisibilityPublic)
	f.nameB = name("名義B", p.ID, model.LinkVisibilityPublic)
	f.nameC = name("名義C", q.ID, model.LinkVisibilityPublic)
	f.nameD = name("名義D", q.ID, model.LinkVisibilityHidden)

	c1 := model.CatalogCharacter{DisplayName: "キャラ1", Lang: "ja"}
	require.NoError(t, db.Create(&c1).Error)
	c2 := model.CatalogCharacter{DisplayName: "キャラ2", Lang: "ja"}
	require.NoError(t, db.Create(&c2).Error)
	f.char1, f.char2 = c1.ID, c2.ID

	credit := func(workID, nameID, roleID int64, charID *int64) {
		require.NoError(t, db.Create(&model.CatalogCredit{
			WorkID: workID, CreditNameID: nameID, RoleID: roleID, CharacterID: charID,
		}).Error)
	}
	credit(f.work1, f.nameA, roleVoiceActor, &f.char1)
	credit(f.work1, f.nameA, roleScenario, nil)
	credit(f.work2, f.nameA, roleVoiceActor, &f.char2)
	credit(f.work3, f.nameB, roleScenario, nil)
	credit(f.work1, f.nameC, roleVoiceActor, &f.char1)
	credit(f.work2, f.nameD, roleVoiceActor, &f.char2)
	return f
}

func TestNameWorks(t *testing.T) {
	db := openCatalogTestDB(t)
	db.Raw("SELECT id FROM catalog_role WHERE key='scenario'").Scan(&roleScenario)
	f := seedReverseFixture(t, db)
	app := readApp(service.NewReadService(db), nil)

	code, body := getJSON(t, app, "/api/v1/catalog/names/"+itoa(f.nameA)+"/works")
	require.Equal(t, 200, code)
	data := body["data"].(map[string]any)
	head := data["name"].(map[string]any)
	assert.EqualValues(t, f.nameA, head["id"])
	assert.Equal(t, "名義A", head["display_name"])
	assert.EqualValues(t, f.personP, head["person_id"])
	assert.Equal(t, fixturePhotoHash, head["photo_hash"])
	assert.EqualValues(t, 1, head["gender"])
	assert.EqualValues(t, 1975, head["birth_y"])
	assert.EqualValues(t, 1, head["birth_m"])
	assert.EqualValues(t, 3, head["birth_d"])
	sibs := head["siblings"].([]any)
	require.Len(t, sibs, 1, "person P's other public name only")
	assert.EqualValues(t, f.nameB, sibs[0].(map[string]any)["id"])
	assert.EqualValues(t, 2, data["total"])
	items := data["items"].([]any)
	require.Len(t, items, 2)
	byWork := map[int64]map[string]any{}
	for _, it := range items {
		m := it.(map[string]any)
		byWork[int64(m["work"].(map[string]any)["work_id"].(float64))] = m
	}
	w1 := byWork[f.work1]
	require.NotNil(t, w1)
	roles := w1["roles"].([]any)
	assert.Len(t, roles, 2, "A holds two roles on w1")
	var vaRole map[string]any
	for _, r := range roles {
		rm := r.(map[string]any)
		if rm["role_key"] == "voice-actor" {
			vaRole = rm
		}
	}
	require.NotNil(t, vaRole)
	assert.Equal(t, "キャラ1", vaRole["character"], "VA role carries the voiced character")

	code, body = getJSON(t, app, "/api/v1/catalog/names/"+itoa(f.nameA)+"/works?limit=1")
	require.Equal(t, 200, code)
	assert.EqualValues(t, 2, body["data"].(map[string]any)["total"])
	assert.Len(t, body["data"].(map[string]any)["items"].([]any), 1)

	code, body = getJSON(t, app, "/api/v1/catalog/names/"+itoa(f.nameC)+"/works")
	require.Equal(t, 200, code)
	assert.Empty(t, body["data"].(map[string]any)["name"].(map[string]any)["siblings"],
		"a hidden-linked sibling never surfaces")

	code, body = getJSON(t, app, "/api/v1/catalog/names/"+itoa(f.nameD)+"/works")
	require.Equal(t, 200, code)
	dHead := body["data"].(map[string]any)["name"].(map[string]any)
	_, hasPerson := dHead["person_id"]
	assert.False(t, hasPerson, "hidden link → person_id withheld")
	assert.Empty(t, dHead["siblings"])
	assert.Equal(t, "", dHead["photo_hash"], "hidden link → photo withheld, key still present")
	for _, k := range []string{"gender", "birth_y", "birth_m", "birth_d"} {
		_, has := dHead[k]
		assert.Falsef(t, has, "hidden link → %s withheld", k)
	}
	assert.EqualValues(t, 1, body["data"].(map[string]any)["total"], "works are still listed")

	code, _ = getJSON(t, app, "/api/v1/catalog/names/99999999/works")
	assert.Equal(t, 404, code)
}

func TestCharacterWorks(t *testing.T) {
	db := openCatalogTestDB(t)
	db.Raw("SELECT id FROM catalog_role WHERE key='scenario'").Scan(&roleScenario)
	f := seedReverseFixture(t, db)
	require.NoError(t, db.Create(&model.CatalogWorkCharacter{
		WorkID: f.work1, CharacterID: f.char1, Kind: model.WorkCharacterKindMain, Spoiler: model.SpoilerMild, MatchedBy: "import:test"}).Error)
	require.NoError(t, db.Create(&model.CatalogWorkCharacter{
		WorkID: f.work3, CharacterID: f.char1, Kind: model.WorkCharacterKindAppears, MatchedBy: "import:test"}).Error)
	app := readApp(service.NewReadService(db), nil)

	code, body := getJSON(t, app, "/api/v1/catalog/characters/"+itoa(f.char1)+"/works")
	require.Equal(t, 200, code)
	data := body["data"].(map[string]any)
	assert.EqualValues(t, f.char1, data["character"].(map[string]any)["id"])
	assert.Equal(t, "キャラ1", data["character"].(map[string]any)["display_name"])
	assert.EqualValues(t, 2, data["total"], "union: w1 (voiced) + w3 (appearance-only)")
	items := data["items"].([]any)
	require.Len(t, items, 2)
	byWork := map[int64]map[string]any{}
	for _, it := range items {
		m := it.(map[string]any)
		byWork[int64(m["work"].(map[string]any)["work_id"].(float64))] = m
	}
	w1 := byWork[f.work1]
	require.NotNil(t, w1)
	assert.EqualValues(t, model.WorkCharacterKindMain, w1["kind"])
	assert.EqualValues(t, model.SpoilerMild, w1["spoiler"], "roster edge spoiler surfaces on character→works")
	assert.Equal(t, true, w1["voiced"])
	voices := w1["voices"].([]any)
	assert.Len(t, voices, 2, "both A and C voiced char1 on w1")
	voiceNames := map[int64]bool{}
	for _, v := range voices {
		voiceNames[int64(v.(map[string]any)["credit_name_id"].(float64))] = true
	}
	assert.True(t, voiceNames[f.nameA] && voiceNames[f.nameC])
	w3 := byWork[f.work3]
	require.NotNil(t, w3)
	assert.EqualValues(t, model.WorkCharacterKindAppears, w3["kind"])
	assert.Equal(t, false, w3["voiced"])
	assert.Empty(t, w3["voices"].([]any))

	code, _ = getJSON(t, app, "/api/v1/catalog/characters/99999999/works")
	assert.Equal(t, 404, code)
}

func TestWorkCharacters(t *testing.T) {
	db := openCatalogTestDB(t)
	db.Raw("SELECT id FROM catalog_role WHERE key='scenario'").Scan(&roleScenario)
	for _, tbl := range []string{
		"catalog_credit", "catalog_work_character", "catalog_work", "catalog_credit_name", "catalog_character",
	} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	mkWork := func(name string) int64 {
		w := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: name, ContentRating: 0, Status: 0}
		require.NoError(t, db.Create(&w).Error)
		return w.ID
	}
	work := mkWork("花名册テスト")
	empty := mkWork("空作品")

	female := model.GenderFemale
	hash := "abc123hash"
	chMain := model.CatalogCharacter{DisplayName: "主人公", Lang: "ja", Gender: &female, ImageHash: &hash}
	require.NoError(t, db.Create(&chMain).Error)
	chAppears := model.CatalogCharacter{DisplayName: "脇役", Lang: "ja"}
	require.NoError(t, db.Create(&chAppears).Error)
	chCredit := model.CatalogCharacter{DisplayName: "客演", Lang: "ja"}
	require.NoError(t, db.Create(&chCredit).Error)

	va1 := model.CatalogCreditName{Name: "AAA声優", Lang: "ja"}
	va2 := model.CatalogCreditName{Name: "ZZZ声優", Lang: "ja"}
	va3 := model.CatalogCreditName{Name: "客演声優", Lang: "ja"}
	require.NoError(t, db.Create(&va1).Error)
	require.NoError(t, db.Create(&va2).Error)
	require.NoError(t, db.Create(&va3).Error)

	edge := func(charID int64, kind, spoiler int16) {
		require.NoError(t, db.Create(&model.CatalogWorkCharacter{
			WorkID: work, CharacterID: charID, Kind: kind, Spoiler: spoiler, MatchedBy: "import:test"}).Error)
	}
	edge(chMain.ID, model.WorkCharacterKindMain, model.SpoilerSevere)
	edge(chAppears.ID, model.WorkCharacterKindAppears, model.SpoilerNone)
	vcredit := func(charID, nameID int64) {
		require.NoError(t, db.Create(&model.CatalogCredit{
			WorkID: work, CreditNameID: nameID, RoleID: roleVoiceActor, CharacterID: &charID}).Error)
	}
	vcredit(chMain.ID, va1.ID)
	vcredit(chMain.ID, va2.ID)
	vcredit(chCredit.ID, va3.ID)

	app := readApp(service.NewReadService(db), nil)

	code, body := getJSON(t, app, "/api/v1/catalog/works/"+itoa(work))
	require.Equal(t, 200, code)
	chars := body["data"].(map[string]any)["characters"].([]any)
	require.Len(t, chars, 3, "union: 2 roster edges + 1 credit-only")

	c0 := chars[0].(map[string]any)
	assert.EqualValues(t, chMain.ID, c0["character_id"])
	assert.EqualValues(t, model.WorkCharacterKindMain, c0["kind"])
	assert.EqualValues(t, model.SpoilerSevere, c0["spoiler"], "roster edge spoiler surfaces")
	assert.EqualValues(t, model.GenderFemale, c0["gender"])
	assert.Equal(t, "abc123hash", c0["image_hash"])
	c0va := c0["va"].([]any)
	require.Len(t, c0va, 2, "chMain has two VAs")
	assert.Equal(t, "AAA声優", c0va[0].(map[string]any)["name"], "va sorted by name")
	assert.Equal(t, "ZZZ声優", c0va[1].(map[string]any)["name"])

	c1 := chars[1].(map[string]any)
	assert.EqualValues(t, chAppears.ID, c1["character_id"])
	assert.EqualValues(t, model.WorkCharacterKindAppears, c1["kind"])
	assert.Empty(t, c1["va"].([]any), "roster-only character → empty (non-null) va")
	_, hasGender := c1["gender"]
	assert.False(t, hasGender, "unknown gender omitted")

	c2 := chars[2].(map[string]any)
	assert.EqualValues(t, chCredit.ID, c2["character_id"])
	assert.EqualValues(t, model.WorkCharacterKindUnknown, c2["kind"], "credit-only → kind 0")
	assert.EqualValues(t, model.SpoilerNone, c2["spoiler"], "credit-only → spoiler 0")
	assert.Len(t, c2["va"].([]any), 1)

	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(empty))
	require.Equal(t, 200, code)
	assert.Empty(t, body["data"].(map[string]any)["characters"].([]any))
}

func TestCharacterDetail(t *testing.T) {
	db := openCatalogTestDB(t)
	for _, tbl := range []string{"catalog_character_intro", "catalog_character_alias", "catalog_character"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	male := model.GenderMale
	latin := "Shujinkou"
	ch := model.CatalogCharacter{DisplayName: "主人公", Lang: "ja", Latin: &latin, Gender: &male, Description: "テスト説明"}
	require.NoError(t, db.Create(&ch).Error)
	require.NoError(t, db.Create(&model.CatalogCharacterAlias{
		CharacterID: ch.ID, Name: "しゅじんこう", Lang: "ja", Kind: model.AliasKindSpellingVariant}).Error)
	var vndbSrc, bangumiSrc, dlsiteSrc, derivedSrc int16
	require.NoError(t, db.Raw(`SELECT id FROM catalog_source WHERE key = 'vndb'`).Scan(&vndbSrc).Error)
	require.NoError(t, db.Raw(`SELECT id FROM catalog_source WHERE key = 'bangumi'`).Scan(&bangumiSrc).Error)
	require.NoError(t, db.Raw(`SELECT id FROM catalog_source WHERE key = 'dlsite'`).Scan(&dlsiteSrc).Error)
	require.NoError(t, db.Raw(`SELECT id FROM catalog_source WHERE key = 'derived'`).Scan(&derivedSrc).Error)
	require.NotZero(t, vndbSrc)
	require.NotZero(t, derivedSrc)
	for _, in := range []model.CatalogCharacterIntro{
		{CharacterID: ch.ID, Lang: "en", Intro: "An English intro.", SourceID: vndbSrc},
		{CharacterID: ch.ID, Lang: "ja", Intro: "日本語の紹介。", SourceID: bangumiSrc},
		{CharacterID: ch.ID, Lang: "ja", Intro: "負けるほうの紹介。", SourceID: dlsiteSrc},
		{CharacterID: ch.ID, Lang: "zh-Hans", Intro: "机翻的介绍。", SourceID: vndbSrc, Provenance: 1},
		{CharacterID: ch.ID, Lang: "zh-Hans", Intro: "提取的介绍。", SourceID: derivedSrc, Provenance: 1},
	} {
		require.NoError(t, db.Create(&in).Error)
	}
	chBare := model.CatalogCharacter{DisplayName: "紹介なし"}
	require.NoError(t, db.Create(&chBare).Error)

	app := readApp(service.NewReadService(db), nil)

	code, body := getJSON(t, app, "/api/v1/catalog/characters/"+itoa(ch.ID))
	require.Equal(t, 200, code)
	data := body["data"].(map[string]any)
	assert.EqualValues(t, ch.ID, data["id"])
	assert.Equal(t, "主人公", data["display_name"])
	assert.Equal(t, "Shujinkou", data["latin"])
	assert.Equal(t, "ja", data["lang"])
	assert.EqualValues(t, model.GenderMale, data["gender"])
	assert.Equal(t, "テスト説明", data["description"])
	aliases := data["aliases"].([]any)
	require.Len(t, aliases, 1)
	assert.Equal(t, "しゅじんこう", aliases[0].(map[string]any)["name"])
	assert.EqualValues(t, model.AliasKindSpellingVariant, aliases[0].(map[string]any)["kind"])

	intros := data["intros"].([]any)
	require.Len(t, intros, 3, "one element per language after the source merge")
	i0 := intros[0].(map[string]any)
	assert.Equal(t, "en", i0["lang"], "sorted by lang")
	assert.Equal(t, "An English intro.", i0["intro"])
	assert.EqualValues(t, vndbSrc, i0["source_id"])
	i1 := intros[1].(map[string]any)
	assert.Equal(t, "ja", i1["lang"])
	assert.Equal(t, "日本語の紹介。", i1["intro"], "lowest source_id wins the language")
	assert.EqualValues(t, bangumiSrc, i1["source_id"])
	i2 := intros[2].(map[string]any)
	assert.Equal(t, "zh-Hans", i2["lang"])
	assert.Equal(t, "提取的介绍。", i2["intro"], "derived extraction outranks translated machine rows")
	assert.EqualValues(t, derivedSrc, i2["source_id"])
	assert.Equal(t, true, i2["machine"])

	code, body = getJSON(t, app, "/api/v1/catalog/characters/"+itoa(chBare.ID))
	require.Equal(t, 200, code)
	bare := body["data"].(map[string]any)
	introsBare, ok := bare["intros"].([]any)
	require.True(t, ok, "intros present and non-null")
	assert.Empty(t, introsBare)

	code, _ = getJSON(t, app, "/api/v1/catalog/characters/99999999")
	assert.Equal(t, 404, code)
}

var galgameStubIDs = []int64{5001, 5002}

func ensureGalgameStub(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS galgame (
		id bigserial PRIMARY KEY,
		catalog_work_id bigint,
		user_id int NOT NULL DEFAULT 0,
		name_en_us text NOT NULL DEFAULT '',
		name_ja_jp text NOT NULL DEFAULT '',
		name_zh_cn text NOT NULL DEFAULT '',
		name_zh_tw text NOT NULL DEFAULT '',
		intro_en_us text NOT NULL DEFAULT '',
		intro_ja_jp text NOT NULL DEFAULT '',
		intro_zh_cn text NOT NULL DEFAULT '',
		intro_zh_tw text NOT NULL DEFAULT ''
	)`).Error)
	for _, col := range []string{"name_en_us", "name_ja_jp", "name_zh_cn", "name_zh_tw"} {
		require.NoError(t, db.Exec(
			`ALTER TABLE galgame ADD COLUMN IF NOT EXISTS `+col+` text NOT NULL DEFAULT ''`).Error)
	}
	require.NoError(t, db.Exec(
		`ALTER TABLE galgame ADD COLUMN IF NOT EXISTS content_limit varchar(10) DEFAULT 'sfw'`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS galgame_alias (
		id bigserial PRIMARY KEY,
		galgame_id bigint NOT NULL,
		name text NOT NULL DEFAULT ''
	)`).Error)
	require.NoError(t, db.Exec(`DELETE FROM galgame_alias WHERE galgame_id IN ?`, galgameStubIDs).Error)
	require.NoError(t, db.Exec(`DELETE FROM galgame WHERE id IN ?`, galgameStubIDs).Error)
}

func insertGalgameBody(t *testing.T, db *gorm.DB, id, catalogWorkID int64, en, ja, zhCN, zhTW string) {
	t.Helper()
	require.NoError(t, db.Exec(`INSERT INTO galgame (id, catalog_work_id, user_id, intro_en_us, intro_ja_jp, intro_zh_cn, intro_zh_tw)
		VALUES (?, ?, 0, ?, ?, ?, ?)`, id, catalogWorkID, en, ja, zhCN, zhTW).Error)
}

func TestWorkIntro(t *testing.T) {
	db := openCatalogTestDB(t)
	ensureGalgameStub(t, db)
	ensureGalgameRatingStub(t, db)
	for _, tbl := range []string{"catalog_work_intro", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	var srcGalgameWiki, srcVNDB, srcUser int16
	db.Raw("SELECT id FROM catalog_source WHERE key IN ('curated','galgame_wiki')").Scan(&srcGalgameWiki)
	db.Raw("SELECT id FROM catalog_source WHERE key='vndb'").Scan(&srcVNDB)
	db.Raw("SELECT id FROM catalog_source WHERE key='user'").Scan(&srcUser)
	require.NotZero(t, srcGalgameWiki, "galgame_wiki source must be seeded")

	claimed := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "主張作品", ContentRating: 0, Status: 0,
		Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(5001)}
	require.NoError(t, db.Create(&claimed).Error)
	require.NoError(t, db.Create(&model.CatalogWorkIntro{
		WorkID: claimed.ID, Lang: "en", Intro: "English intro.", SourceID: srcGalgameWiki,
		Provenance: model.IntroProvenanceSource}).Error)
	require.NoError(t, db.Create(&model.CatalogWorkIntro{
		WorkID: claimed.ID, Lang: "ja", Intro: "日本語の紹介。", SourceID: srcGalgameWiki,
		Provenance: model.IntroProvenanceSource}).Error)

	bodyless := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "無体作品", ContentRating: 0, Status: 0}
	require.NoError(t, db.Create(&bodyless).Error)
	require.NoError(t, db.Create(&model.CatalogWorkIntro{WorkID: bodyless.ID, Lang: "en", Intro: "VNDB english", SourceID: srcVNDB}).Error)
	require.NoError(t, db.Create(&model.CatalogWorkIntro{WorkID: bodyless.ID, Lang: "en", Intro: "User english", SourceID: srcUser}).Error)
	require.NoError(t, db.Create(&model.CatalogWorkIntro{WorkID: bodyless.ID, Lang: "ja", Intro: "VNDB日本語", SourceID: srcVNDB}).Error)

	merged := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "空主張", ContentRating: 0, Status: 0,
		Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(5002)}
	require.NoError(t, db.Create(&merged).Error)
	require.NoError(t, db.Create(&model.CatalogWorkIntro{WorkID: merged.ID, Lang: "en", Intro: "VNDB english", SourceID: srcVNDB}).Error)

	app := readApp(service.NewReadService(db), nil)

	code, body := getJSON(t, app, "/api/v1/catalog/works/"+itoa(claimed.ID))
	require.Equal(t, 200, code)
	intro := body["data"].(map[string]any)["intro"].([]any)
	require.Len(t, intro, 2, "en + ja rows; the empty zh columns never became rows")
	assert.Equal(t, "en", intro[0].(map[string]any)["lang"])
	assert.Equal(t, "English intro.", intro[0].(map[string]any)["intro"])
	assert.EqualValues(t, srcGalgameWiki, intro[0].(map[string]any)["source_id"], "the wiki pivot is attributed to galgame_wiki")
	assert.Equal(t, "ja", intro[1].(map[string]any)["lang"])
	assert.Equal(t, "日本語の紹介。", intro[1].(map[string]any)["intro"])

	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(bodyless.ID))
	require.Equal(t, 200, code)
	intro = body["data"].(map[string]any)["intro"].([]any)
	require.Len(t, intro, 2, "en (one winner) + ja")
	en := intro[0].(map[string]any)
	assert.Equal(t, "en", en["lang"])
	assert.Equal(t, "User english", en["intro"], "lowest source_id wins the language")
	assert.EqualValues(t, srcUser, en["source_id"])
	assert.EqualValues(t, srcVNDB, intro[1].(map[string]any)["source_id"])

	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(merged.ID))
	require.Equal(t, 200, code)
	intro = body["data"].(map[string]any)["intro"].([]any)
	require.Len(t, intro, 1, "a claimed work with no wiki row is not shadowed; the native one serves")
	assert.Equal(t, "VNDB english", intro[0].(map[string]any)["intro"])
	assert.EqualValues(t, srcVNDB, intro[0].(map[string]any)["source_id"])
}

func TestWorkCover(t *testing.T) {
	db := openCatalogTestDB(t)
	for _, tbl := range []string{"catalog_work_cover", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	var srcCurated, srcVNDB, srcUpscale, srcUser int16
	db.Raw("SELECT id FROM catalog_source WHERE key IN ('curated','galgame_wiki')").Scan(&srcCurated)
	db.Raw("SELECT id FROM catalog_source WHERE key='vndb'").Scan(&srcVNDB)
	db.Raw("SELECT id FROM catalog_source WHERE key='upscale'").Scan(&srcUpscale)
	db.Raw("SELECT id FROM catalog_source WHERE key='user'").Scan(&srcUser)
	require.NotZero(t, srcCurated, "the curated source must be seeded")
	require.NotZero(t, srcUpscale, "upscale source must be seeded (step 53)")

	claimed := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "主張作品", ContentRating: 0, Status: 0,
		Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(6001)}
	require.NoError(t, db.Create(&claimed).Error)
	require.NoError(t, db.Create(&model.CatalogWorkCover{
		WorkID: claimed.ID, ImageHash: "hash_vndb_landscape", SortOrder: 0, Kind: "main",
		Sexual: 1, SourceID: srcVNDB}).Error)
	require.NoError(t, db.Create(&model.CatalogWorkCover{
		WorkID: claimed.ID, ImageHash: "hash_upscale_portrait", SortOrder: 1,
		PortraitPinned: true, SourceID: srcUpscale}).Error)
	require.NoError(t, db.Create(&model.CatalogWorkCover{
		WorkID: claimed.ID, ImageHash: "hash_user_extra", SortOrder: 2, SourceID: srcCurated}).Error)

	bodyless := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "無体作品", ContentRating: 0, Status: 0}
	require.NoError(t, db.Create(&bodyless).Error)
	require.NoError(t, db.Create(&model.CatalogWorkCover{
		WorkID: bodyless.ID, ImageHash: "hash_bodyless_pin", SortOrder: 0, Kind: "pkgfront",
		PortraitPinned: true, Sexual: 2, Violence: 0, SourceID: srcVNDB}).Error)
	require.NoError(t, db.Create(&model.CatalogWorkCover{
		WorkID: bodyless.ID, ImageHash: "hash_bodyless_extra", SortOrder: 1, SourceID: srcUser}).Error)

	claimedEmpty := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "空主張", ContentRating: 0, Status: 0,
		Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(6002)}
	require.NoError(t, db.Create(&claimedEmpty).Error)

	empty := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "空作品", ContentRating: 0, Status: 0}
	require.NoError(t, db.Create(&empty).Error)

	app := readApp(service.NewReadService(db), nil)

	code, body := getJSON(t, app, "/api/v1/catalog/works/"+itoa(claimed.ID))
	require.Equal(t, 200, code)
	covers := body["data"].(map[string]any)["covers"].([]any)
	require.Len(t, covers, 3, "a claimed work reads its own native covers")
	c0 := covers[0].(map[string]any)
	assert.Equal(t, "hash_vndb_landscape", c0["image_hash"])
	assert.EqualValues(t, 0, c0["sort_order"])
	assert.Equal(t, false, c0["portrait_pinned"])
	assert.EqualValues(t, srcVNDB, c0["source_id"], "vndb provenance kept")
	assert.EqualValues(t, 1, c0["sexual"])
	c1 := covers[1].(map[string]any)
	assert.Equal(t, "hash_upscale_portrait", c1["image_hash"])
	assert.Equal(t, true, c1["portrait_pinned"], "the portrait pin surfaces")
	assert.EqualValues(t, srcUpscale, c1["source_id"])
	c2 := covers[2].(map[string]any)
	assert.Equal(t, "hash_user_extra", c2["image_hash"])
	assert.EqualValues(t, srcCurated, c2["source_id"], "the wiki user upload stays on the curated lane")

	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(bodyless.ID))
	require.Equal(t, 200, code)
	covers = body["data"].(map[string]any)["covers"].([]any)
	require.Len(t, covers, 2)
	b0 := covers[0].(map[string]any)
	assert.Equal(t, "hash_bodyless_pin", b0["image_hash"])
	assert.Equal(t, true, b0["portrait_pinned"])
	assert.Equal(t, "pkgfront", b0["kind"])
	assert.EqualValues(t, 2, b0["sexual"])
	assert.EqualValues(t, srcVNDB, b0["source_id"])
	assert.EqualValues(t, srcUser, covers[1].(map[string]any)["source_id"])

	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(claimedEmpty.ID))
	require.Equal(t, 200, code)
	assert.Empty(t, body["data"].(map[string]any)["covers"].([]any))

	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(empty.ID))
	require.Equal(t, 200, code)
	assert.Empty(t, body["data"].(map[string]any)["covers"].([]any))
}

func TestWorkScreenshot(t *testing.T) {
	db := openCatalogTestDB(t)
	for _, tbl := range []string{"catalog_work_screenshot", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	var srcCurated, srcVNDB, srcUser, srcDlsite int16
	db.Raw("SELECT id FROM catalog_source WHERE key IN ('curated','galgame_wiki')").Scan(&srcCurated)
	db.Raw("SELECT id FROM catalog_source WHERE key='vndb'").Scan(&srcVNDB)
	db.Raw("SELECT id FROM catalog_source WHERE key='user'").Scan(&srcUser)
	db.Raw("SELECT id FROM catalog_source WHERE key='dlsite'").Scan(&srcDlsite)
	require.NotZero(t, srcCurated, "the curated source must be seeded")
	require.NotZero(t, srcDlsite, "dlsite source must be seeded")

	claimed := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "主張作品", ContentRating: 0, Status: 0,
		Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(7001)}
	require.NoError(t, db.Create(&claimed).Error)
	require.NoError(t, db.Create(&model.CatalogWorkScreenshot{
		WorkID: claimed.ID, ImageHash: "hash_vndb_shot", SortOrder: 0, Caption: "オープニング",
		Sexual: 1, SourceID: srcVNDB}).Error)
	require.NoError(t, db.Create(&model.CatalogWorkScreenshot{
		WorkID: claimed.ID, ImageHash: "hash_user_shot", SortOrder: 1, SourceID: srcCurated}).Error)

	bodyless := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "無体作品", ContentRating: 0, Status: 0}
	require.NoError(t, db.Create(&bodyless).Error)
	require.NoError(t, db.Create(&model.CatalogWorkScreenshot{
		WorkID: bodyless.ID, ImageHash: "hash_bodyless_shot", SortOrder: 0, Caption: "本体截图",
		Sexual: 2, Violence: 0, SourceID: srcVNDB}).Error)
	require.NoError(t, db.Create(&model.CatalogWorkScreenshot{
		WorkID: bodyless.ID, ImageHash: "hash_bodyless_extra", SortOrder: 1, SourceID: srcUser}).Error)

	dlsiteOnly := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "商店样图", ContentRating: 0, Status: 0,
		Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(7002)}
	require.NoError(t, db.Create(&dlsiteOnly).Error)
	require.NoError(t, db.Create(&model.CatalogWorkScreenshot{
		WorkID: dlsiteOnly.ID, ImageHash: "hash_claimed_dlsite_only", SortOrder: 0,
		Sexual: 2, SourceID: srcDlsite}).Error)

	mixed := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "双源", ContentRating: 0, Status: 0,
		Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(7003)}
	require.NoError(t, db.Create(&mixed).Error)
	require.NoError(t, db.Create(&model.CatalogWorkScreenshot{
		WorkID: mixed.ID, ImageHash: "hash_rescued_wiki", SortOrder: 0, Caption: "wiki caption",
		Sexual: 1, SourceID: srcCurated}).Error)
	require.NoError(t, db.Create(&model.CatalogWorkScreenshot{
		WorkID: mixed.ID, ImageHash: "hash_native_dlsite", SortOrder: 1, SourceID: srcDlsite}).Error)

	claimedEmpty := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "空主張", ContentRating: 0, Status: 0,
		Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(7004)}
	require.NoError(t, db.Create(&claimedEmpty).Error)

	empty := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "空作品", ContentRating: 0, Status: 0}
	require.NoError(t, db.Create(&empty).Error)

	app := readApp(service.NewReadService(db), nil)

	code, body := getJSON(t, app, "/api/v1/catalog/works/"+itoa(claimed.ID))
	require.Equal(t, 200, code)
	shots := body["data"].(map[string]any)["screenshots"].([]any)
	require.Len(t, shots, 2, "a claimed work reads its own native screenshots")
	s0 := shots[0].(map[string]any)
	assert.Equal(t, "hash_vndb_shot", s0["image_hash"])
	assert.EqualValues(t, 0, s0["sort_order"])
	assert.Equal(t, "オープニング", s0["caption"], "caption carried")
	assert.EqualValues(t, srcVNDB, s0["source_id"])
	assert.EqualValues(t, 1, s0["sexual"])
	s1 := shots[1].(map[string]any)
	assert.Equal(t, "hash_user_shot", s1["image_hash"])
	assert.Equal(t, "", s1["caption"], "empty caption preserved")
	assert.EqualValues(t, srcCurated, s1["source_id"], "the rescued wiki upload stays on the curated lane")

	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(bodyless.ID))
	require.Equal(t, 200, code)
	shots = body["data"].(map[string]any)["screenshots"].([]any)
	require.Len(t, shots, 2)
	b0 := shots[0].(map[string]any)
	assert.Equal(t, "hash_bodyless_extra", b0["image_hash"])
	assert.EqualValues(t, srcUser, b0["source_id"], "the lower source_id leads")
	b1 := shots[1].(map[string]any)
	assert.Equal(t, "hash_bodyless_shot", b1["image_hash"])
	assert.Equal(t, "本体截图", b1["caption"])
	assert.EqualValues(t, 2, b1["sexual"])
	assert.EqualValues(t, srcVNDB, b1["source_id"])

	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(dlsiteOnly.ID))
	require.Equal(t, 200, code)
	shots = body["data"].(map[string]any)["screenshots"].([]any)
	require.Len(t, shots, 1)
	n0 := shots[0].(map[string]any)
	assert.Equal(t, "hash_claimed_dlsite_only", n0["image_hash"])
	assert.EqualValues(t, srcDlsite, n0["source_id"], "native row keeps its dlsite attribution")
	assert.EqualValues(t, 2, n0["sexual"])

	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(mixed.ID))
	require.Equal(t, 200, code)
	shots = body["data"].(map[string]any)["screenshots"].([]any)
	require.Len(t, shots, 2, "no lane is filtered by source")
	m0 := shots[0].(map[string]any)
	assert.Equal(t, "hash_native_dlsite", m0["image_hash"])
	assert.EqualValues(t, srcDlsite, m0["source_id"])
	m1 := shots[1].(map[string]any)
	assert.Equal(t, "hash_rescued_wiki", m1["image_hash"])
	assert.Equal(t, "wiki caption", m1["caption"])
	assert.EqualValues(t, srcCurated, m1["source_id"])

	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(claimedEmpty.ID))
	require.Equal(t, 200, code)
	assert.Empty(t, body["data"].(map[string]any)["screenshots"].([]any))

	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(empty.ID))
	require.Equal(t, 200, code)
	assert.Empty(t, body["data"].(map[string]any)["screenshots"].([]any))
}

var galgameRatingStubIDs = []int64{8001, 8002}

func ensureGalgameRatingStub(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS galgame_vndb_meta (
		galgame_id bigint PRIMARY KEY,
		vndb_id text NOT NULL,
		rating numeric,
		vote_count bigint NOT NULL,
		synced_at timestamptz NOT NULL
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS galgame_bangumi_meta (
		galgame_id bigint PRIMARY KEY,
		bid bigint NOT NULL,
		score numeric NOT NULL,
		rank bigint NOT NULL,
		total bigint NOT NULL,
		nsfw boolean NOT NULL,
		synced_at timestamptz NOT NULL
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS galgame_eg_meta (
		galgame_id bigint PRIMARY KEY,
		eg_game_id bigint NOT NULL,
		median bigint,
		vote_count bigint NOT NULL,
		synced_at timestamptz NOT NULL
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS galgame_dlsite_meta (
		galgame_id bigint PRIMARY KEY,
		workno text NOT NULL,
		rate_average_star numeric,
		rate_count bigint,
		dl_count bigint,
		wishlist_count bigint,
		review_count bigint,
		synced_at timestamptz NOT NULL
	)`).Error)
	require.NoError(t, db.Exec(`DELETE FROM galgame_vndb_meta WHERE galgame_id IN ?`, galgameRatingStubIDs).Error)
	require.NoError(t, db.Exec(`DELETE FROM galgame_bangumi_meta WHERE galgame_id IN ?`, galgameRatingStubIDs).Error)
	require.NoError(t, db.Exec(`DELETE FROM galgame_eg_meta WHERE galgame_id IN ?`, galgameRatingStubIDs).Error)
	require.NoError(t, db.Exec(`DELETE FROM galgame_dlsite_meta WHERE galgame_id IN ?`, galgameRatingStubIDs).Error)
}

func insertDlsiteMeta(t *testing.T, db *gorm.DB, galgameID int64, star *float64, rateCount *int, dl, wl *int64, rv *int) {
	t.Helper()
	require.NoError(t, db.Exec(`INSERT INTO galgame_dlsite_meta
		(galgame_id, workno, rate_average_star, rate_count, dl_count, wishlist_count, review_count, synced_at)
		VALUES (?, 'RJ62TEST', ?, ?, ?, ?, ?, now())`, galgameID, star, rateCount, dl, wl, rv).Error)
}

func insertVNDBMeta(t *testing.T, db *gorm.DB, galgameID int64, rating *float64, voteCount int) {
	t.Helper()
	require.NoError(t, db.Exec(`INSERT INTO galgame_vndb_meta
		(galgame_id, vndb_id, rating, vote_count, synced_at)
		VALUES (?, 'v1', ?, ?, now())`, galgameID, rating, voteCount).Error)
}

func insertBangumiMeta(t *testing.T, db *gorm.DB, galgameID int64, score float64, rank, total int) {
	t.Helper()
	require.NoError(t, db.Exec(`INSERT INTO galgame_bangumi_meta
		(galgame_id, bid, score, rank, total, nsfw, synced_at)
		VALUES (?, 0, ?, ?, ?, false, now())`, galgameID, score, rank, total).Error)
}

func insertEGMeta(t *testing.T, db *gorm.DB, galgameID int64, median *int, voteCount int) {
	t.Helper()
	require.NoError(t, db.Exec(`INSERT INTO galgame_eg_meta
		(galgame_id, eg_game_id, median, vote_count, synced_at)
		VALUES (?, 0, ?, ?, now())`, galgameID, median, voteCount).Error)
}

func TestWorkRating(t *testing.T) {
	db := openCatalogTestDB(t)
	ensureGalgameStub(t, db)
	for _, tbl := range []string{"catalog_work_rating", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	var srcVNDB, srcBangumi, srcDlsite, srcEG int16
	db.Raw("SELECT id FROM catalog_source WHERE key='vndb'").Scan(&srcVNDB)
	db.Raw("SELECT id FROM catalog_source WHERE key='bangumi'").Scan(&srcBangumi)
	db.Raw("SELECT id FROM catalog_source WHERE key='dlsite'").Scan(&srcDlsite)
	db.Raw("SELECT id FROM catalog_source WHERE key='erogamescape'").Scan(&srcEG)
	require.NotZero(t, srcVNDB, "vndb source must be seeded")
	require.NotZero(t, srcBangumi, "bangumi source must be seeded")
	require.NotZero(t, srcDlsite, "dlsite source must be seeded")
	require.NotZero(t, srcEG, "erogamescape source must be seeded")

	claimed := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "主張作品", ContentRating: 0, Status: 0,
		Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(8001)}
	require.NoError(t, db.Create(&claimed).Error)
	bgmRank1234 := 1234
	for _, row := range []model.CatalogWorkRating{
		{WorkID: claimed.ID, SourceID: srcVNDB, Score: 8.45, VoteCount: 456},
		{WorkID: claimed.ID, SourceID: srcBangumi, Score: 7.6, VoteCount: 890, Rank: &bgmRank1234},
		{WorkID: claimed.ID, SourceID: srcDlsite, Score: 4.36, VoteCount: 120},
		{WorkID: claimed.ID, SourceID: srcEG, Score: 78, VoteCount: 321},
	} {
		require.NoError(t, db.Create(&row).Error)
	}

	bodyless := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "無体作品", ContentRating: 0, Status: 0}
	require.NoError(t, db.Create(&bodyless).Error)
	bgmRank := 5000
	require.NoError(t, db.Create(&model.CatalogWorkRating{
		WorkID: bodyless.ID, SourceID: srcBangumi, Score: 6.3, VoteCount: 150, Rank: &bgmRank}).Error)
	require.NoError(t, db.Create(&model.CatalogWorkRating{
		WorkID: bodyless.ID, SourceID: srcEG, Score: 82, VoteCount: 40}).Error)

	xor := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "空主張", ContentRating: 0, Status: 0,
		Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(8002)}
	require.NoError(t, db.Create(&xor).Error)
	require.NoError(t, db.Create(&model.CatalogWorkRating{
		WorkID: xor.ID, SourceID: srcBangumi, Score: 9.9, VoteCount: 1}).Error)

	empty := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "空作品", ContentRating: 0, Status: 0}
	require.NoError(t, db.Create(&empty).Error)

	app := readApp(service.NewReadService(db), nil)

	code, body := getJSON(t, app, "/api/v1/catalog/works/"+itoa(claimed.ID))
	require.Equal(t, 200, code)
	ratings := body["data"].(map[string]any)["ratings"].([]any)
	require.Len(t, ratings, 4, "all four sources materialized")
	rVndb := ratings[0].(map[string]any)
	assert.EqualValues(t, srcVNDB, rVndb["source_id"], "vndb row first (source_id ascending)")
	assert.EqualValues(t, 8.45, rVndb["score"], "vndb wire 84.5 decoded to its displayed 1-10 scale")
	assert.EqualValues(t, 456, rVndb["vote_count"])
	_, hasRank := rVndb["rank"]
	assert.False(t, hasRank, "vndb meta has no rank → omitted")
	r0 := ratings[1].(map[string]any)
	assert.EqualValues(t, srcBangumi, r0["source_id"], "bangumi row second")
	assert.EqualValues(t, 7.6, r0["score"], "bangumi score on its native 0-10 scale")
	assert.EqualValues(t, 890, r0["vote_count"], "vote_count = meta total")
	assert.EqualValues(t, 1234, r0["rank"], "bangumi rank surfaces")
	rDl := ratings[2].(map[string]any)
	assert.EqualValues(t, srcDlsite, rDl["source_id"], "dlsite row third")
	assert.EqualValues(t, 4.36, rDl["score"], "dlsite star average on its native 0-5 scale")
	assert.EqualValues(t, 120, rDl["vote_count"], "vote_count = rate_count")
	_, hasRank = rDl["rank"]
	assert.False(t, hasRank, "dlsite has no rank → omitted")
	r1 := ratings[3].(map[string]any)
	assert.EqualValues(t, srcEG, r1["source_id"])
	assert.EqualValues(t, 78, r1["score"], "EG median on its native 0-100 scale")
	assert.EqualValues(t, 321, r1["vote_count"])
	_, hasRank = r1["rank"]
	assert.False(t, hasRank, "EG has no rank → omitted")

	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(bodyless.ID))
	require.Equal(t, 200, code)
	ratings = body["data"].(map[string]any)["ratings"].([]any)
	require.Len(t, ratings, 2)
	b0 := ratings[0].(map[string]any)
	assert.EqualValues(t, srcBangumi, b0["source_id"])
	assert.EqualValues(t, 6.3, b0["score"])
	assert.EqualValues(t, 150, b0["vote_count"])
	assert.EqualValues(t, 5000, b0["rank"])
	b1 := ratings[1].(map[string]any)
	assert.EqualValues(t, srcEG, b1["source_id"])
	assert.EqualValues(t, 82, b1["score"])
	_, hasRank = b1["rank"]
	assert.False(t, hasRank, "NULL rank omitted")

	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(xor.ID))
	require.Equal(t, 200, code)
	ratings = body["data"].(map[string]any)["ratings"].([]any)
	require.Len(t, ratings, 1, "an unscored upstream leaves no row; the native one is not shadowed")
	assert.EqualValues(t, srcBangumi, ratings[0].(map[string]any)["source_id"])
	assert.EqualValues(t, 9.9, ratings[0].(map[string]any)["score"])

	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(empty.ID))
	require.Equal(t, 200, code)
	assert.Empty(t, body["data"].(map[string]any)["ratings"].([]any))
}

func TestWorkPopularity(t *testing.T) {
	db := openCatalogTestDB(t)
	ensureGalgameStub(t, db)
	for _, tbl := range []string{"catalog_work_popularity", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	var srcDlsite, srcBgm int16
	db.Raw("SELECT id FROM catalog_source WHERE key='dlsite'").Scan(&srcDlsite)
	require.NotZero(t, srcDlsite, "dlsite source must be seeded")
	db.Raw("SELECT id FROM catalog_source WHERE key='bangumi'").Scan(&srcBgm)
	require.NotZero(t, srcBgm, "bangumi source must be seeded")

	claimed := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "主張作品", ContentRating: 0, Status: 0,
		Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(8001)}
	require.NoError(t, db.Create(&claimed).Error)
	for _, row := range []model.CatalogWorkPopularity{
		{WorkID: claimed.ID, SourceID: srcBgm, Metric: model.PopularityMetricBgmWish, Value: 42},
		{WorkID: claimed.ID, SourceID: srcDlsite, Metric: model.PopularityMetricDownloads, Value: 2000},
		{WorkID: claimed.ID, SourceID: srcDlsite, Metric: model.PopularityMetricWishlist, Value: 300},
		{WorkID: claimed.ID, SourceID: srcDlsite, Metric: model.PopularityMetricReviews, Value: 0},
	} {
		require.NoError(t, db.Create(&row).Error)
	}

	bodyless := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "無体作品", ContentRating: 0, Status: 0}
	require.NoError(t, db.Create(&bodyless).Error)
	require.NoError(t, db.Create(&model.CatalogWorkPopularity{
		WorkID: bodyless.ID, SourceID: srcDlsite, Metric: model.PopularityMetricReviews, Value: 7}).Error)
	require.NoError(t, db.Create(&model.CatalogWorkPopularity{
		WorkID: bodyless.ID, SourceID: srcDlsite, Metric: model.PopularityMetricDownloads, Value: 4500}).Error)

	xor := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "空主張", ContentRating: 0, Status: 0,
		Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(8002)}
	require.NoError(t, db.Create(&xor).Error)
	require.NoError(t, db.Create(&model.CatalogWorkPopularity{
		WorkID: xor.ID, SourceID: srcDlsite, Metric: model.PopularityMetricDownloads, Value: 99}).Error)

	empty := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "空作品", ContentRating: 0, Status: 0}
	require.NoError(t, db.Create(&empty).Error)

	app := readApp(service.NewReadService(db), nil)

	code, body := getJSON(t, app, "/api/v1/catalog/works/"+itoa(claimed.ID))
	require.Equal(t, 200, code)
	pop := body["data"].(map[string]any)["popularity"].([]any)
	require.Len(t, pop, 4, "native bgm shelf + three dlsite counters")
	p0 := pop[0].(map[string]any)
	assert.EqualValues(t, srcBgm, p0["source_id"], "bgm sorts before dlsite")
	assert.EqualValues(t, model.PopularityMetricBgmWish, p0["metric"])
	assert.EqualValues(t, 42, p0["value"], "the claimed work's native bgm row surfaces")
	p1 := pop[1].(map[string]any)
	assert.EqualValues(t, srcDlsite, p1["source_id"])
	assert.EqualValues(t, 0, p1["metric"], "downloads first (metric ascending)")
	assert.EqualValues(t, 2000, p1["value"], "the dlsite downloads counter")
	p2 := pop[2].(map[string]any)
	assert.EqualValues(t, 1, p2["metric"])
	assert.EqualValues(t, 300, p2["value"])
	p3 := pop[3].(map[string]any)
	assert.EqualValues(t, 2, p3["metric"])
	assert.EqualValues(t, 0, p3["value"], "a published 0 is a real row")

	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(bodyless.ID))
	require.Equal(t, 200, code)
	pop = body["data"].(map[string]any)["popularity"].([]any)
	require.Len(t, pop, 2)
	assert.EqualValues(t, 0, pop[0].(map[string]any)["metric"], "downloads before reviews")
	assert.EqualValues(t, 4500, pop[0].(map[string]any)["value"])
	assert.EqualValues(t, 2, pop[1].(map[string]any)["metric"])
	assert.EqualValues(t, 7, pop[1].(map[string]any)["value"])

	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(xor.ID))
	require.Equal(t, 200, code)
	pop = body["data"].(map[string]any)["popularity"].([]any)
	require.Len(t, pop, 1, "a claimed work's own dlsite row is not shadowed")
	assert.EqualValues(t, 99, pop[0].(map[string]any)["value"])

	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(empty.ID))
	require.Equal(t, 200, code)
	assert.Empty(t, body["data"].(map[string]any)["popularity"].([]any))
}

var (
	galgameTagStubGalgameIDs = []int64{9001, 9002}
	galgameTagStubTagIDs     = []int64{9101, 9102, 9103, 9104}
)

func ensureGalgameTagStub(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS galgame_tag (
		id bigserial PRIMARY KEY,
		name text NOT NULL UNIQUE,
		category text NOT NULL,
		description text DEFAULT ''
	)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS galgame_tag_relation (
		galgame_id bigint NOT NULL,
		tag_id bigint NOT NULL,
		spoiler_level bigint DEFAULT 0,
		source varchar(16) DEFAULT '',
		PRIMARY KEY (galgame_id, tag_id)
	)`).Error)
	require.NoError(t, db.Exec(`DELETE FROM galgame_tag_relation WHERE galgame_id IN ?`, galgameTagStubGalgameIDs).Error)
	require.NoError(t, db.Exec(`DELETE FROM galgame_tag WHERE id IN ?`, galgameTagStubTagIDs).Error)
}

func insertGalgameTag(t *testing.T, db *gorm.DB, id int64, name string) {
	t.Helper()
	require.NoError(t, db.Exec(`INSERT INTO galgame_tag (id, name, category) VALUES (?, ?, 'content')`, id, name).Error)
}

func insertGalgameTagRelation(t *testing.T, db *gorm.DB, galgameID, tagID int64, spoiler int, source string) {
	t.Helper()
	require.NoError(t, db.Exec(`INSERT INTO galgame_tag_relation (galgame_id, tag_id, spoiler_level, source)
		VALUES (?, ?, ?, ?)`, galgameID, tagID, spoiler, source).Error)
}

func TestWorkTag(t *testing.T) {
	db := openCatalogTestDB(t)
	ensureGalgameStub(t, db)
	for _, tbl := range []string{"catalog_work_tag", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	var srcGalgameWiki, srcVNDB, srcBangumi int16
	db.Raw("SELECT id FROM catalog_source WHERE key IN ('curated','galgame_wiki')").Scan(&srcGalgameWiki)
	db.Raw("SELECT id FROM catalog_source WHERE key='vndb'").Scan(&srcVNDB)
	db.Raw("SELECT id FROM catalog_source WHERE key='bangumi'").Scan(&srcBangumi)
	require.NotZero(t, srcGalgameWiki, "galgame_wiki source must be seeded")
	require.NotZero(t, srcVNDB, "vndb source must be seeded")
	require.NotZero(t, srcBangumi, "bangumi source must be seeded")

	claimed := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "主張作品", ContentRating: 0, Status: 0,
		Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(9001)}
	require.NoError(t, db.Create(&claimed).Error)
	for _, row := range []model.CatalogWorkTag{
		{WorkID: claimed.ID, Name: "恋愛(58b)", SourceID: srcGalgameWiki},
		{WorkID: claimed.ID, Name: "泣きゲー(58b)", SourceID: srcVNDB},
		{WorkID: claimed.ID, Name: "ネタバレ(58b)", SourceID: srcVNDB, Spoiler: 2},
		{WorkID: claimed.ID, Name: "百合", Count: 30, SourceID: srcBangumi},
		{WorkID: claimed.ID, Name: "PC", Count: 5, SourceID: srcBangumi},
	} {
		require.NoError(t, db.Create(&row).Error)
	}

	bodyless := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "無体作品", ContentRating: 0, Status: 0}
	require.NoError(t, db.Create(&bodyless).Error)
	for _, row := range []model.CatalogWorkTag{
		{WorkID: bodyless.ID, Name: "拔作", Count: 1, SourceID: srcBangumi},
		{WorkID: bodyless.ID, Name: "PC", Count: 5, SourceID: srcBangumi},
		{WorkID: bodyless.ID, Name: "百合", Count: 30, SourceID: srcBangumi},
		{WorkID: bodyless.ID, Name: "ADV", Count: 5, SourceID: srcBangumi},
	} {
		require.NoError(t, db.Create(&row).Error)
	}

	nativeOnly := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "原生主張", ContentRating: 0, Status: 0,
		Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(9002)}
	require.NoError(t, db.Create(&nativeOnly).Error)
	require.NoError(t, db.Create(&model.CatalogWorkTag{
		WorkID: nativeOnly.ID, Name: "ネタバレ(58b)", SourceID: srcVNDB, Spoiler: 2}).Error)
	require.NoError(t, db.Create(&model.CatalogWorkTag{
		WorkID: nativeOnly.ID, Name: "百合", Count: 99, SourceID: srcBangumi}).Error)

	empty := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "空作品", ContentRating: 0, Status: 0}
	require.NoError(t, db.Create(&empty).Error)

	app := readApp(service.NewReadService(db), nil)

	code, body := getJSON(t, app, "/api/v1/catalog/works/"+itoa(claimed.ID))
	require.Equal(t, 200, code)
	tags := body["data"].(map[string]any)["tags"].([]any)
	require.Len(t, tags, 4, "2 native bgm + 2 wiki tags; spoiler filtered")
	t0 := tags[0].(map[string]any)
	assert.Equal(t, "百合", t0["name"], "voted bgm row leads the merge")
	assert.EqualValues(t, 30, t0["count"])
	assert.EqualValues(t, srcBangumi, t0["source_id"], "native lane attributes bangumi")
	t1 := tags[1].(map[string]any)
	assert.Equal(t, "PC", t1["name"])
	assert.EqualValues(t, 5, t1["count"])
	assert.EqualValues(t, srcBangumi, t1["source_id"])
	byName := map[string]map[string]any{}
	for _, raw := range tags {
		tg := raw.(map[string]any)
		byName[tg["name"].(string)] = tg
	}
	require.Contains(t, byName, "恋愛(58b)")
	assert.EqualValues(t, srcGalgameWiki, byName["恋愛(58b)"]["source_id"], "user-curated edge → galgame_wiki")
	_, hasCount := byName["恋愛(58b)"]["count"]
	assert.False(t, hasCount, "a wiki tag has no votes → count omitted")
	require.Contains(t, byName, "泣きゲー(58b)")
	assert.EqualValues(t, srcVNDB, byName["泣きゲー(58b)"]["source_id"], "vndb-synced edge → vndb")
	assert.NotContains(t, byName, "ネタバレ(58b)", "a severe-spoiler tag never surfaces by default")

	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(bodyless.ID))
	require.Equal(t, 200, code)
	tags = body["data"].(map[string]any)["tags"].([]any)
	require.Len(t, tags, 4)
	b0 := tags[0].(map[string]any)
	assert.Equal(t, "百合", b0["name"])
	assert.EqualValues(t, 30, b0["count"], "folksonomy vote count surfaces")
	assert.EqualValues(t, srcBangumi, b0["source_id"])
	assert.Equal(t, "ADV", tags[1].(map[string]any)["name"], "count tie broken by name")
	assert.Equal(t, "PC", tags[2].(map[string]any)["name"])
	b3 := tags[3].(map[string]any)
	assert.Equal(t, "拔作", b3["name"])
	assert.EqualValues(t, 1, b3["count"], "count=1 rows stored and served (store-all)")

	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(nativeOnly.ID))
	require.Equal(t, 200, code)
	nTags := body["data"].(map[string]any)["tags"].([]any)
	require.Len(t, nTags, 1, "T2: the claimed work's native bgm row surfaces (was [] under strict XOR)")
	n0 := nTags[0].(map[string]any)
	assert.Equal(t, "百合", n0["name"])
	assert.EqualValues(t, 99, n0["count"])
	assert.EqualValues(t, srcBangumi, n0["source_id"])

	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(empty.ID))
	require.Equal(t, 200, code)
	assert.Empty(t, body["data"].(map[string]any)["tags"].([]any))
}

func TestWorkTagCanonicalOverlay(t *testing.T) {
	db := openCatalogTestDB(t)
	ensureGalgameStub(t, db)
	for _, tbl := range []string{"catalog_tag_source_map", "catalog_tag", "catalog_work_tag", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	var srcVNDB, srcDlsite, srcGalgameWiki int16
	db.Raw("SELECT id FROM catalog_source WHERE key='vndb'").Scan(&srcVNDB)
	db.Raw("SELECT id FROM catalog_source WHERE key='dlsite'").Scan(&srcDlsite)
	db.Raw("SELECT id FROM catalog_source WHERE key IN ('curated','galgame_wiki')").Scan(&srcGalgameWiki)

	tContent := model.CatalogTag{Name: "泣きゲー(74)", Tier: model.TagTierCore, Kind: model.TagKindContent}
	require.NoError(t, db.Create(&tContent).Error)
	tMeta := model.CatalogTag{Name: "像素(74)", Tier: model.TagTierCore, Kind: model.TagKindMeta}
	require.NoError(t, db.Create(&tMeta).Error)
	require.NoError(t, db.Create(&model.CatalogTagSourceMap{SourceID: srcVNDB, SourceName: "泣きゲー(74)", TagID: tContent.ID}).Error)
	require.NoError(t, db.Create(&model.CatalogTagSourceMap{SourceID: srcDlsite, SourceName: "像素(74)", TagID: tMeta.ID}).Error)

	claimed := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "主張", Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(9001)}
	require.NoError(t, db.Create(&claimed).Error)
	require.NoError(t, db.Create(&model.CatalogWorkTag{
		WorkID: claimed.ID, Name: "泣きゲー(74)", SourceID: srcVNDB}).Error)
	require.NoError(t, db.Create(&model.CatalogWorkTag{
		WorkID: claimed.ID, Name: "未映射(74)", SourceID: srcGalgameWiki}).Error)

	bodyless := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "無体"}
	require.NoError(t, db.Create(&bodyless).Error)
	require.NoError(t, db.Create(&model.CatalogWorkTag{WorkID: bodyless.ID, Name: "像素(74)", Count: 3, SourceID: srcDlsite}).Error)
	require.NoError(t, db.Create(&model.CatalogWorkTag{WorkID: bodyless.ID, Name: "独有(74)", Count: 2, SourceID: srcDlsite}).Error)

	app := readApp(service.NewReadService(db), nil)

	code, body := getJSON(t, app, "/api/v1/catalog/works/"+itoa(claimed.ID))
	require.Equal(t, 200, code)
	byName := tagsByName(body)
	require.Contains(t, byName, "泣きゲー(74)")
	assert.EqualValues(t, tContent.ID, byName["泣きゲー(74)"]["canonical_id"], "a mapped vndb tag carries canonical_id")
	assert.EqualValues(t, 0, byName["泣きゲー(74)"]["tier"], "core tier surfaces")
	assert.EqualValues(t, 0, byName["泣きゲー(74)"]["kind"], "content kind surfaces")
	assert.EqualValues(t, srcVNDB, byName["泣きゲー(74)"]["source_id"], "overlay never mutates source_id")
	require.Contains(t, byName, "未映射(74)")
	assertNoOverlay(t, byName["未映射(74)"])

	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(bodyless.ID))
	require.Equal(t, 200, code)
	byName = tagsByName(body)
	require.Contains(t, byName, "像素(74)")
	assert.EqualValues(t, tMeta.ID, byName["像素(74)"]["canonical_id"])
	assert.EqualValues(t, 1, byName["像素(74)"]["kind"], "meta kind surfaces")
	require.Contains(t, byName, "独有(74)")
	assertNoOverlay(t, byName["独有(74)"])
}

func tagsByName(body map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, raw := range body["data"].(map[string]any)["tags"].([]any) {
		tg := raw.(map[string]any)
		out[tg["name"].(string)] = tg
	}
	return out
}

func assertNoOverlay(t *testing.T, tg map[string]any) {
	t.Helper()
	for _, k := range []string{"canonical_id", "tier", "kind"} {
		_, has := tg[k]
		assert.Falsef(t, has, "unmapped tag omits %s", k)
	}
}

func ptrI16(v int16) *int16 { return &v }

func ptrI64(v int64) *int64 { return &v }

func strptr(s string) *string { return &s }

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

func TestCharacterDetailTraits(t *testing.T) {
	db := openCatalogTestDB(t)
	for _, tbl := range []string{
		"catalog_character_trait_link", "catalog_character_trait_parent",
		"catalog_character_trait", "catalog_character",
	} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	ch := model.CatalogCharacter{DisplayName: "特性持ち", Lang: "ja"}
	require.NoError(t, db.Create(&ch).Error)
	chBare := model.CatalogCharacter{DisplayName: "特性なし"}
	require.NoError(t, db.Create(&chBare).Error)

	mkTrait := func(tid, gid, name, nameZh string, sexual bool) int64 {
		tr := model.CatalogCharacterTrait{VndbTID: tid, Name: name, NameZh: nameZh, GroupTID: gid, Sexual: sexual, Searchable: true, Applicable: true}
		require.NoError(t, db.Create(&tr).Error)
		return tr.ID
	}
	mkTrait("i1", "", "Hair", "毛发", false)
	mkTrait("i43", "", "Engages in (Sexual)", "", true)
	blond := mkTrait("i10", "i1", "Blond Hair", "金发", false)
	long := mkTrait("i11", "i1", "Long Hair", "长发", false)
	sexualX := mkTrait("i50", "i43", "Sexual X", "", true)
	for _, l := range []model.CatalogCharacterTraitLink{
		{CharacterID: ch.ID, TraitID: blond, SpoilerLevel: 0},
		{CharacterID: ch.ID, TraitID: long, SpoilerLevel: 2},
		{CharacterID: ch.ID, TraitID: sexualX, SpoilerLevel: 0, Lie: true},
	} {
		require.NoError(t, db.Create(&l).Error)
	}

	app := readApp(service.NewReadService(db), nil)

	code, body := getJSON(t, app, "/api/v1/catalog/characters/"+itoa(ch.ID))
	require.Equal(t, 200, code)
	traits := body["data"].(map[string]any)["traits"].([]any)
	require.Len(t, traits, 2, "spoiler_level 2 gated out by default")
	t0 := traits[0].(map[string]any)
	assert.Equal(t, "Blond Hair", t0["name"])
	assert.Equal(t, "i1", t0["group_tid"])
	assert.Equal(t, "Hair", t0["group_name"], "group name resolved via self-join")
	assert.Equal(t, "金发", t0["name_zh"], "the Chinese trait name rides the read face")
	assert.Equal(t, "毛发", t0["group_name_zh"], "the group's Chinese name resolves via the same self-join")
	assert.Nil(t, t0["sexual"], "sexual=false omitted")
	t1 := traits[1].(map[string]any)
	assert.Equal(t, "Sexual X", t1["name"])
	assert.Nil(t, t1["name_zh"], "an unrendered trait omits name_zh instead of echoing English")
	assert.Nil(t, t1["group_name_zh"], "and so does its unrendered group")
	assert.Equal(t, true, t1["sexual"])
	assert.Equal(t, true, t1["lie"], "lie flag rides verbatim")

	code, body = getJSON(t, app, "/api/v1/catalog/characters/"+itoa(ch.ID)+"?spoilers=2")
	require.Equal(t, 200, code)
	traits = body["data"].(map[string]any)["traits"].([]any)
	require.Len(t, traits, 3)
	assert.Equal(t, "Long Hair", traits[1].(map[string]any)["name"])
	assert.EqualValues(t, 2, traits[1].(map[string]any)["spoiler_level"])

	code, body = getJSON(t, app, "/api/v1/catalog/characters/"+itoa(chBare.ID))
	require.Equal(t, 200, code)
	bareTraits, ok := body["data"].(map[string]any)["traits"].([]any)
	require.True(t, ok, "traits present and non-null")
	assert.Empty(t, bareTraits)
}

func TestWorkDetailSeries(t *testing.T) {
	db := openCatalogTestDB(t)
	for _, tbl := range []string{"catalog_series_member", "catalog_series", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	wA := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "系列作一", ContentRating: 0, Status: 0}
	require.NoError(t, db.Create(&wA).Error)
	wB := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "系列作二", ContentRating: 0, Status: 0}
	require.NoError(t, db.Create(&wB).Error)
	wSolo := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "独立作", ContentRating: 0, Status: 0}
	require.NoError(t, db.Create(&wSolo).Error)

	se := model.CatalogSeries{DisplayName: "テスト系列", SourceID: 4, ExternalID: "SRI777"}
	require.NoError(t, db.Create(&se).Error)
	require.NoError(t, db.Create(&model.CatalogSeriesMember{SeriesID: se.ID, WorkID: wA.ID}).Error)
	require.NoError(t, db.Create(&model.CatalogSeriesMember{SeriesID: se.ID, WorkID: wB.ID}).Error)

	app := readApp(service.NewReadService(db), nil)

	code, body := getJSON(t, app, "/api/v1/catalog/works/"+itoa(wA.ID))
	require.Equal(t, 200, code)
	series := body["data"].(map[string]any)["series"].([]any)
	require.Len(t, series, 1)
	s0 := series[0].(map[string]any)
	assert.EqualValues(t, se.ID, s0["id"])
	assert.Equal(t, "テスト系列", s0["name"])
	assert.EqualValues(t, 4, s0["source_id"])
	assert.EqualValues(t, 2, s0["member_count"])

	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(wSolo.ID))
	require.Equal(t, 200, code)
	soloSeries, ok := body["data"].(map[string]any)["series"].([]any)
	require.True(t, ok, "series present and non-null")
	assert.Empty(t, soloSeries)
}

func TestWorkDetailPlatforms(t *testing.T) {
	db := openCatalogTestDB(t)
	for _, tbl := range []string{"catalog_work_platform", "catalog_release", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	w := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "平台持ち", ContentRating: 0, Status: 0}
	require.NoError(t, db.Create(&w).Error)
	wBare := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "平台無し", ContentRating: 0, Status: 0}
	require.NoError(t, db.Create(&wBare).Error)

	require.NoError(t, db.Exec(`INSERT INTO catalog_work_platform (work_id, platform, source_id)
		VALUES (?, 'win', 3), (?, 'and', 3)`, w.ID, w.ID).Error)

	win := "win"
	relFull := model.CatalogRelease{WorkID: w.ID, Kind: model.ReleaseKindDigital, Platform: &win,
		Extra: []byte(`{"platforms": ["win", "and"]}`)}
	require.NoError(t, db.Create(&relFull).Error)
	relBare := model.CatalogRelease{WorkID: wBare.ID, Kind: model.ReleaseKindDigital}
	require.NoError(t, db.Create(&relBare).Error)

	app := readApp(service.NewReadService(db), nil)

	code, body := getJSON(t, app, "/api/v1/catalog/works/"+itoa(w.ID))
	require.Equal(t, 200, code)
	data := body["data"].(map[string]any)
	plats := data["platforms"].([]any)
	require.Len(t, plats, 2)
	p0 := plats[0].(map[string]any)
	assert.Equal(t, "and", p0["platform"], "platform-ascending order")
	assert.EqualValues(t, 3, p0["source_id"])
	assert.Equal(t, "win", plats[1].(map[string]any)["platform"])
	rels := data["releases"].([]any)
	require.Len(t, rels, 1)
	r0 := rels[0].(map[string]any)
	assert.Equal(t, "win", r0["platform"])
	assert.Equal(t, []any{"win", "and"}, r0["platforms"].([]any))

	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(wBare.ID))
	require.Equal(t, 200, code)
	data = body["data"].(map[string]any)
	barePlats, ok := data["platforms"].([]any)
	require.True(t, ok, "platforms present and non-null")
	assert.Empty(t, barePlats)
	bareRel := data["releases"].([]any)[0].(map[string]any)
	_, hasP := bareRel["platform"]
	assert.False(t, hasP, "empty platform omitted")
	_, hasPs := bareRel["platforms"]
	assert.False(t, hasPs, "absent extra.platforms omitted")
}
