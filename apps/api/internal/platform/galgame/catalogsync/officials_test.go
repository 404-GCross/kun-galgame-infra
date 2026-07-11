package catalogsync

import (
	"testing"

	"api/internal/platform/catalog/model"
	gmodel "api/internal/platform/galgame/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

// setupOfficials ensures the wiki official tables exist (the catalog Gold schema
// is already migrated by TestMain) and truncates everything this wave touches.
func setupOfficials(t *testing.T) {
	t.Helper()
	require.NoError(t, testDB.AutoMigrate(
		&gmodel.GalgameOfficial{}, &gmodel.GalgameOfficialAlias{}, &gmodel.GalgameOfficialRelation{},
	))
	require.NoError(t, testDB.Exec(`TRUNCATE
		catalog_work_label, catalog_match_candidate, catalog_label_alias, catalog_label,
		catalog_revision, catalog_work_title, catalog_release, catalog_credit, catalog_work,
		galgame_official_relation, galgame_official_alias, galgame_official, galgame
		RESTART IDENTITY CASCADE`).Error)
}

// seedGalgameStub inserts a bare galgame row so the official_relation FK
// (galgame_id → galgame) is satisfied. The catalog claim lives in catalog_work.
func seedGalgameStub(t *testing.T, id int64) {
	t.Helper()
	seedGame(t, id, "", nil, "g", "", 0)
}

func seedExistingLabel(t *testing.T, name string, kind int16) int64 {
	t.Helper()
	l := model.CatalogLabel{DisplayName: name, Kind: kind, Lang: "ja"}
	require.NoError(t, testDB.Create(&l).Error)
	return l.ID
}

func seedLabelAlias(t *testing.T, labelID int64, name string) {
	t.Helper()
	require.NoError(t, testDB.Create(&model.CatalogLabelAlias{
		LabelID: labelID, Name: name, Kind: model.AliasKindSpellingVariant,
	}).Error)
}

func seedClaimedWork(t *testing.T, productWorkID int64) int64 {
	t.Helper()
	site := siteGalgame
	w := model.CatalogWork{
		MediumID: 1, Site: &site, ProductWorkID: &productWorkID, OLang: "ja",
		DisplayName: "work", Extra: datatypes.JSON(`{}`), FieldProvenance: datatypes.JSON(`{}`),
	}
	require.NoError(t, testDB.Create(&w).Error)
	return w.ID
}

func seedOfficial(t *testing.T, id int64, name, original, lang, category string) {
	t.Helper()
	require.NoError(t, testDB.Exec(
		`INSERT INTO galgame_official (id, name, original, lang, category) VALUES (?, ?, ?, ?, ?)`,
		id, name, original, lang, category).Error)
}

func seedOfficialAlias(t *testing.T, officialID int64, name string) {
	t.Helper()
	require.NoError(t, testDB.Exec(
		`INSERT INTO galgame_official_alias (name, galgame_official_id) VALUES (?, ?)`, name, officialID).Error)
}

func seedOfficialRelation(t *testing.T, galgameID, officialID int64) {
	t.Helper()
	require.NoError(t, testDB.Exec(
		`INSERT INTO galgame_official_relation (galgame_id, official_id) VALUES (?, ?)`, galgameID, officialID).Error)
}

func TestRegisterOfficials(t *testing.T) {
	if testDB == nil {
		t.Skip("no test database")
	}
	setupOfficials(t)

	// Pre-wave labels (Bangumi/DLsite legacy): a company "Key" and a circle that
	// carries a matching ALIAS (tests both-sides-alias collision).
	lKey := seedExistingLabel(t, "Key", model.LabelKindPublisher)
	lCircle := seedExistingLabel(t, "サークル既存", model.LabelKindDoujinCircle)
	seedLabelAlias(t, lCircle, "エイリアス標的")

	// Galgame stub rows (relation FK) + claimed works (galgame 3 deliberately
	// NOT claimed in catalog).
	for id := int64(1); id <= 5; id++ {
		seedGalgameStub(t, id)
	}
	wG1 := seedClaimedWork(t, 1)
	seedClaimedWork(t, 2)
	seedClaimedWork(t, 4)
	seedClaimedWork(t, 5)

	// Officials to register.
	seedOfficial(t, 101, "Key", "", "ja", "company")                  // display collides with lKey
	seedOfficial(t, 102, "新しいブランド", "", "ja", "individual")    // no collision
	seedOfficial(t, 103, "サークルＸ", "サークルx", "ja", "amateur")  // amateur→circle; original+alias; unclaimed edge
	seedOfficial(t, 104, "エイリアス標的", "", "ja", "company")       // collides with lCircle via its alias
	seedOfficial(t, 105, "別名テスト", "", "ja", "company")           // collides with lKey via its own alias
	seedOfficialAlias(t, 103, "circle-x-alias")
	seedOfficialAlias(t, 105, "Key")

	seedOfficialRelation(t, 1, 101)
	seedOfficialRelation(t, 2, 102)
	seedOfficialRelation(t, 3, 103) // galgame 3 unclaimed → skip
	seedOfficialRelation(t, 4, 104)
	seedOfficialRelation(t, 5, 105)

	// --- dry run: full plan, nothing written ---
	ds, err := NewOfficialRegistrar(testDB, testDB, true, 0).Run()
	require.NoError(t, err)
	assert.Equal(t, 5, ds.Officials)
	assert.Equal(t, 5, ds.Minted)
	assert.Equal(t, 0, ds.Already)
	assert.Equal(t, 3, ds.AliasesWritten, "103: original + circle-x-alias; 105: Key")
	assert.Equal(t, 3, ds.NameCandidates, "101→Key, 104→circle(alias), 105→Key(own alias)")
	assert.Equal(t, 4, ds.EdgesWritten, "g1/g2/g4/g5 claimed")
	assert.Equal(t, 1, ds.EdgesSkippedUnclaimed, "g3 unclaimed")
	assert.Equal(t, int64(2), count(t, "catalog_label"), "dry run must not mint")

	// --- apply run 1 ---
	s1, err := NewOfficialRegistrar(testDB, testDB, false, 0).Run()
	require.NoError(t, err)
	assert.Equal(t, 5, s1.Minted)
	assert.Equal(t, 3, s1.AliasesWritten)
	assert.Equal(t, 3, s1.NameCandidates)
	assert.Equal(t, 4, s1.EdgesWritten)
	assert.Equal(t, 0, s1.EdgesAlready)
	assert.Equal(t, 1, s1.EdgesSkippedUnclaimed)

	assert.Equal(t, int64(7), count(t, "catalog_label"), "2 pre-wave + 5 minted")
	assert.Equal(t, int64(5), count(t, "catalog_revision"), "one imported revision per mint")
	assert.Equal(t, int64(3), count(t, "catalog_match_candidate"))
	assert.Equal(t, int64(4), count(t, "catalog_work_label"))

	// Every official mapped; kind mapping is correct.
	assert.Equal(t, int64(0), officialUnmapped(t))
	assert.EqualValues(t, model.LabelKindGameBrand, labelKindOfOfficial(t, 101), "company→game_brand")
	assert.EqualValues(t, model.LabelKindDoujinCircle, labelKindOfOfficial(t, 103), "amateur→doujin_circle")

	// Provenance note stamped on every mint; original landed as an alias.
	l101 := labelIDOfOfficial(t, 101)
	assert.Equal(t, ruleOfficialImport, labelNote(t, l101))
	assert.True(t, labelHasAlias(t, labelIDOfOfficial(t, 103), "サークルx"), "original → alias")

	// The imported revision is present with the right action.
	assert.EqualValues(t, model.RevisionActionImported, revisionAction(t, l101))

	// Candidate against the existing Key label: entity=label, name-norm reason,
	// pending, ordered a<b (proves the no-auto-equate mechanism fires on a real
	// pre-existing label).
	var cand model.CatalogMatchCandidate
	require.NoError(t, testDB.Where("entity_type=? AND b_id=?", model.EntityTypeLabel, l101).First(&cand).Error)
	assert.Equal(t, lKey, cand.AID)
	assert.EqualValues(t, model.CandidateReasonNameNormEqual, cand.Reason)
	assert.EqualValues(t, model.CandidateStatusPending, cand.Status)
	assert.Less(t, cand.AID, cand.BID)

	// Brand edge: work g1 → official 101's label, kind=Brand, source NULL (user).
	var edge model.CatalogWorkLabel
	require.NoError(t, testDB.Where("work_id=?", wG1).First(&edge).Error)
	assert.Equal(t, l101, edge.LabelID)
	assert.EqualValues(t, model.WorkLabelKindBrand, edge.Kind)
	assert.Nil(t, edge.SourceID)

	// --- apply run 2: idempotent (already=all, zero new writes) ---
	s2, err := NewOfficialRegistrar(testDB, testDB, false, 0).Run()
	require.NoError(t, err)
	assert.Equal(t, 0, s2.Minted)
	assert.Equal(t, 5, s2.Already)
	assert.Equal(t, 0, s2.AliasesWritten)
	assert.Equal(t, 0, s2.NameCandidates)
	assert.Equal(t, 0, s2.EdgesWritten, "second pass writes no edges")
	assert.Equal(t, 4, s2.EdgesAlready)
	assert.Equal(t, 1, s2.EdgesSkippedUnclaimed)
	assert.Equal(t, int64(7), count(t, "catalog_label"), "second pass mints nothing")
	assert.Equal(t, int64(3), count(t, "catalog_match_candidate"))
	assert.Equal(t, int64(4), count(t, "catalog_work_label"))
}

func TestRegisterOfficialsLimit(t *testing.T) {
	if testDB == nil {
		t.Skip("no test database")
	}
	setupOfficials(t)
	for id := int64(1); id <= 3; id++ {
		seedGalgameStub(t, id)
	}
	seedClaimedWork(t, 1)
	seedClaimedWork(t, 2)
	seedClaimedWork(t, 3)
	seedOfficial(t, 201, "AAA", "", "ja", "company")
	seedOfficial(t, 202, "BBB", "", "ja", "company")
	seedOfficial(t, 203, "CCC", "", "ja", "company")
	seedOfficialRelation(t, 1, 201)
	seedOfficialRelation(t, 2, 202)
	seedOfficialRelation(t, 3, 203)

	// --limit 2 mints only the first two officials; the edge phase covers only
	// mapped officials, so official 203's relation is not yet an edge.
	s, err := NewOfficialRegistrar(testDB, testDB, false, 2).Run()
	require.NoError(t, err)
	assert.Equal(t, 3, s.Officials)
	assert.Equal(t, 2, s.Minted)
	assert.Equal(t, int64(2), count(t, "catalog_label"))
	assert.Equal(t, int64(2), count(t, "catalog_work_label"))
	assert.Equal(t, int64(1), officialUnmapped(t), "203 still unmapped")

	// A follow-up full run mints the remainder and backfills the third edge.
	s2, err := NewOfficialRegistrar(testDB, testDB, false, 0).Run()
	require.NoError(t, err)
	assert.Equal(t, 1, s2.Minted)
	assert.Equal(t, 2, s2.Already)
	assert.Equal(t, 1, s2.EdgesWritten, "203's edge backfilled")
	assert.Equal(t, int64(3), count(t, "catalog_work_label"))
	assert.Equal(t, int64(0), officialUnmapped(t))
}

// --- helpers ---------------------------------------------------------------

func officialUnmapped(t *testing.T) int64 {
	t.Helper()
	var n int64
	require.NoError(t, testDB.Raw(`SELECT count(*) FROM galgame_official WHERE catalog_label_id IS NULL`).Scan(&n).Error)
	return n
}

func labelIDOfOfficial(t *testing.T, officialID int64) int64 {
	t.Helper()
	var id int64
	require.NoError(t, testDB.Raw(`SELECT catalog_label_id FROM galgame_official WHERE id=?`, officialID).Scan(&id).Error)
	require.NotZero(t, id)
	return id
}

func labelKindOfOfficial(t *testing.T, officialID int64) int16 {
	t.Helper()
	var kind int16
	require.NoError(t, testDB.Raw(`SELECT kind FROM catalog_label WHERE id=?`, labelIDOfOfficial(t, officialID)).Scan(&kind).Error)
	return kind
}

func labelNote(t *testing.T, labelID int64) string {
	t.Helper()
	var note string
	require.NoError(t, testDB.Raw(`SELECT note FROM catalog_label WHERE id=?`, labelID).Scan(&note).Error)
	return note
}

func labelHasAlias(t *testing.T, labelID int64, name string) bool {
	t.Helper()
	var n int64
	require.NoError(t, testDB.Raw(`SELECT count(*) FROM catalog_label_alias WHERE label_id=? AND name=?`, labelID, name).Scan(&n).Error)
	return n > 0
}

func revisionAction(t *testing.T, labelID int64) int16 {
	t.Helper()
	var action int16
	require.NoError(t, testDB.Raw(`SELECT action FROM catalog_revision WHERE entity_type=? AND entity_id=?`,
		model.EntityTypeLabel, labelID).Scan(&action).Error)
	return action
}
