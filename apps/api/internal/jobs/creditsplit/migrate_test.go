package creditsplit

import (
	"context"
	"fmt"
	"testing"

	"api/internal/platform/catalog/editspec"
	"api/internal/platform/catalog/model"
	"api/internal/platform/editing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func migrateFixture(t *testing.T) (cnID, roleID, workA, workB int64) {
	t.Helper()
	for _, tbl := range []string{
		"catalog_revision", "catalog_external_ref", "catalog_credit",
		"catalog_credit_name", "catalog_person", "catalog_work",
	} {
		require.NoError(t, testDB.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	require.NoError(t, testDB.Raw(`SELECT id FROM catalog_role ORDER BY id LIMIT 1`).Scan(&roleID).Error)
	require.NotZero(t, roleID, "seeded role vocabulary is required")

	cn := model.CatalogCreditName{Name: "高濱 亮", Lang: "ja"}
	require.NoError(t, testDB.Create(&cn).Error)
	require.NoError(t, testDB.Create(&model.CatalogExternalRef{
		EntityType: model.EntityTypeCreditName, EntityID: cn.ID, SourceID: 3,
		ExternalID: "b1", LinkKind: model.LinkKindExact, MatchedBy: "rule:bangumi-person-import",
	}).Error)

	wa := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "work A",
		ContentRating: model.ContentRatingAllAges, Status: model.WorkStatusLive}
	wb := wa
	wb.DisplayName = "work B"
	require.NoError(t, testDB.Create(&wa).Error)
	require.NoError(t, testDB.Create(&wb).Error)

	eg, bgm := int16(5), int16(3)
	for _, c := range []model.CatalogCredit{
		{WorkID: wa.ID, CreditNameID: cn.ID, RoleID: roleID, SourceID: &eg},
		{WorkID: wb.ID, CreditNameID: cn.ID, RoleID: roleID, SourceID: &eg},
		{WorkID: wa.ID, CreditNameID: cn.ID, RoleID: roleID + 1, SourceID: &bgm},
	} {
		require.NoError(t, testDB.Create(&c).Error)
	}
	return cn.ID, roleID, wa.ID, wb.ID
}

func migrateWL(t *testing.T, cnID int64) string {
	t.Helper()
	return write(t, fmt.Sprintf(
		`{"credit_name_id":%d,"source":"eg","external_id":"15510","name":"高濱亮","lang":"ja","matched_by":"rule:eg-creater-import","reason":"wave 156b split"}`,
		cnID))
}

func TestMigrateDryThenApplyThenSecondPassIsZeroWrite(t *testing.T) {
	cnID, _, _, _ := migrateFixture(t)
	opts := Opts{DSN: testDSN, WorklistPath: migrateWL(t, cnID)}

	dry, err := RunMigrate(context.Background(), opts)
	require.NoError(t, err)
	assert.Equal(t, 1, dry.Rows)
	assert.Equal(t, 1, dry.WouldMint)
	assert.Equal(t, 2, dry.WouldMoveCredz, "only the two eg-sourced rows are in scope")
	assert.Zero(t, dry.Minted)
	assert.Zero(t, dry.CreditsMoved, "a dry run must write nothing")
	var names int64
	require.NoError(t, testDB.Model(&model.CatalogCreditName{}).Count(&names).Error)
	assert.EqualValues(t, 1, names)

	opts.Apply = true
	got, err := RunMigrate(context.Background(), opts)
	require.NoError(t, err)
	assert.Equal(t, 1, got.Minted)
	assert.Equal(t, 2, got.CreditsMoved)
	assert.Equal(t, 2, got.Revisions, "one on the minted row, one on the origin")
	require.Len(t, got.Receipts, 1)
	target := got.Receipts[0].ToCreditNameID
	require.NotZero(t, target)

	var minted model.CatalogCreditName
	require.NoError(t, testDB.First(&minted, target).Error)
	assert.Equal(t, "高濱亮", minted.Name)
	assert.Nil(t, minted.PersonID)
	var ref model.CatalogExternalRef
	require.NoError(t, testDB.Where("entity_type = ? AND entity_id = ?",
		model.EntityTypeCreditName, target).First(&ref).Error)
	assert.EqualValues(t, 5, ref.SourceID)
	assert.Equal(t, "15510", ref.ExternalID)
	assert.Equal(t, "rule:eg-creater-import", ref.MatchedBy)

	var total, onOld, onNew int64
	require.NoError(t, testDB.Model(&model.CatalogCredit{}).Count(&total).Error)
	require.NoError(t, testDB.Model(&model.CatalogCredit{}).Where("credit_name_id = ?", cnID).Count(&onOld).Error)
	require.NoError(t, testDB.Model(&model.CatalogCredit{}).Where("credit_name_id = ?", target).Count(&onNew).Error)
	assert.EqualValues(t, 3, total, "a migrate re-points credits, it never creates or drops one")
	assert.EqualValues(t, 1, onOld)
	assert.EqualValues(t, 2, onNew)

	var rev model.CatalogRevision
	require.NoError(t, testDB.Where("entity_type = ? AND entity_id = ?",
		model.EntityTypeCreditName, cnID).First(&rev).Error)
	assert.Equal(t, model.RevisionActionSplit, rev.Action)
	assert.Contains(t, string(rev.Snapshot), "moved_credit_ids")

	second, err := RunMigrate(context.Background(), opts)
	require.NoError(t, err)
	assert.Equal(t, 1, second.Skipped)
	assert.Zero(t, second.Minted)
	assert.Zero(t, second.CreditsMoved)
	assert.Zero(t, second.Revisions, "a second apply must add no revision")
	var revs int64
	require.NoError(t, testDB.Model(&model.CatalogRevision{}).Count(&revs).Error)
	assert.EqualValues(t, 2, revs)
}

func TestMigrateReusesAnImporterReMint(t *testing.T) {
	cnID, _, _, _ := migrateFixture(t)
	fresh := model.CatalogCreditName{Name: "高濱亮", Lang: "ja"}
	require.NoError(t, testDB.Create(&fresh).Error)
	require.NoError(t, testDB.Create(&model.CatalogExternalRef{
		EntityType: model.EntityTypeCreditName, EntityID: fresh.ID, SourceID: 5,
		ExternalID: "15510", LinkKind: model.LinkKindExact, MatchedBy: "rule:eg-creater-import",
	}).Error)

	st, err := RunMigrate(context.Background(), Opts{DSN: testDSN, WorklistPath: migrateWL(t, cnID), Apply: true})
	require.NoError(t, err)
	assert.Equal(t, 1, st.Reused)
	assert.Zero(t, st.Minted)
	assert.Equal(t, 2, st.CreditsMoved)
	assert.Equal(t, 1, st.Revisions, "no mint revision — only the origin's move revision")

	var onNew int64
	require.NoError(t, testDB.Model(&model.CatalogCredit{}).Where("credit_name_id = ?", fresh.ID).Count(&onNew).Error)
	assert.EqualValues(t, 2, onNew)
}

func TestMigrateRefusesWhenTheDuplicateIsAlreadyLive(t *testing.T) {
	cnID, roleID, workA, _ := migrateFixture(t)
	fresh := model.CatalogCreditName{Name: "高濱亮", Lang: "ja"}
	require.NoError(t, testDB.Create(&fresh).Error)
	require.NoError(t, testDB.Create(&model.CatalogExternalRef{
		EntityType: model.EntityTypeCreditName, EntityID: fresh.ID, SourceID: 5,
		ExternalID: "15510", LinkKind: model.LinkKindExact, MatchedBy: "rule:eg-creater-import",
	}).Error)
	eg := int16(5)
	require.NoError(t, testDB.Create(&model.CatalogCredit{
		WorkID: workA, CreditNameID: fresh.ID, RoleID: roleID, SourceID: &eg}).Error)

	st, err := RunMigrate(context.Background(), Opts{DSN: testDSN, WorklistPath: migrateWL(t, cnID), Apply: true})
	require.NoError(t, err)
	assert.Equal(t, 1, st.Refused)
	assert.Zero(t, st.CreditsMoved)
	assert.Contains(t, st.Refusals[0].Reason, "uq_catalog_credit")
}

func TestMigrateRefusesWhenTheSourceIsStillAnchored(t *testing.T) {
	cnID, _, _, _ := migrateFixture(t)
	require.NoError(t, testDB.Create(&model.CatalogExternalRef{
		EntityType: model.EntityTypeCreditName, EntityID: cnID, SourceID: 5,
		ExternalID: "99999", LinkKind: model.LinkKindExact, MatchedBy: "rule:eg-creater-import",
	}).Error)

	st, err := RunMigrate(context.Background(), Opts{DSN: testDSN, WorklistPath: migrateWL(t, cnID), Apply: true})
	require.NoError(t, err)
	assert.Equal(t, 1, st.Refused)
	assert.Contains(t, st.Refusals[0].Reason, "ambiguous")
	assert.Zero(t, st.CreditsMoved)
}

func TestLoadMigrateWorklistValidates(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"bad id", `{"credit_name_id":0,"source":"eg","external_id":"1","name":"x"}`, "positive id"},
		{"no anchor", `{"credit_name_id":1,"source":"","external_id":"","name":"x"}`, "source and external_id"},
		{"no name", `{"credit_name_id":1,"source":"eg","external_id":"1","name":"  "}`, "name is required"},
		{"duplicate anchor", "{\"credit_name_id\":1,\"source\":\"eg\",\"external_id\":\"1\",\"name\":\"x\"}\n{\"credit_name_id\":2,\"source\":\"eg\",\"external_id\":\"1\",\"name\":\"x\"}", "already listed"},
		{"empty", "", "no rows"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadMigrateWorklist(write(t, tc.body))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestCreditSplitFollowsIdentityWithScope(t *testing.T) {
	cnID, roleID, workA, workB := migrateFixture(t)
	require.NoError(t, testDB.Exec(`TRUNCATE edit_suppressed_row RESTART IDENTITY CASCADE`).Error)

	untouched := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "work C",
		ContentRating: model.ContentRatingAllAges, Status: model.WorkStatusLive}
	require.NoError(t, testDB.Create(&untouched).Error)

	oldKey := editspec.CreditIdentity(roleID, cnID, 0)
	for _, workID := range []int64{workA, workB, untouched.ID} {
		require.NoError(t, testDB.Create(&editing.SuppressedRow{
			EntityType: editspec.TypeWork, EntityID: workID,
			FieldKey: editspec.FieldWorkCredits, IdentityKey: oldKey,
		}).Error)
	}

	st, err := RunMigrate(context.Background(), Opts{DSN: testDSN, WorklistPath: migrateWL(t, cnID), Apply: true})
	require.NoError(t, err)
	require.Equal(t, 2, st.CreditsMoved)
	target := st.Receipts[0].ToCreditNameID
	require.NotZero(t, target)

	newKey := editspec.CreditIdentity(roleID, target, 0)
	keyOf := func(workID int64) string {
		var keys []string
		require.NoError(t, testDB.Model(&editing.SuppressedRow{}).
			Where("entity_type = ? AND entity_id = ? AND field_key = ?",
				editspec.TypeWork, workID, editspec.FieldWorkCredits).
			Pluck("identity_key", &keys).Error)
		require.Len(t, keys, 1)
		return keys[0]
	}
	assert.Equal(t, newKey, keyOf(workA), "a work whose rows moved must follow")
	assert.Equal(t, newKey, keyOf(workB), "a work whose rows moved must follow")
	assert.Equal(t, oldKey, keyOf(untouched.ID),
		"scopeEntityIDs must keep a work whose rows did not move out of the rewrite")
}
