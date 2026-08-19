package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/catalog/editspec"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/perm"
	"api/internal/platform/catalog/service"
	"api/internal/platform/editing"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func creditIdentityRows(t *testing.T, db *gorm.DB, workID int64, identity string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM catalog_credit c
		WHERE c.work_id = ? AND `+editspec.CreditIdentitySQL("c")+` = ?`, workID, identity).Scan(&n).Error)
	return n
}

func TestIdentityRoundTripsToExactlyOneCreditRow(t *testing.T) {
	db := openCatalogTestDB(t)
	db.Raw("SELECT id FROM catalog_role WHERE key='scenario'").Scan(&roleScenario)
	workID := seedReadFixture(t, db)
	app := readApp(service.NewReadService(db), nil)

	code, body := getJSON(t, app, "/api/v1/catalog/works/"+itoa(workID)+"/credits")
	require.Equal(t, 200, code)
	groups := body["data"].(map[string]any)["groups"].([]any)
	require.NotEmpty(t, groups)
	seen := 0
	for _, g := range groups {
		for _, c := range g.(map[string]any)["credits"].([]any) {
			identity, ok := c.(map[string]any)["identity"].(string)
			require.Truef(t, ok, "every credit on a 1:1 face carries an identity: %v", c)
			assert.EqualValuesf(t, 1, creditIdentityRows(t, db, workID, identity),
				"identity %q must name exactly one catalog_credit row", identity)
			seen++
		}
	}
	require.Equal(t, 2, seen)

	var nameID int64
	require.NoError(t, db.Raw(`SELECT credit_name_id FROM catalog_credit
		WHERE work_id = ? AND character_id IS NOT NULL LIMIT 1`, workID).Scan(&nameID).Error)
	code, body = getJSON(t, app, "/api/v1/catalog/names/"+itoa(nameID)+"/works")
	require.Equal(t, 200, code)
	items := body["data"].(map[string]any)["items"].([]any)
	require.NotEmpty(t, items)
	for _, it := range items {
		m := it.(map[string]any)
		wid := int64(m["work"].(map[string]any)["work_id"].(float64))
		for _, r := range m["roles"].([]any) {
			identity, ok := r.(map[string]any)["identity"].(string)
			require.Truef(t, ok, "names/{id}/works renders one credit row per role entry: %v", r)
			assert.EqualValues(t, 1, creditIdentityRows(t, db, wid, identity))
		}
	}

	// The collapsing faces must stay without one: their SQL is SELECT DISTINCT,
	// so a rendered row stands for 1..N credit rows.
	code, body = getJSON(t, app, "/api/v1/catalog/works/"+itoa(workID))
	require.Equal(t, 200, code)
	for _, ch := range body["data"].(map[string]any)["characters"].([]any) {
		for _, v := range ch.(map[string]any)["va"].([]any) {
			_, has := v.(map[string]any)["identity"]
			assert.False(t, has, "va[] is an N→1 collapse and must not offer a suppression handle")
		}
	}
	var charID int64
	require.NoError(t, db.Raw(`SELECT character_id FROM catalog_credit
		WHERE work_id = ? AND character_id IS NOT NULL LIMIT 1`, workID).Scan(&charID).Error)
	code, body = getJSON(t, app, "/api/v1/catalog/characters/"+itoa(charID)+"/works")
	require.Equal(t, 200, code)
	for _, it := range body["data"].(map[string]any)["items"].([]any) {
		for _, v := range it.(map[string]any)["voices"].([]any) {
			_, has := v.(map[string]any)["identity"]
			assert.False(t, has, "voices[] is an N→1 collapse and must not offer a suppression handle")
		}
	}
}

func TestEditSchemaCarriesPerFieldCaps(t *testing.T) {
	db := openCatalogTestDB(t)
	work := seedUserEditWork(t, db)

	want := map[string][2]int{ // key -> {max_elements, max_suppressed}
		editspec.FieldWorkCredits:      {500, 500},
		editspec.FieldWorkCreditsSuppr: {500, 500},
		editspec.FieldWorkRoster:       {500, 500},
		editspec.FieldWorkRosterSuppr:  {500, 500},
		editspec.FieldWorkTitles:       {100, 0},
		editspec.FieldWorkTitlesSuppr:  {200, 200},
		editspec.FieldWorkIntros:       {200, 0},
		editspec.FieldWorkLinks:        {200, 0},
		editspec.FieldWorkDisplayName:  {0, 0},
	}

	// (1) the S2S mapping.
	reg := editing.NewRegistry()
	require.NoError(t, editspec.RegisterWork(reg, db))
	engine := editing.NewEngine(db, reg)
	fields := schemaViaEngine(t, engine, PermResolvers{"catalog": perm.Resolver}, "nextmoe",
		dto.EditActor{UserID: 1, Roles: []string{"admin"}}, "catalog.work", work)
	s2s := map[string][2]int{}
	for _, v := range SchemaFieldViews(fields) {
		s2s[v.Key] = [2]int{v.MaxElements, v.MaxSuppressed}
	}
	for key, caps := range want {
		assert.Equalf(t, caps, s2s[key], "S2S schema caps for %s", key)
	}

	// (2) the user-token mapping, which is a second copy of the same loop; a
	// wave that changed only one of them would leave the open face blind.
	app := userEditApp(t, db, userEditClients())
	status, raw := userEditReq(t, app, "GET",
		fmt.Sprintf("%s/edit/schema/catalog.work?entity_id=%d", UserPrefix, work),
		userToken(t, 501, ScopeCatalogEdit, "kungal-client"), "")
	require.Equal(t, fiber.StatusOK, status, string(raw))
	var env struct {
		Data struct {
			Fields []struct {
				Key           string `json:"key"`
				MaxElements   int    `json:"max_elements"`
				MaxSuppressed int    `json:"max_suppressed"`
			} `json:"fields"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &env), string(raw))
	user := map[string][2]int{}
	for _, f := range env.Data.Fields {
		user[f.Key] = [2]int{f.MaxElements, f.MaxSuppressed}
	}
	for key, caps := range want {
		assert.Equalf(t, caps, user[key], "user-token schema caps for %s", key)
	}
}

func TestCuratedCreditsRenderThroughTheS2SFace(t *testing.T) {
	db := openCatalogTestDB(t)
	db.Raw("SELECT id FROM catalog_role WHERE key='scenario'").Scan(&roleScenario)
	workID := seedReadFixture(t, db)

	reg := editing.NewRegistry()
	require.NoError(t, editspec.RegisterWork(reg, db))
	engine := editing.NewEngine(db, reg)

	var vaName int64
	require.NoError(t, db.Raw(`SELECT id FROM catalog_credit_name WHERE name = ?`, "声優テスト").
		Scan(&vaName).Error)
	require.NotZero(t, vaName)

	actor := editing.PolicyContext{
		UserID: 1, Site: "nextmoe",
		HasPerm: func(key string) bool { return true },
	}
	prop, _, err := engine.CreateProposal(context.Background(), editing.CreateProposalInput{
		EntityType: editspec.TypeWork, EntityID: workID,
		Patch: map[string]any{editspec.FieldWorkCredits: []any{
			map[string]any{"role_id": float64(roleScenario), "credit_name_id": float64(vaName)},
		}},
		Actor: actor,
	})
	require.NoError(t, err)
	_, err = engine.MergeProposal(context.Background(), prop.ID, actor, "")
	require.NoError(t, err)

	var curatedRows int64
	require.NoError(t, db.Model(&model.CatalogCredit{}).
		Where("work_id = ? AND source_id = ?", workID, 12).Count(&curatedRows).Error)
	require.EqualValues(t, 1, curatedRows)

	app := readApp(service.NewReadService(db), nil)
	code, body := getJSON(t, app, "/api/v1/catalog/works/"+itoa(workID)+"/credits")
	require.Equal(t, 200, code)
	found := ""
	for _, g := range body["data"].(map[string]any)["groups"].([]any) {
		gm := g.(map[string]any)
		if int64(gm["role_id"].(float64)) != roleScenario {
			continue
		}
		first := gm["credits"].([]any)[0].(map[string]any)
		found = first["source"].(string)
		assert.Equal(t, editspec.CreditIdentity(roleScenario, vaName, 0), first["identity"])
	}
	assert.Equal(t, "curated", found, "the human lane leads its role group and names itself")
}
