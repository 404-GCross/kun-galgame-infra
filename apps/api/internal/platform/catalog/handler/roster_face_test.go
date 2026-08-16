package handler

import (
	"testing"

	"api/internal/platform/catalog/editspec"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/service"
	"api/internal/platform/editing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func suppressRoster(t *testing.T, db *gorm.DB, workID, characterID int64) {
	t.Helper()
	require.NoError(t, db.Create(&editing.SuppressedRow{
		EntityType: editspec.TypeWork, EntityID: workID, FieldKey: editspec.FieldWorkRoster,
		IdentityKey: editspec.RosterIdentity(characterID),
	}).Error)
}

// TestWorkCharactersExcludesSuppressedRoster covers both halves of the union
// cost: a suppressed edge whose character still has a VA credit degrades that
// character to credit-only, and one without any credit leaves the face.
func TestWorkCharactersExcludesSuppressedRoster(t *testing.T) {
	db := openCatalogTestDB(t)
	db.Raw("SELECT id FROM catalog_role WHERE key='scenario'").Scan(&roleScenario)
	for _, tbl := range []string{
		"catalog_credit", "catalog_work_character", "edit_suppressed_row",
		"catalog_work", "catalog_credit_name", "catalog_character",
	} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	w := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "花名册压制", ContentRating: 0, Status: 0}
	require.NoError(t, db.Create(&w).Error)
	voiced := model.CatalogCharacter{DisplayName: "配音あり", Lang: "ja"}
	require.NoError(t, db.Create(&voiced).Error)
	silent := model.CatalogCharacter{DisplayName: "配音なし", Lang: "ja"}
	require.NoError(t, db.Create(&silent).Error)
	va := model.CatalogCreditName{Name: "声優", Lang: "ja"}
	require.NoError(t, db.Create(&va).Error)
	for _, e := range []struct {
		id      int64
		kind    int16
		spoiler int16
	}{{voiced.ID, model.WorkCharacterKindMain, model.SpoilerSevere},
		{silent.ID, model.WorkCharacterKindAppears, model.SpoilerMild}} {
		require.NoError(t, db.Create(&model.CatalogWorkCharacter{
			WorkID: w.ID, CharacterID: e.id, Kind: e.kind, Spoiler: e.spoiler, MatchedBy: "import:test"}).Error)
	}
	require.NoError(t, db.Create(&model.CatalogCredit{
		WorkID: w.ID, CreditNameID: va.ID, RoleID: roleVoiceActor, CharacterID: &voiced.ID}).Error)

	app := readApp(service.NewReadService(db), nil)
	chars := func(t *testing.T) map[int64]map[string]any {
		t.Helper()
		code, body := getJSON(t, app, "/api/v1/catalog/works/"+itoa(w.ID))
		require.Equal(t, 200, code)
		out := map[int64]map[string]any{}
		for _, c := range body["data"].(map[string]any)["characters"].([]any) {
			m := c.(map[string]any)
			out[int64(m["character_id"].(float64))] = m
		}
		return out
	}

	before := chars(t)
	require.Len(t, before, 2)
	assert.NotEmpty(t, before[voiced.ID]["identity"], "a rendered roster edge carries its identity")
	assert.NotEmpty(t, before[silent.ID]["identity"])

	suppressRoster(t, db, w.ID, voiced.ID)
	suppressRoster(t, db, w.ID, silent.ID)

	after := chars(t)
	require.Len(t, after, 1, "the character with no credit leaves the face entirely")
	still := after[voiced.ID]
	require.NotNil(t, still, "the credit keeps this character in the union")
	assert.EqualValues(t, model.WorkCharacterKindUnknown, still["kind"], "credit-only → kind 0")
	assert.EqualValues(t, model.SpoilerNone, still["spoiler"], "credit-only → spoiler 0")
	_, hasIdentity := still["identity"]
	assert.False(t, hasIdentity, "a credit-only element must not hand out a roster identity")
	assert.Len(t, still["va"].([]any), 1)
}

// TestCharacterWorksTotalMatchesItemsUnderRosterSuppression guards the one
// paginated total on this table: total and the page share one expression, so a
// predicate added to one and not the other would list a work the count denies.
func TestCharacterWorksTotalMatchesItemsUnderRosterSuppression(t *testing.T) {
	db := openCatalogTestDB(t)
	db.Raw("SELECT id FROM catalog_role WHERE key='scenario'").Scan(&roleScenario)
	f := seedReverseFixture(t, db)
	require.NoError(t, db.Create(&model.CatalogWorkCharacter{
		WorkID: f.work1, CharacterID: f.char1, Kind: model.WorkCharacterKindMain, Spoiler: model.SpoilerMild, MatchedBy: "import:test"}).Error)
	require.NoError(t, db.Create(&model.CatalogWorkCharacter{
		WorkID: f.work3, CharacterID: f.char1, Kind: model.WorkCharacterKindAppears, MatchedBy: "import:test"}).Error)
	app := readApp(service.NewReadService(db), nil)

	read := func(t *testing.T) (float64, []any) {
		t.Helper()
		code, body := getJSON(t, app, "/api/v1/catalog/characters/"+itoa(f.char1)+"/works")
		require.Equal(t, 200, code)
		data := body["data"].(map[string]any)
		return data["total"].(float64), data["items"].([]any)
	}

	total, items := read(t)
	assert.EqualValues(t, 2, total)
	require.Len(t, items, 2)

	// w3 is reached by the roster edge alone; w1 also has voice credits.
	suppressRoster(t, db, f.work3, f.char1)
	total, items = read(t)
	assert.EqualValues(t, 1, total, "the total drops the roster-only work")
	require.Len(t, items, 1, "and the page agrees with the total")
	assert.EqualValues(t, f.work1, items[0].(map[string]any)["work"].(map[string]any)["work_id"])

	// w1 stays through its credits, but with the edge gone it is credit-only.
	suppressRoster(t, db, f.work1, f.char1)
	total, items = read(t)
	assert.EqualValues(t, 1, total)
	require.Len(t, items, 1)
	row := items[0].(map[string]any)
	assert.EqualValues(t, model.WorkCharacterKindUnknown, row["kind"], "kind falls to 0 with the edge suppressed")
	assert.EqualValues(t, model.SpoilerNone, row["spoiler"])
	_, hasIdentity := row["identity"]
	assert.False(t, hasIdentity)
	assert.Equal(t, true, row["voiced"])
}
