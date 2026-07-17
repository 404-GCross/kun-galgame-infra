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

// TestCharacterDetail covers GET /characters/{id}: identity fields + aliases,
// and 404 on a missing id.
func TestCharacterDetail(t *testing.T) {
	db := openCatalogTestDB(t)
	for _, tbl := range []string{"catalog_character_alias", "catalog_character"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	male := model.GenderMale
	latin := "Shujinkou"
	ch := model.CatalogCharacter{DisplayName: "主人公", Lang: "ja", Latin: &latin, Gender: &male, Description: "テスト説明"}
	require.NoError(t, db.Create(&ch).Error)
	require.NoError(t, db.Create(&model.CatalogCharacterAlias{
		CharacterID: ch.ID, Name: "しゅじんこう", Lang: "ja", Kind: model.AliasKindSpellingVariant}).Error)

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
		intro_en_us text NOT NULL DEFAULT '',
		intro_ja_jp text NOT NULL DEFAULT '',
		intro_zh_cn text NOT NULL DEFAULT '',
		intro_zh_tw text NOT NULL DEFAULT ''
	)`).Error)
	require.NoError(t, db.Exec(`DELETE FROM galgame WHERE id IN ?`, galgameStubIDs).Error)
}

// insertGalgameBody inserts one fixture galgame body (id + claim back-pointer +
// the four intro columns), passing user_id explicitly for the real-schema case.
func insertGalgameBody(t *testing.T, db *gorm.DB, id, catalogWorkID int64, en, ja, zhCN, zhTW string) {
	t.Helper()
	require.NoError(t, db.Exec(`INSERT INTO galgame (id, catalog_work_id, user_id, intro_en_us, intro_ja_jp, intro_zh_cn, intro_zh_tw)
		VALUES (?, ?, 0, ?, ?, ?, ?)`, id, catalogWorkID, en, ja, zhCN, zhTW).Error)
}

// TestWorkIntro pins the step-52 media-aggregation read face: the CLAIMED bridge
// (galgame.intro_* pivoted to language rows, source=galgame_wiki), the BODYLESS
// native merge (catalog_work_intro, lowest source_id wins per language), strict
// XOR (a claimed work with empty galgame intros yields [] and never falls back
// to a shadow native row), and []-not-null serialization.
func TestWorkIntro(t *testing.T) {
	db := openCatalogTestDB(t)
	ensureGalgameStub(t, db)
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

	// --- XOR work: claimed but galgame intros all empty; a SHADOW native row exists
	// (simulating a prior bodyless state) and must NEVER surface.
	xor := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "空主張", ContentRating: 0, Status: 0,
		Site: strptr("galgame_wiki"), ProductWorkID: ptrI64(5002)}
	require.NoError(t, db.Create(&xor).Error)
	insertGalgameBody(t, db, 5002, xor.ID, "", "", "", "")
	require.NoError(t, db.Create(&model.CatalogWorkIntro{WorkID: xor.ID, Lang: "en", Intro: "shadow row must not appear", SourceID: srcVNDB}).Error)

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

	// XOR: claimed + empty galgame intros → [] (the shadow native row is invisible).
	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(xor.ID))
	require.Equal(t, 200, code)
	assert.Empty(t, body["data"].(map[string]any)["intro"].([]any), "strict XOR: no fallback to native rows")
}

func ptrI16(v int16) *int16 { return &v }

func ptrI64(v int64) *int64 { return &v }

func strptr(s string) *string { return &s }

func itoa(v int64) string { return strconv.FormatInt(v, 10) }
