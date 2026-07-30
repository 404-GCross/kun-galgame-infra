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

// seedReadFixture wipes the read-relevant tables and inserts one fully-formed
// work: release (dlsite RJTEST anchor) + official/kana titles + circle label
// (attribution edge) + a voice credit (with character) + a scenario credit
// (orphan name, no character). Returns the work id.
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

// TestWorkRefsBlock pins the flat refs projection: EXACT-only, work- and
// release-level flattened into one list, release refs carrying their release
// id; probable refs never cross the S2S face.
func TestWorkRefsBlock(t *testing.T) {
	db := openCatalogTestDB(t)
	db.Raw("SELECT id FROM catalog_role WHERE key='scenario'").Scan(&roleScenario)
	workID := seedReadFixture(t, db) // release dlsite RJTEST exact anchor
	var relID int64
	require.NoError(t, db.Raw("SELECT id FROM catalog_release LIMIT 1").Scan(&relID).Error)

	// A work-level EXACT vndb anchor (must appear), plus two PROBABLE anchors —
	// one work-level, one release-level — that must NOT.
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
	assert.False(t, got[key{source: "erogamespace", ext: "eg-work", level: "work"}], "probable work ref excluded")
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

// TestSearchWorks pins the title-search picker: NFKC-folded substring match over
// title_norm, medium filter, and per-hit claim state + first DLsite anchor.
func TestSearchWorks(t *testing.T) {
	db := openCatalogTestDB(t)
	db.Raw("SELECT id FROM catalog_role WHERE key='scenario'").Scan(&roleScenario)
	workID := seedReadFixture(t, db) // medium=5 (asmr), unclaimed, title "テスト音声", release dlsite RJTEST

	// A second work in a different medium (galgame=1), claimed by a site, sharing
	// the search substring — to prove the medium filter + claim annotation.
	other := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "音声ゲーム", ContentRating: 0, Status: 0,
		Site: strptr("letmoe"), ProductWorkID: ptrI64(42)}
	require.NoError(t, db.Create(&other).Error)
	require.NoError(t, db.Create(&model.CatalogWorkTitle{WorkID: other.ID, Lang: "ja", Title: "音声ゲーム", Kind: model.WorkTitleKindOfficial}).Error)

	app := readApp(service.NewReadService(db), nil)

	// substring "音声" matches both works.
	code, body := getJSON(t, app, "/api/v1/catalog/works/search?q=%E9%9F%B3%E5%A3%B0")
	require.Equal(t, 200, code)
	items := body["data"].(map[string]any)["items"].([]any)
	require.Len(t, items, 2)
	byID := map[int64]map[string]any{}
	for _, it := range items {
		m := it.(map[string]any)
		byID[int64(m["work_id"].(float64))] = m
	}
	// the asmr fixture: unclaimed (no site key) + its dlsite anchor surfaced.
	fx := byID[workID]
	require.NotNil(t, fx)
	assert.Equal(t, "RJTEST", fx["dlsite_id"])
	_, hasSite := fx["site"] // omitempty → absent when unclaimed
	assert.False(t, hasSite, "unclaimed work has no site key")
	// the galgame work: claimed by letmoe, no dlsite anchor.
	oth := byID[other.ID]
	require.NotNil(t, oth)
	assert.Equal(t, "letmoe", oth["site"])
	_, hasDl := oth["dlsite_id"]
	assert.False(t, hasDl, "no dlsite anchor → omitted")

	// medium filter to galgame(1) → only the second work.
	code, body = getJSON(t, app, "/api/v1/catalog/works/search?q=%E9%9F%B3%E5%A3%B0&medium_id=1")
	require.Equal(t, 200, code)
	items = body["data"].(map[string]any)["items"].([]any)
	require.Len(t, items, 1)
	assert.EqualValues(t, other.ID, items[0].(map[string]any)["work_id"])

	// NFKC fold: a fullwidth-digit / mixed-width query still matches; no match → empty.
	code, body = getJSON(t, app, "/api/v1/catalog/works/search?q=%E3%83%86%E3%82%B9%E3%83%88") // テスト
	require.Equal(t, 200, code)
	assert.Len(t, body["data"].(map[string]any)["items"].([]any), 1)
	code, body = getJSON(t, app, "/api/v1/catalog/works/search?q=zzznomatch")
	require.Equal(t, 200, code)
	assert.Len(t, body["data"].(map[string]any)["items"].([]any), 0)
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

// TestWorkByIDAndLabelWorks covers the internal-browser drill-downs: work-by-id
// (same bundle as by-anchor, 404 on miss) and label→works reverse (paginated).
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

	// label → works reverse: the fixture's circle label has exactly this work.
	var labelID int64
	db.Raw("SELECT id FROM catalog_label LIMIT 1").Scan(&labelID)
	code, body = getJSON(t, app, "/api/v1/catalog/labels/"+itoa(labelID)+"/works")
	require.Equal(t, 200, code)
	data := body["data"].(map[string]any)
	assert.EqualValues(t, 1, data["total"])
	assert.EqualValues(t, workID, data["items"].([]any)[0].(map[string]any)["work_id"])

	// A missing label id now 404s (step 20: aligned with names/characters →works,
	// step 19 finding ②; previously this returned 200 + label:null).
	code, _ = getJSON(t, app, "/api/v1/catalog/labels/99999999/works")
	assert.Equal(t, 404, code)
}

// TestStats asserts the dashboard aggregate against the seeded fixture.
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
	// anchor source×tier: the fixture has one dlsite exact anchor.
	anchors := data["anchors_by_source_tier"].([]any)
	require.Len(t, anchors, 1)
	assert.Equal(t, "dlsite", anchors[0].(map[string]any)["source"])
	// one circle attribution edge.
	attrs := data["attributions_by_kind"].([]any)
	require.Len(t, attrs, 1)
	assert.EqualValues(t, model.WorkLabelKindCircle, attrs[0].(map[string]any)["kind"])
}

// TestReadEndpoints_401: the read surface sits under /api/v1/catalog and is
// therefore gated by S2SAuth — no credentials → 401 before any handler.
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

// reverseFixture holds the ids seeded for the entity reverse-lookup tests.
type reverseFixture struct {
	personP, personQ           int64
	nameA, nameB, nameC, nameD int64 // A,B → person P (public); C public, D hidden → person Q
	char1, char2               int64
	work1, work2, work3        int64
}

// seedReverseFixture builds a graph exercising the name→works and
// character→works reverse lookups: a person with two public names, a person
// with one public + one HIDDEN name, two characters, three works, and credits
// wiring a name to several works (multiple roles on one work, VA with a
// character) plus two names voicing the same character on one work.
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

	p := model.CatalogPerson{DisplayName: "人物P"}
	require.NoError(t, db.Create(&p).Error)
	q := model.CatalogPerson{DisplayName: "人物Q"}
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
	credit(f.work1, f.nameA, roleVoiceActor, &f.char1) // A voices char1 on w1
	credit(f.work1, f.nameA, roleScenario, nil)        // …and writes w1 (second role, same work)
	credit(f.work2, f.nameA, roleVoiceActor, &f.char2) // A voices char2 on w2
	credit(f.work3, f.nameB, roleScenario, nil)        // B writes w3
	credit(f.work1, f.nameC, roleVoiceActor, &f.char1) // C also voices char1 on w1
	credit(f.work2, f.nameD, roleVoiceActor, &f.char2) // D (hidden link) voices char2 on w2
	return f
}

// TestNameWorks covers name→works: self-description with public sibling names,
// per-work roles (multiple roles on one work + VA character), pagination, the
// hidden-link doctrine, and 404 on a missing id.
func TestNameWorks(t *testing.T) {
	db := openCatalogTestDB(t)
	db.Raw("SELECT id FROM catalog_role WHERE key='scenario'").Scan(&roleScenario)
	f := seedReverseFixture(t, db)
	app := readApp(service.NewReadService(db), nil)

	// name A: bucketed name, person P, sibling = B (public); works w1 (two roles,
	// VA carries char1) + w2 (VA char2).
	code, body := getJSON(t, app, "/api/v1/catalog/names/"+itoa(f.nameA)+"/works")
	require.Equal(t, 200, code)
	data := body["data"].(map[string]any)
	head := data["name"].(map[string]any)
	assert.EqualValues(t, f.nameA, head["id"])
	assert.Equal(t, "名義A", head["name"].(map[string]any)["ja"])
	assert.EqualValues(t, f.personP, head["person_id"])
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

	// pagination: limit 1 → one item, total still 2.
	code, body = getJSON(t, app, "/api/v1/catalog/names/"+itoa(f.nameA)+"/works?limit=1")
	require.Equal(t, 200, code)
	assert.EqualValues(t, 2, body["data"].(map[string]any)["total"])
	assert.Len(t, body["data"].(map[string]any)["items"].([]any), 1)

	// name C (public) of person Q: its sibling D is HIDDEN → excluded.
	code, body = getJSON(t, app, "/api/v1/catalog/names/"+itoa(f.nameC)+"/works")
	require.Equal(t, 200, code)
	assert.Empty(t, body["data"].(map[string]any)["name"].(map[string]any)["siblings"],
		"a hidden-linked sibling never surfaces")

	// name D itself is hidden-linked → appears independent: no person_id, no siblings.
	code, body = getJSON(t, app, "/api/v1/catalog/names/"+itoa(f.nameD)+"/works")
	require.Equal(t, 200, code)
	dHead := body["data"].(map[string]any)["name"].(map[string]any)
	_, hasPerson := dHead["person_id"]
	assert.False(t, hasPerson, "hidden link → person_id withheld")
	assert.Empty(t, dHead["siblings"])
	assert.EqualValues(t, 1, body["data"].(map[string]any)["total"], "works are still listed")

	// 404 on a missing name id.
	code, _ = getJSON(t, app, "/api/v1/catalog/names/99999999/works")
	assert.Equal(t, 404, code)
}

// TestCharacterWorks covers character→works as the step-46 UNION: a voiced work
// (kind from the roster edge + voiced=true + voice names) and an appearance-only
// work (roster edge, no credit → voiced=false, empty voices), plus 404 on a miss.
func TestCharacterWorks(t *testing.T) {
	db := openCatalogTestDB(t)
	db.Raw("SELECT id FROM catalog_role WHERE key='scenario'").Scan(&roleScenario)
	f := seedReverseFixture(t, db)
	// Roster edges: char1 is main on w1 (already voiced by A and C via credits)
	// and merely appears on w3 (no credit — appearance-only).
	require.NoError(t, db.Create(&model.CatalogWorkCharacter{
		WorkID: f.work1, CharacterID: f.char1, Kind: model.WorkCharacterKindMain, Spoiler: model.SpoilerMild, MatchedBy: "import:test"}).Error)
	require.NoError(t, db.Create(&model.CatalogWorkCharacter{
		WorkID: f.work3, CharacterID: f.char1, Kind: model.WorkCharacterKindAppears, MatchedBy: "import:test"}).Error)
	app := readApp(service.NewReadService(db), nil)

	code, body := getJSON(t, app, "/api/v1/catalog/characters/"+itoa(f.char1)+"/works")
	require.Equal(t, 200, code)
	data := body["data"].(map[string]any)
	assert.EqualValues(t, f.char1, data["character"].(map[string]any)["id"])
	assert.Equal(t, "キャラ1", data["character"].(map[string]any)["name"].(map[string]any)["ja"])
	assert.EqualValues(t, 2, data["total"], "union: w1 (voiced) + w3 (appearance-only)")
	items := data["items"].([]any)
	require.Len(t, items, 2)
	byWork := map[int64]map[string]any{}
	for _, it := range items {
		m := it.(map[string]any)
		byWork[int64(m["work"].(map[string]any)["work_id"].(float64))] = m
	}
	// w1: main roster edge + voiced by both A and C.
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
	// w3: appearance-only — kind=appears, not voiced, empty (non-null) voices.
	w3 := byWork[f.work3]
	require.NotNil(t, w3)
	assert.EqualValues(t, model.WorkCharacterKindAppears, w3["kind"])
	assert.Equal(t, false, w3["voiced"])
	assert.Empty(t, w3["voices"].([]any))

	// 404 on a missing character id.
	code, _ = getJSON(t, app, "/api/v1/catalog/characters/99999999/works")
	assert.Equal(t, 404, code)
}

// TestWorkCharacters pins the step-46 roster projection on the work read-through:
// the roster edge ∪ voice credit merge (main-first ordering, kind from the edge,
// credit-only characters kind=0, multi-VA per character), a roster-only character
// with empty (non-null) va, and an empty roster serialized as [].
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
	// chMain: main roster edge + two VAs (multi-VA), gender + image_hash set.
	chMain := model.CatalogCharacter{DisplayName: "主人公", Lang: "ja", Gender: &female, ImageHash: &hash}
	require.NoError(t, db.Create(&chMain).Error)
	// chAppears: roster edge only (no VA) — roster-only, empty va.
	chAppears := model.CatalogCharacter{DisplayName: "脇役", Lang: "ja"}
	require.NoError(t, db.Create(&chAppears).Error)
	// chCredit: voice credit only, NO roster edge → kind must default to 0.
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
	edge(chMain.ID, model.WorkCharacterKindMain, model.SpoilerSevere) // spoiler surfaces on the projection
	edge(chAppears.ID, model.WorkCharacterKindAppears, model.SpoilerNone)
	vcredit := func(charID, nameID int64) {
		require.NoError(t, db.Create(&model.CatalogCredit{
			WorkID: work, CreditNameID: nameID, RoleID: roleVoiceActor, CharacterID: &charID}).Error)
	}
	vcredit(chMain.ID, va1.ID) // chMain voiced by two names (multi-VA)
	vcredit(chMain.ID, va2.ID)
	vcredit(chCredit.ID, va3.ID) // credit-only character (no edge)

	app := readApp(service.NewReadService(db), nil)

	code, body := getJSON(t, app, "/api/v1/catalog/works/"+itoa(work))
	require.Equal(t, 200, code)
	chars := body["data"].(map[string]any)["characters"].([]any)
	require.Len(t, chars, 3, "union: 2 roster edges + 1 credit-only")

	// Ordering is main-first: chMain(1) → chAppears(3) → chCredit(0=unknown, last).
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

	// An empty roster serializes [] (not null).
	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(empty))
	require.Equal(t, 200, code)
	assert.Empty(t, body["data"].(map[string]any)["characters"].([]any))
}

// TestCharacterDetail covers GET /characters/{id}: identity fields + aliases +
// intros (step 65: per-language merge, lowest source_id wins), and 404 on a
// missing id.
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
	var vndbSrc, bangumiSrc, dlsiteSrc int16
	require.NoError(t, db.Raw(`SELECT id FROM catalog_source WHERE key = 'vndb'`).Scan(&vndbSrc).Error)
	require.NoError(t, db.Raw(`SELECT id FROM catalog_source WHERE key = 'bangumi'`).Scan(&bangumiSrc).Error)
	require.NoError(t, db.Raw(`SELECT id FROM catalog_source WHERE key = 'dlsite'`).Scan(&dlsiteSrc).Error)
	require.NotZero(t, vndbSrc)
	for _, in := range []model.CatalogCharacterIntro{
		{CharacterID: ch.ID, Lang: "en", Intro: "An English intro.", SourceID: vndbSrc},
		{CharacterID: ch.ID, Lang: "ja", Intro: "日本語の紹介。", SourceID: bangumiSrc},
		// Same lang, higher source id → merged away (lowest source wins).
		{CharacterID: ch.ID, Lang: "ja", Intro: "負けるほうの紹介。", SourceID: dlsiteSrc},
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
	require.Len(t, intros, 2, "one element per language after the source merge")
	i0 := intros[0].(map[string]any)
	assert.Equal(t, "en", i0["lang"], "sorted by lang")
	assert.Equal(t, "An English intro.", i0["intro"])
	assert.EqualValues(t, vndbSrc, i0["source_id"])
	i1 := intros[1].(map[string]any)
	assert.Equal(t, "ja", i1["lang"])
	assert.Equal(t, "日本語の紹介。", i1["intro"], "lowest source_id wins the language")
	assert.EqualValues(t, bangumiSrc, i1["source_id"])

	// A character with no intro rows serializes intros [] (not null).
	code, body = getJSON(t, app, "/api/v1/catalog/characters/"+itoa(chBare.ID))
	require.Equal(t, 200, code)
	bare := body["data"].(map[string]any)
	introsBare, ok := bare["intros"].([]any)
	require.True(t, ok, "intros present and non-null")
	assert.Empty(t, introsBare)

	// 404 on a missing character id.
	code, _ = getJSON(t, app, "/api/v1/catalog/characters/99999999")
	assert.Equal(t, 404, code)
}

// galgameStubIDs are the fixture galgame body ids used by the claimed bridge.
var galgameStubIDs = []int64{5001, 5002}

// ensureGalgameStub provisions the galgame body table the claimed-intro bridge
// joins against. In prod/rehearsal galgame + catalog are co-located in
// kun_catalog; the catalog test DB may carry either a full galgame table (when a
// galgame test migrated it) or none. CREATE ... IF NOT EXISTS is a no-op against
// a real table, so the INSERTs always pass user_id (the one NOT-NULL column
// without a default) and cleanup is a targeted DELETE (not a TRUNCATE — the real
// galgame table has child FKs). Idempotent across reruns.
func ensureGalgameStub(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS galgame (
		id bigint PRIMARY KEY,
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
	// The four name columns joined the stub with the A2-R1 title bridge; a
	// database whose stub predates it (CREATE ... IF NOT EXISTS is a no-op) needs
	// them added rather than recreated. No-op against the real table too.
	for _, col := range []string{"name_en_us", "name_ja_jp", "name_zh_cn", "name_zh_tw"} {
		require.NoError(t, db.Exec(
			`ALTER TABLE galgame ADD COLUMN IF NOT EXISTS `+col+` text NOT NULL DEFAULT ''`).Error)
	}
	// content_limit joined the stub with the A2-R5 editorial display axis: every
	// claimed_by projection now LEFT JOINs it, so a stub without the column would
	// fail the query rather than merely miss a field. Nullable with the real
	// schema's default, so an insert that omits it reads 'sfw' either way.
	require.NoError(t, db.Exec(
		`ALTER TABLE galgame ADD COLUMN IF NOT EXISTS content_limit varchar(10) DEFAULT 'sfw'`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS galgame_alias (
		id bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
		galgame_id bigint NOT NULL,
		name text NOT NULL DEFAULT ''
	)`).Error)
	require.NoError(t, db.Exec(`DELETE FROM galgame_alias WHERE galgame_id IN ?`, galgameStubIDs).Error)
	require.NoError(t, db.Exec(`DELETE FROM galgame WHERE id IN ?`, galgameStubIDs).Error)
}

// insertGalgameBody inserts one fixture galgame body (id + claim back-pointer +
// the four intro columns), passing user_id explicitly for the real-schema case.
func insertGalgameBody(t *testing.T, db *gorm.DB, id, catalogWorkID int64, en, ja, zhCN, zhTW string) {
	t.Helper()
	require.NoError(t, db.Exec(`INSERT INTO galgame (id, catalog_work_id, user_id, intro_en_us, intro_ja_jp, intro_zh_cn, intro_zh_tw)
		VALUES (?, ?, 0, ?, ?, ?, ?)`, id, catalogWorkID, en, ja, zhCN, zhTW).Error)
}

// TestWorkIntro pins the step-52 media-aggregation read face, now fed by the
// W1-pre mirror (refs/proj/140): the claimed work's wiki intro columns pivoted to
// language rows under source=galgame_wiki — mirrored by step q, byte-identical to
// what the bridge projected — the native merge (catalog_work_intro, lowest
// source_id wins per language) that now serves claimed and bodyless works alike,
// and []-not-null serialization.
//
// The strict XOR retired with the bridge: a claimed work's OTHER-source intro row
// is no longer shadowed, it is one more source in the merge. Production had no such
// rows (the claimed population's intro parity is exact), so nothing moved on the
// wire there.
func TestWorkIntro(t *testing.T) {
	db := openCatalogTestDB(t)
	ensureGalgameStub(t, db)
	ensureGalgameRatingStub(t, db) // the co-loaded rating/popularity bridges need the meta tables (fresh-DB file order)
	for _, tbl := range []string{"catalog_work_intro", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	// Source ids from the seed: galgame_wiki=12 (bridge provenance), vndb=2, user=1.
	var srcGalgameWiki, srcVNDB, srcUser int16
	db.Raw("SELECT id FROM catalog_source WHERE key='galgame_wiki'").Scan(&srcGalgameWiki)
	db.Raw("SELECT id FROM catalog_source WHERE key='vndb'").Scan(&srcVNDB)
	db.Raw("SELECT id FROM catalog_source WHERE key='user'").Scan(&srcUser)
	require.NotZero(t, srcGalgameWiki, "galgame_wiki source must be seeded")

	// --- CLAIMED work: intro bridged from its galgame body (en + ja set, zh empty).
	claimed := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "主張作品", ContentRating: 0, Status: 0,
		Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(5001)}
	require.NoError(t, db.Create(&claimed).Error)
	insertGalgameBody(t, db, 5001, claimed.ID, "English intro.", "日本語の紹介。", "", "")

	// --- BODYLESS work: native rows; en has TWO sources (user beats vndb), plus ja.
	bodyless := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "無体作品", ContentRating: 0, Status: 0}
	require.NoError(t, db.Create(&bodyless).Error)
	require.NoError(t, db.Create(&model.CatalogWorkIntro{WorkID: bodyless.ID, Lang: "en", Intro: "VNDB english", SourceID: srcVNDB}).Error)
	require.NoError(t, db.Create(&model.CatalogWorkIntro{WorkID: bodyless.ID, Lang: "en", Intro: "User english", SourceID: srcUser}).Error)
	require.NoError(t, db.Create(&model.CatalogWorkIntro{WorkID: bodyless.ID, Lang: "ja", Intro: "VNDB日本語", SourceID: srcVNDB}).Error)

	// --- MERGE work: claimed with EMPTY wiki intros, carrying a native VNDB row.
	// The mirror writes nothing for it (nothing to mirror) and does not touch the
	// foreign source's row, which the one native lane then serves.
	merged := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "空主張", ContentRating: 0, Status: 0,
		Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(5002)}
	require.NoError(t, db.Create(&merged).Error)
	insertGalgameBody(t, db, 5002, merged.ID, "", "", "", "")
	require.NoError(t, db.Create(&model.CatalogWorkIntro{WorkID: merged.ID, Lang: "en", Intro: "VNDB english", SourceID: srcVNDB}).Error)

	mirrorWikiIntoCatalog(t, db, "q")

	app := readApp(service.NewReadService(db), nil)

	// CLAIMED: two elements (en, ja), sorted by lang, source=galgame_wiki.
	code, body := getJSON(t, app, "/api/v1/catalog/works/"+itoa(claimed.ID))
	require.Equal(t, 200, code)
	intro := body["data"].(map[string]any)["intro"].([]any)
	require.Len(t, intro, 2, "en + ja bridged; empty zh columns dropped")
	assert.Equal(t, "en", intro[0].(map[string]any)["lang"])
	assert.Equal(t, "English intro.", intro[0].(map[string]any)["intro"])
	assert.EqualValues(t, srcGalgameWiki, intro[0].(map[string]any)["source_id"], "bridge attributes galgame_wiki")
	assert.Equal(t, "ja", intro[1].(map[string]any)["lang"])
	assert.Equal(t, "日本語の紹介。", intro[1].(map[string]any)["intro"])

	// BODYLESS: en resolves to the user source (lower id beats vndb); ja from vndb.
	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(bodyless.ID))
	require.Equal(t, 200, code)
	intro = body["data"].(map[string]any)["intro"].([]any)
	require.Len(t, intro, 2, "en (one winner) + ja")
	en := intro[0].(map[string]any)
	assert.Equal(t, "en", en["lang"])
	assert.Equal(t, "User english", en["intro"], "lowest source_id wins the language")
	assert.EqualValues(t, srcUser, en["source_id"])
	assert.EqualValues(t, srcVNDB, intro[1].(map[string]any)["source_id"])

	// MERGE: the wiki body had nothing to mirror, so the work reads the one row it
	// does have — its foreign source's, untouched by the mirror.
	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(merged.ID))
	require.Equal(t, 200, code)
	intro = body["data"].(map[string]any)["intro"].([]any)
	require.Len(t, intro, 1, "an empty wiki column writes no row; the native one is not shadowed")
	assert.Equal(t, "VNDB english", intro[0].(map[string]any)["intro"])
	assert.EqualValues(t, srcVNDB, intro[0].(map[string]any)["source_id"])
}

// galgameCoverStubIDs are the fixture galgame body ids used by the claimed
// cover bridge (kept distinct from galgameStubIDs so the two media tests never
// collide on shared galgame ids).
var galgameCoverStubIDs = []int64{6001, 6002}

// ensureGalgameCoverStub provisions the galgame_cover table the claimed-cover
// bridge joins against. In prod/rehearsal galgame_cover lives in kun_catalog
// alongside catalog; the catalog test DB has no such table, so this creates a
// stub (image_hash text — short test hashes — rather than the real char(64);
// the bridge's TrimSpace makes both equivalent). CREATE ... IF NOT EXISTS is a
// no-op against a real table; cleanup is a targeted DELETE. Idempotent.
func ensureGalgameCoverStub(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS galgame_cover (
		galgame_id bigint NOT NULL,
		image_hash text NOT NULL,
		sort_order int NOT NULL DEFAULT 0,
		kind text NOT NULL DEFAULT '',
		portrait_pinned boolean NOT NULL DEFAULT false,
		sexual smallint NOT NULL DEFAULT 0,
		violence smallint NOT NULL DEFAULT 0,
		source text NOT NULL DEFAULT '',
		PRIMARY KEY (galgame_id, image_hash)
	)`).Error)
	require.NoError(t, db.Exec(`DELETE FROM galgame_cover WHERE galgame_id IN ?`, galgameCoverStubIDs).Error)
}

// insertGalgameCover inserts one fixture galgame_cover row.
func insertGalgameCover(t *testing.T, db *gorm.DB, galgameID int64, hash string, sortOrder int, kind string, portrait bool, sexual, violence int16, source string) {
	t.Helper()
	require.NoError(t, db.Exec(`INSERT INTO galgame_cover
		(galgame_id, image_hash, sort_order, kind, portrait_pinned, sexual, violence, source)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		galgameID, hash, sortOrder, kind, portrait, sexual, violence, source).Error)
}

// TestWorkCover pins the step-53 media-aggregation cover read face: the CLAIMED
// bridge (galgame_cover mapped to the unified shape, with the PORTRAIT pin and
// per-row source mapping ""→galgame_wiki / vndb→vndb / upscale→upscale), the
// BODYLESS native read (catalog_work_cover), strict XOR (a claimed work with no
// galgame cover yields [] and never falls back to a shadow native row), and
// []-not-null serialization. Sorted by (sort_order, image_hash).
func TestWorkCover(t *testing.T) {
	db := openCatalogTestDB(t)
	ensureGalgameStub(t, db)       // the intro bridge (also run by loadWorkDetail) needs galgame
	ensureGalgameCoverStub(t, db)  // the cover bridge needs galgame_cover (clears covers for 6001/6002)
	ensureGalgameRatingStub(t, db) // the co-loaded rating/popularity bridges need the meta tables (fresh-DB file order)
	// galgame_cover carries an FK to galgame(id) in the shared test DB, so the
	// bridge galgame ids need a parent body row. Covers cleared above → safe to
	// reset the parents. Empty intros keep the co-loaded intro bridge a no-op.
	require.NoError(t, db.Exec(`DELETE FROM galgame WHERE id IN ?`, galgameCoverStubIDs).Error)
	insertGalgameBody(t, db, 6001, 0, "", "", "", "")
	insertGalgameBody(t, db, 6002, 0, "", "", "", "")
	for _, tbl := range []string{"catalog_work_cover", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	// Source ids from the seed: galgame_wiki=12, vndb=2, upscale=13, user=1.
	var srcGalgameWiki, srcVNDB, srcUpscale, srcUser int16
	db.Raw("SELECT id FROM catalog_source WHERE key='galgame_wiki'").Scan(&srcGalgameWiki)
	db.Raw("SELECT id FROM catalog_source WHERE key='vndb'").Scan(&srcVNDB)
	db.Raw("SELECT id FROM catalog_source WHERE key='upscale'").Scan(&srcUpscale)
	db.Raw("SELECT id FROM catalog_source WHERE key='user'").Scan(&srcUser)
	require.NotZero(t, srcUpscale, "upscale source must be seeded (step 53)")

	// --- CLAIMED work: covers bridged from galgame_cover (galgame_id 6001).
	//   sort_order 0: a landscape vndb cover (source=vndb → vndb)
	//   sort_order 1: the PORTRAIT-pinned upscale cover (source=upscale → upscale)
	//   sort_order 2: a user upload (source='' → galgame_wiki)
	claimed := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "主張作品", ContentRating: 0, Status: 0,
		Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(6001)}
	require.NoError(t, db.Create(&claimed).Error)
	insertGalgameCover(t, db, 6001, "hash_vndb_landscape", 0, "main", false, 1, 0, "vndb")
	insertGalgameCover(t, db, 6001, "hash_upscale_portrait", 1, "", true, 0, 0, "upscale")
	insertGalgameCover(t, db, 6001, "hash_user_extra", 2, "", false, 0, 0, "")

	// --- BODYLESS work: native catalog_work_cover rows.
	bodyless := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "無体作品", ContentRating: 0, Status: 0}
	require.NoError(t, db.Create(&bodyless).Error)
	require.NoError(t, db.Create(&model.CatalogWorkCover{
		WorkID: bodyless.ID, ImageHash: "hash_bodyless_pin", SortOrder: 0, Kind: "pkgfront",
		PortraitPinned: true, Sexual: 2, Violence: 0, SourceID: srcVNDB}).Error)
	require.NoError(t, db.Create(&model.CatalogWorkCover{
		WorkID: bodyless.ID, ImageHash: "hash_bodyless_extra", SortOrder: 1, SourceID: srcUser}).Error)

	// --- XOR work: claimed (galgame_id 6002) but galgame_cover empty; a SHADOW
	// native catalog_work_cover row exists (a prior bodyless state) and must NEVER
	// surface.
	xor := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "空主張", ContentRating: 0, Status: 0,
		Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(6002)}
	require.NoError(t, db.Create(&xor).Error)
	require.NoError(t, db.Create(&model.CatalogWorkCover{
		WorkID: xor.ID, ImageHash: "hash_shadow_must_not_appear", SortOrder: 0, SourceID: srcVNDB}).Error)

	// --- EMPTY work: bodyless, no covers → [].
	empty := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "空作品", ContentRating: 0, Status: 0}
	require.NoError(t, db.Create(&empty).Error)

	app := readApp(service.NewReadService(db), nil)

	// CLAIMED: three covers, sorted by (sort_order, image_hash); source mapped.
	code, body := getJSON(t, app, "/api/v1/catalog/works/"+itoa(claimed.ID))
	require.Equal(t, 200, code)
	covers := body["data"].(map[string]any)["covers"].([]any)
	require.Len(t, covers, 3, "three galgame_cover rows bridged")
	c0 := covers[0].(map[string]any)
	assert.Equal(t, "hash_vndb_landscape", c0["image_hash"])
	assert.EqualValues(t, 0, c0["sort_order"])
	assert.Equal(t, false, c0["portrait_pinned"])
	assert.EqualValues(t, srcVNDB, c0["source_id"], "vndb source mapped")
	assert.EqualValues(t, 1, c0["sexual"])
	c1 := covers[1].(map[string]any)
	assert.Equal(t, "hash_upscale_portrait", c1["image_hash"])
	assert.Equal(t, true, c1["portrait_pinned"], "the portrait pin surfaces")
	assert.EqualValues(t, srcUpscale, c1["source_id"], "upscale source mapped to new seed 13")
	c2 := covers[2].(map[string]any)
	assert.Equal(t, "hash_user_extra", c2["image_hash"])
	assert.EqualValues(t, srcGalgameWiki, c2["source_id"], "empty galgame_cover.source → galgame_wiki")

	// BODYLESS: native rows, sorted; a portrait pin + a plain cover.
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

	// XOR: claimed + empty galgame_cover → [] (the shadow native row is invisible).
	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(xor.ID))
	require.Equal(t, 200, code)
	assert.Empty(t, body["data"].(map[string]any)["covers"].([]any), "strict XOR: no fallback to native rows")

	// EMPTY: [] not null.
	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(empty.ID))
	require.Equal(t, 200, code)
	assert.Empty(t, body["data"].(map[string]any)["covers"].([]any))
}

// galgameScreenshotStubIDs are the fixture galgame body ids used by the claimed
// screenshot bridge (kept distinct from the intro/cover stub ids so the media
// tests never collide on shared galgame ids).
var galgameScreenshotStubIDs = []int64{7001, 7002, 7003, 7004}

// ensureGalgameScreenshotStub provisions the galgame_screenshot table the
// claimed-screenshot bridge joins against. In prod/rehearsal galgame_screenshot
// lives in kun_catalog alongside catalog; the catalog test DB has no such table,
// so this creates a stub (image_hash text — short test hashes — rather than the
// real char(64); the bridge's TrimSpace makes both equivalent). CREATE ... IF
// NOT EXISTS is a no-op against a real table; cleanup is a targeted DELETE.
// Idempotent.
func ensureGalgameScreenshotStub(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS galgame_screenshot (
		galgame_id bigint NOT NULL,
		image_hash text NOT NULL,
		sort_order bigint NOT NULL DEFAULT 0,
		caption text NOT NULL DEFAULT '',
		sexual smallint NOT NULL DEFAULT 0,
		violence smallint NOT NULL DEFAULT 0,
		source text NOT NULL DEFAULT '',
		PRIMARY KEY (galgame_id, image_hash)
	)`).Error)
	require.NoError(t, db.Exec(`DELETE FROM galgame_screenshot WHERE galgame_id IN ?`, galgameScreenshotStubIDs).Error)
}

// insertGalgameScreenshot inserts one fixture galgame_screenshot row.
func insertGalgameScreenshot(t *testing.T, db *gorm.DB, galgameID int64, hash string, sortOrder int, caption string, sexual, violence int16, source string) {
	t.Helper()
	require.NoError(t, db.Exec(`INSERT INTO galgame_screenshot
		(galgame_id, image_hash, sort_order, caption, sexual, violence, source)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		galgameID, hash, sortOrder, caption, sexual, violence, source).Error)
}

// TestWorkScreenshot pins the step-54 + refs/proj/125 screenshot read face: the
// WIKI BRIDGE lane (galgame_screenshot mapped to the unified shape, with caption
// and per-row source mapping ""→galgame_wiki / vndb→vndb, claimed works only),
// the CATALOG-NATIVE lane (catalog_work_screenshot, run for ALL works), their
// UNION on a claimed work (bridged rows lead, native rows follow, each keeping
// its source attribution), and []-not-null serialization. Each lane is sorted by
// (sort_order, image_hash).
//
// The pre-125 "strict XOR" case (claimed + native-only → []) is deliberately
// INVERTED here: under the (facet, source) XOR a claimed work's dlsite rows are
// first-class read-face rows, so they now surface.
//
// The last case pins the refs/proj/128 dedup: while the wiki-retirement rescue's
// materialized rows coexist with the bridge, one screenshot has a row in each
// lane and must be rendered once, with the bridge winning the key.
func TestWorkScreenshot(t *testing.T) {
	db := openCatalogTestDB(t)
	ensureGalgameStub(t, db)           // the intro bridge (also run by loadWorkDetail) needs galgame
	ensureGalgameCoverStub(t, db)      // the cover bridge (also co-loaded) needs galgame_cover
	ensureGalgameScreenshotStub(t, db) // the screenshot bridge needs galgame_screenshot (clears 7001/7002)
	ensureGalgameRatingStub(t, db)     // the co-loaded rating/popularity bridges need the meta tables (fresh-DB file order)
	// galgame_screenshot carries an FK to galgame(id) in the shared test DB, so the
	// bridge galgame ids need a parent body row. Screenshots cleared above → safe to
	// reset the parents. Empty intros keep the co-loaded intro bridge a no-op; no
	// galgame_cover rows for 7001/7002 keep the co-loaded cover bridge a no-op.
	require.NoError(t, db.Exec(`DELETE FROM galgame WHERE id IN ?`, galgameScreenshotStubIDs).Error)
	insertGalgameBody(t, db, 7001, 0, "", "", "", "")
	insertGalgameBody(t, db, 7002, 0, "", "", "", "")
	insertGalgameBody(t, db, 7003, 0, "", "", "", "")
	insertGalgameBody(t, db, 7004, 0, "", "", "", "")
	for _, tbl := range []string{"catalog_work_screenshot", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	// Source ids from the seed: galgame_wiki=12, vndb=2, user=1, dlsite=4.
	var srcGalgameWiki, srcVNDB, srcUser, srcDlsite int16
	db.Raw("SELECT id FROM catalog_source WHERE key='galgame_wiki'").Scan(&srcGalgameWiki)
	db.Raw("SELECT id FROM catalog_source WHERE key='vndb'").Scan(&srcVNDB)
	db.Raw("SELECT id FROM catalog_source WHERE key='user'").Scan(&srcUser)
	db.Raw("SELECT id FROM catalog_source WHERE key='dlsite'").Scan(&srcDlsite)
	require.NotZero(t, srcGalgameWiki, "galgame_wiki source must be seeded")
	require.NotZero(t, srcDlsite, "dlsite source must be seeded")

	// --- CLAIMED work: screenshots bridged from galgame_screenshot (galgame_id 7001).
	//   sort_order 0: a captioned vndb screenshot (source=vndb → vndb, sexual=1)
	//   sort_order 1: an uncaptioned user upload (source='' → galgame_wiki)
	claimed := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "主張作品", ContentRating: 0, Status: 0,
		Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(7001)}
	require.NoError(t, db.Create(&claimed).Error)
	insertGalgameScreenshot(t, db, 7001, "hash_vndb_shot", 0, "オープニング", 1, 0, "vndb")
	insertGalgameScreenshot(t, db, 7001, "hash_user_shot", 1, "", 0, 0, "")

	// --- BODYLESS work: native catalog_work_screenshot rows.
	bodyless := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "無体作品", ContentRating: 0, Status: 0}
	require.NoError(t, db.Create(&bodyless).Error)
	require.NoError(t, db.Create(&model.CatalogWorkScreenshot{
		WorkID: bodyless.ID, ImageHash: "hash_bodyless_shot", SortOrder: 0, Caption: "本体截图",
		Sexual: 2, Violence: 0, SourceID: srcVNDB}).Error)
	require.NoError(t, db.Create(&model.CatalogWorkScreenshot{
		WorkID: bodyless.ID, ImageHash: "hash_bodyless_extra", SortOrder: 1, SourceID: srcUser}).Error)

	// --- NATIVE-ONLY CLAIMED work: claimed (galgame_id 7002) with an empty
	// galgame_screenshot bridge and a native dlsite row (the refs/proj/125
	// backfill's exact target shape). Pre-125 this yielded []; the catalog-native
	// lane now runs for claimed works too, so the row surfaces.
	nativeOnly := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "空主張", ContentRating: 0, Status: 0,
		Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(7002)}
	require.NoError(t, db.Create(&nativeOnly).Error)
	require.NoError(t, db.Create(&model.CatalogWorkScreenshot{
		WorkID: nativeOnly.ID, ImageHash: "hash_claimed_dlsite_only", SortOrder: 0,
		Sexual: 2, SourceID: srcDlsite}).Error)

	// --- UNION work: claimed (galgame_id 7003) with BOTH a bridged wiki
	// screenshot and a native dlsite row. Bridged rows lead, native rows follow;
	// source_id keeps the two lanes attributable.
	union := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "双泳道", ContentRating: 0, Status: 0,
		Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(7003)}
	require.NoError(t, db.Create(&union).Error)
	insertGalgameScreenshot(t, db, 7003, "hash_wiki_bridged", 0, "wiki", 0, 0, "")
	require.NoError(t, db.Create(&model.CatalogWorkScreenshot{
		WorkID: union.ID, ImageHash: "hash_native_dlsite", SortOrder: 0, SourceID: srcDlsite}).Error)

	// --- RESCUED work: claimed (galgame_id 7004) whose wiki screenshot the
	// wiki-retirement rescue has ALREADY materialized into the native table under
	// source_id=galgame_wiki (refs/proj/128 steps b/n). The same picture then has a
	// row in each lane; the read face must show it ONCE, attributed to the bridge.
	// A second, native-only hash proves the dedup is keyed per (work, hash) and
	// does not swallow the rest of the native lane.
	rescued := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "抢救双现", ContentRating: 0, Status: 0,
		Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(7004)}
	require.NoError(t, db.Create(&rescued).Error)
	insertGalgameScreenshot(t, db, 7004, "hash_rescued_shot", 0, "wiki caption", 1, 0, "")
	require.NoError(t, db.Create(&model.CatalogWorkScreenshot{
		WorkID: rescued.ID, ImageHash: "hash_rescued_shot", SortOrder: 0, Caption: "",
		SourceID: srcGalgameWiki}).Error)
	require.NoError(t, db.Create(&model.CatalogWorkScreenshot{
		WorkID: rescued.ID, ImageHash: "hash_rescued_native_only", SortOrder: 1,
		SourceID: srcDlsite}).Error)

	// --- EMPTY work: bodyless, no screenshots → [].
	empty := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "空作品", ContentRating: 0, Status: 0}
	require.NoError(t, db.Create(&empty).Error)

	app := readApp(service.NewReadService(db), nil)

	// CLAIMED: two screenshots, sorted by (sort_order, image_hash); source mapped; caption carried.
	code, body := getJSON(t, app, "/api/v1/catalog/works/"+itoa(claimed.ID))
	require.Equal(t, 200, code)
	shots := body["data"].(map[string]any)["screenshots"].([]any)
	require.Len(t, shots, 2, "two galgame_screenshot rows bridged")
	s0 := shots[0].(map[string]any)
	assert.Equal(t, "hash_vndb_shot", s0["image_hash"])
	assert.EqualValues(t, 0, s0["sort_order"])
	assert.Equal(t, "オープニング", s0["caption"], "caption bridged")
	assert.EqualValues(t, srcVNDB, s0["source_id"], "vndb source mapped")
	assert.EqualValues(t, 1, s0["sexual"])
	s1 := shots[1].(map[string]any)
	assert.Equal(t, "hash_user_shot", s1["image_hash"])
	assert.Equal(t, "", s1["caption"], "empty caption preserved")
	assert.EqualValues(t, srcGalgameWiki, s1["source_id"], "empty galgame_screenshot.source → galgame_wiki")

	// BODYLESS: native rows, sorted; captioned + plain.
	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(bodyless.ID))
	require.Equal(t, 200, code)
	shots = body["data"].(map[string]any)["screenshots"].([]any)
	require.Len(t, shots, 2)
	b0 := shots[0].(map[string]any)
	assert.Equal(t, "hash_bodyless_shot", b0["image_hash"])
	assert.Equal(t, "本体截图", b0["caption"])
	assert.EqualValues(t, 2, b0["sexual"])
	assert.EqualValues(t, srcVNDB, b0["source_id"])
	assert.EqualValues(t, srcUser, shots[1].(map[string]any)["source_id"])

	// CLAIMED, NATIVE-ONLY: the dlsite row surfaces (refs/proj/125 semantic flip).
	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(nativeOnly.ID))
	require.Equal(t, 200, code)
	shots = body["data"].(map[string]any)["screenshots"].([]any)
	require.Len(t, shots, 1, "the catalog-native lane runs for claimed works too")
	n0 := shots[0].(map[string]any)
	assert.Equal(t, "hash_claimed_dlsite_only", n0["image_hash"])
	assert.EqualValues(t, srcDlsite, n0["source_id"], "native row keeps its dlsite attribution")
	assert.EqualValues(t, 2, n0["sexual"])

	// CLAIMED, BOTH LANES: bridge ∪ native, bridged row first, both attributed.
	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(union.ID))
	require.Equal(t, 200, code)
	shots = body["data"].(map[string]any)["screenshots"].([]any)
	require.Len(t, shots, 2, "bridge ∪ native")
	u0 := shots[0].(map[string]any)
	assert.Equal(t, "hash_wiki_bridged", u0["image_hash"], "bridged rows lead")
	assert.EqualValues(t, srcGalgameWiki, u0["source_id"])
	u1 := shots[1].(map[string]any)
	assert.Equal(t, "hash_native_dlsite", u1["image_hash"], "native rows follow")
	assert.EqualValues(t, srcDlsite, u1["source_id"])

	// CLAIMED, RESCUED: the doubled hash collapses to one row, the bridge wins the
	// key (caption + sexual come from the wiki body), and the native-only row still
	// follows.
	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(rescued.ID))
	require.Equal(t, 200, code)
	shots = body["data"].(map[string]any)["screenshots"].([]any)
	require.Len(t, shots, 2, "the doubled (work, image_hash) appears once")
	r0 := shots[0].(map[string]any)
	assert.Equal(t, "hash_rescued_shot", r0["image_hash"])
	assert.Equal(t, "wiki caption", r0["caption"], "the bridge row wins the key")
	assert.EqualValues(t, 1, r0["sexual"], "bridge attributes, not the native row's zero")
	assert.EqualValues(t, srcGalgameWiki, r0["source_id"])
	r1 := shots[1].(map[string]any)
	assert.Equal(t, "hash_rescued_native_only", r1["image_hash"], "the rest of the native lane is untouched")
	assert.EqualValues(t, srcDlsite, r1["source_id"])

	// EMPTY: [] not null.
	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(empty.ID))
	require.Equal(t, 200, code)
	assert.Empty(t, body["data"].(map[string]any)["screenshots"].([]any))
}

// galgameRatingStubIDs are the fixture galgame body ids used by the claimed
// rating bridge (kept distinct from the intro/cover/screenshot stub ids so the
// media tests never collide on shared galgame ids).
var galgameRatingStubIDs = []int64{8001, 8002}

// ensureGalgameRatingStub provisions the four rating/popularity meta tables
// the claimed bridges (steps 58a + 60 + 62) join against: galgame_vndb_meta,
// galgame_bangumi_meta, galgame_dlsite_meta and galgame_eg_meta. In
// prod/rehearsal all live in kun_catalog alongside catalog; the catalog test
// DB has no such tables, so this creates stubs mirroring the real shapes
// (surveyed live: score numeric / rank+total bigint; eg median bigint
// NULLABLE; vndb rating numeric NULLABLE; dlsite star numeric + counters all
// NULLABLE). CREATE ... IF NOT EXISTS is a no-op against a real table; the
// INSERT helpers always pass every NOT-NULL column (bid / eg_game_id /
// vndb_id / workno / nsfw / synced_at) so they also work against the real
// schema; cleanup is a targeted DELETE. Idempotent.
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

// insertDlsiteMeta inserts one fixture galgame_dlsite_meta row. Nil star/rate
// pair = DLsite publishes no rating; nil counters = unpublished (absent ≠ 0).
func insertDlsiteMeta(t *testing.T, db *gorm.DB, galgameID int64, star *float64, rateCount *int, dl, wl *int64, rv *int) {
	t.Helper()
	require.NoError(t, db.Exec(`INSERT INTO galgame_dlsite_meta
		(galgame_id, workno, rate_average_star, rate_count, dl_count, wishlist_count, review_count, synced_at)
		VALUES (?, 'RJ62TEST', ?, ?, ?, ?, ?, now())`, galgameID, star, rateCount, dl, wl, rv).Error)
}

// insertVNDBMeta inserts one fixture galgame_vndb_meta row (rating nil = VNDB
// publishes no rating — zero/below-threshold votes; the value is on the kana
// wire scale, 10-100).
func insertVNDBMeta(t *testing.T, db *gorm.DB, galgameID int64, rating *float64, voteCount int) {
	t.Helper()
	require.NoError(t, db.Exec(`INSERT INTO galgame_vndb_meta
		(galgame_id, vndb_id, rating, vote_count, synced_at)
		VALUES (?, 'v1', ?, ?, now())`, galgameID, rating, voteCount).Error)
}

// insertBangumiMeta inserts one fixture galgame_bangumi_meta row (bid/nsfw are
// bridge-irrelevant fillers passed for the real-schema NOT NULLs).
func insertBangumiMeta(t *testing.T, db *gorm.DB, galgameID int64, score float64, rank, total int) {
	t.Helper()
	require.NoError(t, db.Exec(`INSERT INTO galgame_bangumi_meta
		(galgame_id, bid, score, rank, total, nsfw, synced_at)
		VALUES (?, 0, ?, ?, ?, false, now())`, galgameID, score, rank, total).Error)
}

// insertEGMeta inserts one fixture galgame_eg_meta row (median nil = EG has no
// median for the game).
func insertEGMeta(t *testing.T, db *gorm.DB, galgameID int64, median *int, voteCount int) {
	t.Helper()
	require.NoError(t, db.Exec(`INSERT INTO galgame_eg_meta
		(galgame_id, eg_game_id, median, vote_count, synced_at)
		VALUES (?, 0, ?, ?, now())`, galgameID, median, voteCount).Error)
}

// TestWorkRating pins the step-58a/60 media-aggregation rating read face: the
// CLAIMED bridge (galgame_vndb_meta ∪ galgame_bangumi_meta ∪ galgame_eg_meta
// mapped to the unified shape on SOURCE-NATIVE scales — vndb 1-10 mean without
// rank and the kana 10-100 wire value decoded ÷10, bangumi 0-10 mean with
// rank, erogamespace 0-100 median without), the BODYLESS native read
// (catalog_work_rating), strict XOR (a claimed work with only unscored metas —
// vndb NULL rating / bangumi score 0 / EG NULL median — yields [] and never
// falls back to a shadow native row), and []-not-null serialization. Ordered
// by source_id ascending.
func TestWorkRating(t *testing.T) {
	db := openCatalogTestDB(t)
	ensureGalgameStub(t, db)           // the intro bridge (also run by loadWorkDetail) needs galgame
	ensureGalgameCoverStub(t, db)      // the co-loaded cover bridge needs galgame_cover
	ensureGalgameScreenshotStub(t, db) // the co-loaded screenshot bridge needs galgame_screenshot
	ensureGalgameRatingStub(t, db)     // the rating bridge needs the two meta tables (clears 8001/8002)
	// The meta tables carry FKs to galgame(id) in the shared test DB, so the
	// bridge galgame ids need a parent body row. Metas cleared above → safe to
	// reset the parents. Empty intros / no cover / no screenshot rows keep the
	// co-loaded sibling bridges no-ops.
	require.NoError(t, db.Exec(`DELETE FROM galgame WHERE id IN ?`, galgameRatingStubIDs).Error)
	insertGalgameBody(t, db, 8001, 0, "", "", "", "")
	insertGalgameBody(t, db, 8002, 0, "", "", "", "")
	for _, tbl := range []string{"catalog_work_rating", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	// Source ids from the seed: vndb=2, bangumi=3, dlsite=4, erogamespace=5.
	var srcVNDB, srcBangumi, srcDlsite, srcEG int16
	db.Raw("SELECT id FROM catalog_source WHERE key='vndb'").Scan(&srcVNDB)
	db.Raw("SELECT id FROM catalog_source WHERE key='bangumi'").Scan(&srcBangumi)
	db.Raw("SELECT id FROM catalog_source WHERE key='dlsite'").Scan(&srcDlsite)
	db.Raw("SELECT id FROM catalog_source WHERE key='erogamespace'").Scan(&srcEG)
	require.NotZero(t, srcVNDB, "vndb source must be seeded")
	require.NotZero(t, srcBangumi, "bangumi source must be seeded")
	require.NotZero(t, srcDlsite, "dlsite source must be seeded")
	require.NotZero(t, srcEG, "erogamespace source must be seeded")

	// --- CLAIMED work: all four metas scored (galgame_id 8001) → four bridged
	// rows. The vndb fixture stores the kana wire value 84.5 (displayed 8.45);
	// the dlsite fixture the displayed 0-5 star average (4.36).
	claimed := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "主張作品", ContentRating: 0, Status: 0,
		Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(8001)}
	require.NoError(t, db.Create(&claimed).Error)
	wire := 84.5
	insertVNDBMeta(t, db, 8001, &wire, 456)
	insertBangumiMeta(t, db, 8001, 7.6, 1234, 890)
	star := 4.36
	rc := 120
	dl64, wl64 := int64(2000), int64(300)
	rv := 12
	insertDlsiteMeta(t, db, 8001, &star, &rc, &dl64, &wl64, &rv)
	median := 78
	insertEGMeta(t, db, 8001, &median, 321)

	// --- BODYLESS work: native catalog_work_rating rows (backfill provenance).
	bodyless := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "無体作品", ContentRating: 0, Status: 0}
	require.NoError(t, db.Create(&bodyless).Error)
	bgmRank := 5000
	require.NoError(t, db.Create(&model.CatalogWorkRating{
		WorkID: bodyless.ID, SourceID: srcBangumi, Score: 6.3, VoteCount: 150, Rank: &bgmRank}).Error)
	require.NoError(t, db.Create(&model.CatalogWorkRating{
		WorkID: bodyless.ID, SourceID: srcEG, Score: 82, VoteCount: 40}).Error)

	// --- UNSCORED work: claimed (galgame_id 8002) with only UNSCORED metas (vndb
	// rating NULL = no public rating, bangumi score 0 = unrated, dlsite star
	// NULL = below the rating threshold, EG median NULL) — all filtered, so the
	// mirror writes nothing — plus a native bangumi row the importer owns, which
	// the one native lane serves (the strict XOR that hid it retired with the
	// bridge).
	xor := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "空主張", ContentRating: 0, Status: 0,
		Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(8002)}
	require.NoError(t, db.Create(&xor).Error)
	insertVNDBMeta(t, db, 8002, nil, 3)
	insertBangumiMeta(t, db, 8002, 0, 0, 0)
	xorWl := int64(9)
	insertDlsiteMeta(t, db, 8002, nil, nil, nil, &xorWl, nil)
	insertEGMeta(t, db, 8002, nil, 3)
	require.NoError(t, db.Create(&model.CatalogWorkRating{
		WorkID: xor.ID, SourceID: srcBangumi, Score: 9.9, VoteCount: 1}).Error)

	// --- EMPTY work: bodyless, no ratings → [].
	empty := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "空作品", ContentRating: 0, Status: 0}
	require.NoError(t, db.Create(&empty).Error)

	mirrorWikiIntoCatalog(t, db, "s,t")

	app := readApp(service.NewReadService(db), nil)

	// CLAIMED: four ratings, source_id ascending, native scales, rank bgm-only.
	code, body := getJSON(t, app, "/api/v1/catalog/works/"+itoa(claimed.ID))
	require.Equal(t, 200, code)
	ratings := body["data"].(map[string]any)["ratings"].([]any)
	require.Len(t, ratings, 4, "all four metas materialized")
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

	// BODYLESS: native rows, source_id ascending; rank present only where set.
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

	// UNSCORED: the metas contributed nothing, so what is left is the importer's own
	// bangumi row — one lane, no shadows.
	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(xor.ID))
	require.Equal(t, 200, code)
	ratings = body["data"].(map[string]any)["ratings"].([]any)
	require.Len(t, ratings, 1, "an unscored meta writes no row; the native one is not shadowed")
	assert.EqualValues(t, srcBangumi, ratings[0].(map[string]any)["source_id"])
	assert.EqualValues(t, 9.9, ratings[0].(map[string]any)["score"])

	// EMPTY: [] not null.
	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(empty.ID))
	require.Equal(t, 200, code)
	assert.Empty(t, body["data"].(map[string]any)["ratings"].([]any))
}

// TestWorkPopularity pins the popularity read face (step 62; per-source XOR
// since T2b, refs/proj/102): the CLAIMED dlsite bridge (galgame_dlsite_meta
// counter columns pivoted to metric rows — a NULL counter contributes NO row,
// a published 0 does) UNIONED with the claimed work's native NON-dlsite rows
// (the bgm shelves), the BODYLESS native read (catalog_work_popularity,
// (source_id, metric) ascending), the per-source XOR (a claimed work's
// native dlsite rows NEVER surface — dlsite is bridge-exclusive), and
// []-not-null serialization.
func TestWorkPopularity(t *testing.T) {
	db := openCatalogTestDB(t)
	ensureGalgameStub(t, db)           // the co-loaded intro bridge needs galgame
	ensureGalgameCoverStub(t, db)      // the co-loaded cover bridge needs galgame_cover
	ensureGalgameScreenshotStub(t, db) // the co-loaded screenshot bridge needs galgame_screenshot
	ensureGalgameRatingStub(t, db)     // the popularity bridge needs galgame_dlsite_meta (clears 8001/8002)
	// The meta tables carry FKs to galgame(id) in the shared test DB, so the
	// bridge galgame ids need a parent body row. Metas cleared above → safe to
	// reset the parents. Empty sibling bridges stay no-ops.
	require.NoError(t, db.Exec(`DELETE FROM galgame WHERE id IN ?`, galgameRatingStubIDs).Error)
	insertGalgameBody(t, db, 8001, 0, "", "", "", "")
	insertGalgameBody(t, db, 8002, 0, "", "", "", "")
	for _, tbl := range []string{"catalog_work_popularity", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	var srcDlsite, srcBgm int16
	db.Raw("SELECT id FROM catalog_source WHERE key='dlsite'").Scan(&srcDlsite)
	require.NotZero(t, srcDlsite, "dlsite source must be seeded")
	db.Raw("SELECT id FROM catalog_source WHERE key='bangumi'").Scan(&srcBgm)
	require.NotZero(t, srcBgm, "bangumi source must be seeded")

	// --- CLAIMED work (galgame_id 8001): full counter trio published (dl 2000 /
	// wishlist 300 / reviews 0 — a published 0 IS a row) → three bridged rows.
	claimed := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "主張作品", ContentRating: 0, Status: 0,
		Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(8001)}
	require.NoError(t, db.Create(&claimed).Error)
	dl64, wl64 := int64(2000), int64(300)
	rv0 := 0
	insertDlsiteMeta(t, db, 8001, nil, nil, &dl64, &wl64, &rv0)
	// T2b: a native bgm shelf row on the claimed work surfaces, and a STALE native
	// dlsite row (777) is corrected to the meta's value by the adoption step rather
	// than shadowed behind a bridge.
	require.NoError(t, db.Create(&model.CatalogWorkPopularity{
		WorkID: claimed.ID, SourceID: srcBgm, Metric: model.PopularityMetricBgmWish, Value: 42}).Error)
	require.NoError(t, db.Create(&model.CatalogWorkPopularity{
		WorkID: claimed.ID, SourceID: srcDlsite, Metric: model.PopularityMetricDownloads, Value: 777}).Error)

	// --- BODYLESS work: native catalog_work_popularity rows.
	bodyless := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "無体作品", ContentRating: 0, Status: 0}
	require.NoError(t, db.Create(&bodyless).Error)
	require.NoError(t, db.Create(&model.CatalogWorkPopularity{
		WorkID: bodyless.ID, SourceID: srcDlsite, Metric: model.PopularityMetricReviews, Value: 7}).Error)
	require.NoError(t, db.Create(&model.CatalogWorkPopularity{
		WorkID: bodyless.ID, SourceID: srcDlsite, Metric: model.PopularityMetricDownloads, Value: 4500}).Error)

	// --- ADOPTED-ROW work: claimed (galgame_id 8002) with NO dlsite meta at all,
	// carrying a native dlsite row the importer owns. The adoption step has no
	// delete arm — these rows belong to the importer now — so the row stays and the
	// one native lane serves it.
	xor := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "空主張", ContentRating: 0, Status: 0,
		Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(8002)}
	require.NoError(t, db.Create(&xor).Error)
	require.NoError(t, db.Create(&model.CatalogWorkPopularity{
		WorkID: xor.ID, SourceID: srcDlsite, Metric: model.PopularityMetricDownloads, Value: 99}).Error)

	// --- EMPTY work: bodyless, no counters → [].
	empty := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "空作品", ContentRating: 0, Status: 0}
	require.NoError(t, db.Create(&empty).Error)

	mirrorWikiIntoCatalog(t, db, "t")

	app := readApp(service.NewReadService(db), nil)

	// CLAIMED: the native bgm shelf + the three adopted dlsite counters,
	// (source_id, metric) ascending (bgm sorts before dlsite).
	code, body := getJSON(t, app, "/api/v1/catalog/works/"+itoa(claimed.ID))
	require.Equal(t, 200, code)
	pop := body["data"].(map[string]any)["popularity"].([]any)
	require.Len(t, pop, 4, "native bgm shelf + three adopted dlsite counters")
	p0 := pop[0].(map[string]any)
	assert.EqualValues(t, srcBgm, p0["source_id"], "bgm sorts before dlsite")
	assert.EqualValues(t, model.PopularityMetricBgmWish, p0["metric"])
	assert.EqualValues(t, 42, p0["value"], "the claimed work's native bgm row surfaces")
	p1 := pop[1].(map[string]any)
	assert.EqualValues(t, srcDlsite, p1["source_id"])
	assert.EqualValues(t, 0, p1["metric"], "downloads first (metric ascending)")
	assert.EqualValues(t, 2000, p1["value"], "the adoption corrected the stale native row (777) to the meta's value")
	p2 := pop[2].(map[string]any)
	assert.EqualValues(t, 1, p2["metric"])
	assert.EqualValues(t, 300, p2["value"])
	p3 := pop[3].(map[string]any)
	assert.EqualValues(t, 2, p3["metric"])
	assert.EqualValues(t, 0, p3["value"], "a published 0 is a real row")

	// BODYLESS: native rows, (source_id, metric) ascending.
	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(bodyless.ID))
	require.Equal(t, 200, code)
	pop = body["data"].(map[string]any)["popularity"].([]any)
	require.Len(t, pop, 2)
	assert.EqualValues(t, 0, pop[0].(map[string]any)["metric"], "downloads before reviews")
	assert.EqualValues(t, 4500, pop[0].(map[string]any)["value"])
	assert.EqualValues(t, 2, pop[1].(map[string]any)["metric"])
	assert.EqualValues(t, 7, pop[1].(map[string]any)["value"])

	// ADOPTED-ROW: no meta to adopt, and the importer's row is kept and served.
	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(xor.ID))
	require.Equal(t, 200, code)
	pop = body["data"].(map[string]any)["popularity"].([]any)
	require.Len(t, pop, 1, "the adoption keeps what it did not write; nothing is shadowed")
	assert.EqualValues(t, 99, pop[0].(map[string]any)["value"])

	// EMPTY: [] not null.
	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(empty.ID))
	require.Equal(t, 200, code)
	assert.Empty(t, body["data"].(map[string]any)["popularity"].([]any))
}

// galgameTagStubGalgameIDs are the fixture galgame body ids used by the claimed
// tag bridge; galgameTagStubTagIDs the fixture galgame_tag ids (both kept
// distinct from the other media stubs' ids so the tests never collide).
var (
	galgameTagStubGalgameIDs = []int64{9001, 9002}
	// 9104 is the A2-1e safety-axis suite's fourth fixture tag: it was missing
	// from this list, so a SECOND run against the same database hit the real
	// UNIQUE(id) — the stub cleanup must cover every fixture id any suite writes.
	galgameTagStubTagIDs = []int64{9101, 9102, 9103, 9104}
)

// ensureGalgameTagStub provisions the two tag-layer tables the claimed-tag
// bridge (step 58b) joins against: galgame_tag and galgame_tag_relation. In
// prod/rehearsal both live in kun_catalog alongside catalog; the catalog test
// DB has no such tables, so this creates stubs mirroring the real shapes
// (surveyed live: tag name text UNIQUE / category NOT NULL; relation
// (galgame_id, tag_id) PK with nullable spoiler_level + source defaults).
// CREATE ... IF NOT EXISTS is a no-op against a real table; the INSERT helpers
// always pass category (the real schema's defaultless NOT NULL) and the
// fixture tag names are 58b-suffixed so the real UNIQUE(name) can never
// collide; cleanup is a targeted DELETE (relations first — the real relation
// FKs both parents). Idempotent.
func ensureGalgameTagStub(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec(`CREATE TABLE IF NOT EXISTS galgame_tag (
		id bigint PRIMARY KEY,
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

// insertGalgameTag inserts one fixture galgame_tag definition (category passed
// for the real-schema defaultless NOT NULL).
func insertGalgameTag(t *testing.T, db *gorm.DB, id int64, name string) {
	t.Helper()
	require.NoError(t, db.Exec(`INSERT INTO galgame_tag (id, name, category) VALUES (?, ?, 'content')`, id, name).Error)
}

// insertGalgameTagRelation attaches a tag to a galgame body with the given
// spoiler level and provenance source (”=user-curated, 'vndb'=synced).
func insertGalgameTagRelation(t *testing.T, db *gorm.DB, galgameID, tagID int64, spoiler int, source string) {
	t.Helper()
	require.NoError(t, db.Exec(`INSERT INTO galgame_tag_relation (galgame_id, tag_id, spoiler_level, source)
		VALUES (?, ?, ?, ?)`, galgameID, tagID, spoiler, source).Error)
}

// TestWorkTag pins the step-58b + T2 media-aggregation tag read face under the
// (facet, source) XOR: a CLAIMED work merges its WIKI bridge lane
// (galgame_tag_relation ⋈ galgame_tag — localized names, NON-SPOILER only, no
// votes) with its CATALOG-native lane (catalog_work_tag bgm rows), re-sorted
// (count DESC, name ASC) so voted bgm rows lead. Covers: ① claimed bridge ∪
// native merge (two-source attribution, contract order); ② a claimed work with
// NO bridgeable wiki tag still surfaces its native bgm rows (the pre-T2 [] is
// now filled — the flip); ③ bodyless native read unchanged; ④ the spoiler
// filter still holds; and []-not-null serialization.
func TestWorkTag(t *testing.T) {
	db := openCatalogTestDB(t)
	ensureGalgameStub(t, db)           // the intro bridge (also run by loadWorkDetail) needs galgame
	ensureGalgameCoverStub(t, db)      // the co-loaded cover bridge needs galgame_cover
	ensureGalgameScreenshotStub(t, db) // the co-loaded screenshot bridge needs galgame_screenshot
	ensureGalgameRatingStub(t, db)     // the co-loaded rating bridge needs the two meta tables
	ensureGalgameTagStub(t, db)        // the tag bridge needs the tag layer (clears 9001/9002 + fixture tags)
	// The relation FKs galgame(id) in the real-schema case, so the bridge
	// galgame ids need a parent body row. Relations cleared above → safe to
	// reset the parents. Empty intros / no other media rows keep the co-loaded
	// sibling bridges no-ops.
	require.NoError(t, db.Exec(`DELETE FROM galgame WHERE id IN ?`, galgameTagStubGalgameIDs).Error)
	insertGalgameBody(t, db, 9001, 0, "", "", "", "")
	insertGalgameBody(t, db, 9002, 0, "", "", "", "")
	for _, tbl := range []string{"catalog_work_tag", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	// Source ids from the seed: galgame_wiki=12 (bridge fallback), vndb=2, bangumi=3.
	var srcGalgameWiki, srcVNDB, srcBangumi int16
	db.Raw("SELECT id FROM catalog_source WHERE key='galgame_wiki'").Scan(&srcGalgameWiki)
	db.Raw("SELECT id FROM catalog_source WHERE key='vndb'").Scan(&srcVNDB)
	db.Raw("SELECT id FROM catalog_source WHERE key='bangumi'").Scan(&srcBangumi)
	require.NotZero(t, srcGalgameWiki, "galgame_wiki source must be seeded")
	require.NotZero(t, srcVNDB, "vndb source must be seeded")
	require.NotZero(t, srcBangumi, "bangumi source must be seeded")

	// Fixture tag layer: a user-curated tag, a vndb-synced tag, and a severe-
	// spoiler tag that must NEVER cross the bridge (the unified shape carries no
	// spoiler flag, so only the non-spoiler subset is honest).
	insertGalgameTag(t, db, 9101, "恋愛(58b)")
	insertGalgameTag(t, db, 9102, "泣きゲー(58b)")
	insertGalgameTag(t, db, 9103, "ネタバレ(58b)")

	// --- CLAIMED work (galgame_id 9001): the (facet, source) merge showcase —
	// two bridgeable wiki tags + one spoiler tag (filtered) + native bgm rows
	// (the T2 catalog-native lane; voted, so they lead the merge).
	claimed := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "主張作品", ContentRating: 0, Status: 0,
		Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(9001)}
	require.NoError(t, db.Create(&claimed).Error)
	insertGalgameTagRelation(t, db, 9001, 9101, 0, "")     // user-curated → galgame_wiki
	insertGalgameTagRelation(t, db, 9001, 9102, 0, "vndb") // synced → vndb
	insertGalgameTagRelation(t, db, 9001, 9103, 2, "vndb") // severe spoiler → filtered
	for _, row := range []model.CatalogWorkTag{
		{WorkID: claimed.ID, Name: "百合", Count: 30, SourceID: srcBangumi},
		{WorkID: claimed.ID, Name: "PC", Count: 5, SourceID: srcBangumi},
	} {
		require.NoError(t, db.Create(&row).Error)
	}

	// --- BODYLESS work: native folksonomy rows (count DESC, name tie-break).
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

	// --- CLAIMED native-only work (galgame_id 9002): its ONLY wiki tag is a
	// spoiler (filtered → bridge empty), but it carries a native bgm row. Pre-T2
	// the strict XOR hid that row and served []; under the (facet, source) XOR the
	// catalog-native lane is legitimate for a claimed work, so the row surfaces.
	nativeOnly := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "原生主張", ContentRating: 0, Status: 0,
		Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(9002)}
	require.NoError(t, db.Create(&nativeOnly).Error)
	insertGalgameTagRelation(t, db, 9002, 9103, 2, "vndb")
	require.NoError(t, db.Create(&model.CatalogWorkTag{
		WorkID: nativeOnly.ID, Name: "百合", Count: 99, SourceID: srcBangumi}).Error)

	// --- EMPTY work: bodyless, no tags → [].
	empty := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "空作品", ContentRating: 0, Status: 0}
	require.NoError(t, db.Create(&empty).Error)

	mirrorWikiIntoCatalog(t, db, "r")

	app := readApp(service.NewReadService(db), nil)

	// CLAIMED ①: bridge ∪ native merge. The voted bgm rows (百合 30, PC 5) lead by
	// (count DESC, name ASC); the count-0 bridged wiki tags trail. Spoiler tag
	// filtered. Two-source attribution across the merge (bangumi native +
	// galgame_wiki/vndb bridge).
	code, body := getJSON(t, app, "/api/v1/catalog/works/"+itoa(claimed.ID))
	require.Equal(t, 200, code)
	tags := body["data"].(map[string]any)["tags"].([]any)
	require.Len(t, tags, 4, "2 native bgm + 2 bridged wiki tags; spoiler filtered")
	// Contract order: voted bgm rows first (positional — count breaks the tie).
	t0 := tags[0].(map[string]any)
	assert.Equal(t, "百合", t0["name"], "voted bgm row leads the merge")
	assert.EqualValues(t, 30, t0["count"])
	assert.EqualValues(t, srcBangumi, t0["source_id"], "native lane attributes bangumi")
	t1 := tags[1].(map[string]any)
	assert.Equal(t, "PC", t1["name"])
	assert.EqualValues(t, 5, t1["count"])
	assert.EqualValues(t, srcBangumi, t1["source_id"])
	// Bridged wiki tags trail (count omitted, per-relation source). Matched by
	// name — CJK order among the count-0 rows is not asserted positionally.
	byName := map[string]map[string]any{}
	for _, raw := range tags {
		tg := raw.(map[string]any)
		byName[tg["name"].(string)] = tg
	}
	require.Contains(t, byName, "恋愛(58b)")
	assert.EqualValues(t, srcGalgameWiki, byName["恋愛(58b)"]["source_id"], "user-curated relation → galgame_wiki")
	_, hasCount := byName["恋愛(58b)"]["count"]
	assert.False(t, hasCount, "bridged wiki tag has no votes → count omitted")
	require.Contains(t, byName, "泣きゲー(58b)")
	assert.EqualValues(t, srcVNDB, byName["泣きゲー(58b)"]["source_id"], "vndb-synced relation → vndb")
	assert.NotContains(t, byName, "ネタバレ(58b)", "spoiler tag never crosses the bridge")

	// BODYLESS: native rows, (count DESC, name) — high-vote first, ASCII tie
	// broken by name; counts present.
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

	// CLAIMED native-only ②: bridge empty (only a spoiler tag), but the native bgm
	// row now surfaces — the pre-T2 [] is filled (the (facet, source) flip).
	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(nativeOnly.ID))
	require.Equal(t, 200, code)
	nTags := body["data"].(map[string]any)["tags"].([]any)
	require.Len(t, nTags, 1, "T2: the claimed work's native bgm row surfaces (was [] under strict XOR)")
	n0 := nTags[0].(map[string]any)
	assert.Equal(t, "百合", n0["name"])
	assert.EqualValues(t, 99, n0["count"])
	assert.EqualValues(t, srcBangumi, n0["source_id"])

	// EMPTY: [] not null.
	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(empty.ID))
	require.Equal(t, 200, code)
	assert.Empty(t, body["data"].(map[string]any)["tags"].([]any))
}

// TestWorkTagCanonicalOverlay pins the step-74 additive canonical overlay on
// the tag read face: a tag whose (source_id, name) is mapped into
// catalog_tag_source_map carries canonical_id/tier/kind; an UNMAPPED tag omits
// all three. It covers both provenances — a MIRRORED wiki edge (source_id=vndb,
// name=galgame_tag.name, which hits the same map key a bodyless vndb tag would,
// and that key-compatibility is exactly what let the flip delete the bridge) and
// an importer's own row — and asserts the overlay never mutates name/source_id.
func TestWorkTagCanonicalOverlay(t *testing.T) {
	db := openCatalogTestDB(t)
	ensureGalgameStub(t, db)
	ensureGalgameCoverStub(t, db)
	ensureGalgameScreenshotStub(t, db)
	ensureGalgameRatingStub(t, db)
	ensureGalgameTagStub(t, db)
	require.NoError(t, db.Exec(`DELETE FROM galgame WHERE id IN ?`, galgameTagStubGalgameIDs).Error)
	insertGalgameBody(t, db, 9001, 0, "", "", "", "")
	for _, tbl := range []string{"catalog_tag_source_map", "catalog_tag", "catalog_work_tag", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	var srcVNDB, srcDlsite, srcGalgameWiki int16
	db.Raw("SELECT id FROM catalog_source WHERE key='vndb'").Scan(&srcVNDB)
	db.Raw("SELECT id FROM catalog_source WHERE key='dlsite'").Scan(&srcDlsite)
	db.Raw("SELECT id FROM catalog_source WHERE key='galgame_wiki'").Scan(&srcGalgameWiki)

	// Canonical vocabulary: one mapped vndb name (content) + one mapped dlsite
	// name (meta). Their map keys are exactly (source_id, source_name).
	tContent := model.CatalogTag{Name: "泣きゲー(74)", Tier: model.TagTierCore, Kind: model.TagKindContent}
	require.NoError(t, db.Create(&tContent).Error)
	tMeta := model.CatalogTag{Name: "像素(74)", Tier: model.TagTierCore, Kind: model.TagKindMeta}
	require.NoError(t, db.Create(&tMeta).Error)
	require.NoError(t, db.Create(&model.CatalogTagSourceMap{SourceID: srcVNDB, SourceName: "泣きゲー(74)", TagID: tContent.ID}).Error)
	require.NoError(t, db.Create(&model.CatalogTagSourceMap{SourceID: srcDlsite, SourceName: "像素(74)", TagID: tMeta.ID}).Error)

	// CLAIMED work: a vndb-synced tag that IS mapped + a user tag (galgame_wiki
	// source) that is NOT (no source-12 key exists).
	insertGalgameTag(t, db, 9101, "泣きゲー(74)")
	insertGalgameTag(t, db, 9102, "未映射(74)")
	claimed := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "主張", Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(9001)}
	require.NoError(t, db.Create(&claimed).Error)
	insertGalgameTagRelation(t, db, 9001, 9101, 0, "vndb") // mapped (source_id=vndb)
	insertGalgameTagRelation(t, db, 9001, 9102, 0, "")     // galgame_wiki → unmapped

	// BODYLESS work: a mapped dlsite tag + an unmapped dlsite tag.
	bodyless := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "無体"}
	require.NoError(t, db.Create(&bodyless).Error)
	require.NoError(t, db.Create(&model.CatalogWorkTag{WorkID: bodyless.ID, Name: "像素(74)", Count: 3, SourceID: srcDlsite}).Error)
	require.NoError(t, db.Create(&model.CatalogWorkTag{WorkID: bodyless.ID, Name: "独有(74)", Count: 2, SourceID: srcDlsite}).Error)

	mirrorWikiIntoCatalog(t, db, "r")

	app := readApp(service.NewReadService(db), nil)

	// CLAIMED: the mirrored vndb tag carries the overlay; the wiki user tag omits it.
	code, body := getJSON(t, app, "/api/v1/catalog/works/"+itoa(claimed.ID))
	require.Equal(t, 200, code)
	byName := tagsByName(body)
	require.Contains(t, byName, "泣きゲー(74)")
	assert.EqualValues(t, tContent.ID, byName["泣きゲー(74)"]["canonical_id"], "mapped vndb tag carries canonical_id")
	assert.EqualValues(t, 0, byName["泣きゲー(74)"]["tier"], "core tier surfaces")
	assert.EqualValues(t, 0, byName["泣きゲー(74)"]["kind"], "content kind surfaces")
	assert.EqualValues(t, srcVNDB, byName["泣きゲー(74)"]["source_id"], "overlay never mutates source_id")
	require.Contains(t, byName, "未映射(74)")
	assertNoOverlay(t, byName["未映射(74)"])

	// BODYLESS: the mapped dlsite tag carries the overlay (kind=meta), unmapped omits.
	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(bodyless.ID))
	require.Equal(t, 200, code)
	byName = tagsByName(body)
	require.Contains(t, byName, "像素(74)")
	assert.EqualValues(t, tMeta.ID, byName["像素(74)"]["canonical_id"])
	assert.EqualValues(t, 1, byName["像素(74)"]["kind"], "meta kind surfaces")
	require.Contains(t, byName, "独有(74)")
	assertNoOverlay(t, byName["独有(74)"])
}

// tagsByName indexes a work read-through's tags[] by name.
func tagsByName(body map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, raw := range body["data"].(map[string]any)["tags"].([]any) {
		tg := raw.(map[string]any)
		out[tg["name"].(string)] = tg
	}
	return out
}

// assertNoOverlay asserts an unmapped tag omits all three overlay fields.
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

// TestCharacterDetailTraits covers the step-93 traits[] read face: the default
// spoiler gate (0 — no leaks unless asked), ?spoilers widening, group-name
// resolution via the vocabulary self-join, sexual/lie flags riding verbatim,
// and `[]` (not null) for a trait-less character.
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

	mkTrait := func(tid, gid, name string, sexual bool) int64 {
		tr := model.CatalogCharacterTrait{VndbTID: tid, Name: name, GroupTID: gid, Sexual: sexual, Searchable: true, Applicable: true}
		require.NoError(t, db.Create(&tr).Error)
		return tr.ID
	}
	mkTrait("i1", "", "Hair", false) // root group
	mkTrait("i43", "", "Engages in (Sexual)", true)
	blond := mkTrait("i10", "i1", "Blond Hair", false)
	long := mkTrait("i11", "i1", "Long Hair", false)
	sexualX := mkTrait("i50", "i43", "Sexual X", true)
	for _, l := range []model.CatalogCharacterTraitLink{
		{CharacterID: ch.ID, TraitID: blond, SpoilerLevel: 0},
		{CharacterID: ch.ID, TraitID: long, SpoilerLevel: 2}, // major spoiler
		{CharacterID: ch.ID, TraitID: sexualX, SpoilerLevel: 0, Lie: true},
	} {
		require.NoError(t, db.Create(&l).Error)
	}

	app := readApp(service.NewReadService(db), nil)

	// Default (?spoilers absent = 0): the spoiler-2 trait must NOT leak.
	code, body := getJSON(t, app, "/api/v1/catalog/characters/"+itoa(ch.ID))
	require.Equal(t, 200, code)
	traits := body["data"].(map[string]any)["traits"].([]any)
	require.Len(t, traits, 2, "spoiler_level 2 gated out by default")
	t0 := traits[0].(map[string]any)
	assert.Equal(t, "Blond Hair", t0["name"])
	assert.Equal(t, "i1", t0["group_tid"])
	assert.Equal(t, "Hair", t0["group_name"], "group name resolved via self-join")
	assert.Nil(t, t0["sexual"], "sexual=false omitted")
	t1 := traits[1].(map[string]any)
	assert.Equal(t, "Sexual X", t1["name"])
	assert.Equal(t, true, t1["sexual"])
	assert.Equal(t, true, t1["lie"], "lie flag rides verbatim")

	// ?spoilers=2 widens to all three, ordered (group_tid, gorder, name).
	code, body = getJSON(t, app, "/api/v1/catalog/characters/"+itoa(ch.ID)+"?spoilers=2")
	require.Equal(t, 200, code)
	traits = body["data"].(map[string]any)["traits"].([]any)
	require.Len(t, traits, 3)
	assert.Equal(t, "Long Hair", traits[1].(map[string]any)["name"])
	assert.EqualValues(t, 2, traits[1].(map[string]any)["spoiler_level"])

	// Trait-less character serializes traits [] (not null).
	code, body = getJSON(t, app, "/api/v1/catalog/characters/"+itoa(chBare.ID))
	require.Equal(t, 200, code)
	bareTraits, ok := body["data"].(map[string]any)["traits"].([]any)
	require.True(t, ok, "traits present and non-null")
	assert.Empty(t, bareTraits)
}

// TestWorkDetailSeries covers the step-94 series[] read face: a work in a
// series carries {id, name, source_id, member_count}; a series-less work
// serializes `[]` (not null).
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

// TestWorkDetailPlatforms covers the step-96 platform read faces: work-level
// platforms[] rows carry {platform, source_id}; a release with a platform
// column + extra.platforms surfaces both the primary code and the full array;
// a platform-less work serializes `[]` (not null) and its bare release omits
// both fields.
func TestWorkDetailPlatforms(t *testing.T) {
	db := openCatalogTestDB(t)
	for _, tbl := range []string{"catalog_work_platform", "catalog_release", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	w := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "平台持ち", ContentRating: 0, Status: 0}
	require.NoError(t, db.Create(&w).Error)
	wBare := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "平台無し", ContentRating: 0, Status: 0}
	require.NoError(t, db.Create(&wBare).Error)

	// Work-level rows (the bgm-lane grain), inserted unordered to pin ordering.
	require.NoError(t, db.Exec(`INSERT INTO catalog_work_platform (work_id, platform, source_id)
		VALUES (?, 'win', 3), (?, 'and', 3)`, w.ID, w.ID).Error)

	// Release-level: one with primary + array (the step-76/96 shape), one bare.
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
