package importer

import (
	"fmt"
	"testing"

	"api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedVNDBStaff(t *testing.T, sid, lang string) {
	t.Helper()
	require.NoError(t, testDB.Exec(`INSERT INTO src_vndb.staff (id, gender, lang, main, description, prod)
		VALUES (?, '', ?, 0, '', '')`, sid, lang).Error)
}

func seedVNDBAlias(t *testing.T, aid int, sid, name, latin string) {
	t.Helper()
	require.NoError(t, testDB.Exec(`INSERT INTO src_vndb.staff_alias (aid, id, name, latin) VALUES (?,?,?,?)`, aid, sid, name, latin).Error)
}

func seedVNStaff(t *testing.T, vid string, aid int, role, note string) {
	t.Helper()
	require.NoError(t, testDB.Exec(`INSERT INTO src_vndb.vn_staff (id, aid, role, eid, note) VALUES (?,?,?,NULL,?)`, vid, aid, role, note).Error)
}

func seedVNStaffEid(t *testing.T, vid string, aid int, role string, eid int, note string) {
	t.Helper()
	require.NoError(t, testDB.Exec(`INSERT INTO src_vndb.vn_staff (id, aid, role, eid, note) VALUES (?,?,?,?,?)`, vid, aid, role, eid, note).Error)
}

func seedVNSeiyuu(t *testing.T, vid, cid string, aid int, note string) {
	t.Helper()
	require.NoError(t, testDB.Exec(`INSERT INTO src_vndb.vn_seiyuu (id, cid, aid, note) VALUES (?,?,?,?)`, vid, cid, aid, note).Error)
}

func seedVNDBCharAnchor(t *testing.T, cid string) int64 {
	t.Helper()
	var id int64
	require.NoError(t, testDB.Raw(`INSERT INTO catalog_character (display_name, lang, description, field_provenance)
		VALUES ('c','ja','','{}') RETURNING id`).Scan(&id).Error)
	require.NoError(t, testDB.Exec(`INSERT INTO catalog_external_ref (entity_type, entity_id, source_id, external_id, link_kind, matched_by)
		VALUES (4, ?, 2, ?, 0, 'rule:vndb-character-import')`, id, cid).Error)
	return id
}

func seedOrphanCreditName(t *testing.T, name string) int64 {
	t.Helper()
	var id int64
	require.NoError(t, testDB.Raw(`INSERT INTO catalog_credit_name (name, lang, kind, link_visibility, note, field_provenance)
		VALUES (?, 'ja', 0, 0, '', '{}') RETURNING id`, name).Scan(&id).Error)
	return id
}

func seedVNDBRef(t *testing.T, entityType int16, entityID int64, extID string, linkKind int16) {
	t.Helper()
	require.NoError(t, testDB.Exec(`INSERT INTO catalog_external_ref (entity_type, entity_id, source_id, external_id, link_kind, matched_by)
		VALUES (?, ?, 2, ?, ?, 'rule:test-seed')`, entityType, entityID, extID, linkKind).Error)
}

func TestVNDBCreditsWave_ClaimedAliasIsNotAVacancy(t *testing.T) {
	clean(t)
	work := seedVNDBWork(t, "v300")
	seedVNDBStaff(t, "s4", "ja")
	seedVNDBAlias(t, 40, "s4", "掛谷", "Kakeya")
	seedVNDBAlias(t, 41, "s4", "牧野", "Makino")
	seedVNStaff(t, "v300", 40, "scenario", "")
	seedVNStaff(t, "v300", 41, "director", "")

	survivor := seedOrphanCreditName(t, "掛谷")
	seedVNDBRef(t, model.EntityTypeCreditName, survivor, "40", model.LinkKindProbable)
	seedVNDBRef(t, model.EntityTypeCreditName, survivor, "99", model.LinkKindProbable)
	imported := seedOrphanCreditName(t, "牧野")
	seedVNDBRef(t, model.EntityTypeCreditName, imported, "41", model.LinkKindExact)
	other := seedOrphanCreditName(t, "牧野(別)")
	seedVNDBRef(t, model.EntityTypeCreditName, other, "41", model.LinkKindProbable)

	dry, err := New(testDB, nil, Options{Source: "vndb", DryRun: true}).Run("vndb")
	require.NoError(t, err)
	assert.Equal(t, 1, dry.SkippedClaimedProbableName, "alias 40 is claimed by an alive credit_name")
	assert.Zero(t, dry.NamesCreated, "nothing left to mint")
	assert.Equal(t, 1, dry.CreditsWritten, "only alias 41's director credit is planned")

	st, err := New(testDB, nil, Options{Source: "vndb"}).Run("vndb")
	require.NoError(t, err)
	assert.Equal(t, 1, st.SkippedClaimedProbableName)
	assert.Zero(t, st.NamesCreated)
	assert.Zero(t, st.Errors)

	assert.Equal(t, int64(3), scalarInt(t, `SELECT count(*) FROM catalog_credit_name`), "no duplicate minted")
	assert.Zero(t, scalarInt(t, `SELECT count(*) FROM catalog_external_ref
		WHERE entity_type=1 AND source_id=2 AND external_id='40' AND link_kind=0`), "id 40 never re-claimed as exact")
	assert.Equal(t, int64(1), scalarIntA(t, `SELECT count(*) FROM catalog_external_ref
		WHERE entity_type=1 AND source_id=2 AND external_id='40' AND link_kind=? AND entity_id=?`,
		model.LinkKindProbable, survivor))
	assert.Equal(t, int64(1), scalarInt(t, `SELECT count(*) FROM catalog_credit`))
	assert.Equal(t, int64(1), scalarIntA(t, `SELECT count(*) FROM catalog_credit WHERE work_id=? AND credit_name_id=?`, work, imported))
}

func TestVNDBCreditsWave_VAResolutionIsLivenessAware(t *testing.T) {
	clean(t)
	work := seedVNDBWork(t, "v400")
	seedVNDBStaff(t, "s5", "ja")
	seedVNDBAlias(t, 50, "s5", "声優", "Seiyuu")

	alive := seedVNDBCharAnchor(t, "cA")
	claimed := seedVNDBCharAnchor(t, "cB")
	require.NoError(t, testDB.Exec(`UPDATE catalog_external_ref SET link_kind = ?
		WHERE entity_type=4 AND source_id=2 AND external_id='cB'`, model.LinkKindProbable).Error)
	retired := seedVNDBCharAnchor(t, "cC")
	require.NoError(t, testDB.Exec(`UPDATE catalog_character SET deleted_at = now() WHERE id = ?`, retired).Error)
	freed := seedVNDBCharAnchor(t, "cD")
	require.NoError(t, testDB.Exec(`UPDATE catalog_external_ref SET link_kind = ?
		WHERE entity_type=4 AND source_id=2 AND external_id='cD'`, model.LinkKindProbable).Error)
	require.NoError(t, testDB.Exec(`UPDATE catalog_character SET deleted_at = now() WHERE id = ?`, freed).Error)

	seedVNSeiyuu(t, "v400", "cA", 50, "")
	seedVNSeiyuu(t, "v400", "cB", 50, "")
	seedVNSeiyuu(t, "v400", "cC", 50, "")
	seedVNSeiyuu(t, "v400", "cD", 50, "")
	seedVNSeiyuu(t, "v400", "cE", 50, "")

	st, err := New(testDB, nil, Options{Source: "vndb"}).Run("vndb")
	require.NoError(t, err)
	assert.Zero(t, st.Errors)
	assert.Equal(t, 1, st.SkippedClaimedProbableChar, "cB only")
	assert.Equal(t, 1, st.SkippedRetiredExactChar, "cC only")
	assert.Zero(t, st.SkippedClaimedProbableName, "the alias itself is unclaimed")
	assert.Equal(t, 1, st.CreditsWritten, "only cA's VA credit survives")
	assert.Equal(t, int64(1), scalarIntA(t, `SELECT count(*) FROM catalog_credit WHERE work_id=? AND character_id=?`, work, alive))
	assert.Zero(t, scalarIntA(t, `SELECT count(*) FROM catalog_credit WHERE character_id IN (?,?,?)`, claimed, retired, freed))
}

func TestVNDBCreditsWave(t *testing.T) {
	clean(t)
	work := seedVNDBWork(t, "v100")

	seedVNDBStaff(t, "s1", "ja")
	seedVNDBStaff(t, "s2", "en")
	seedVNDBAlias(t, 10, "s1", "織田薫", "Oda Kaoru")
	seedVNDBAlias(t, 20, "s2", "OdaKaoru", "")

	seedVNStaff(t, "v100", 10, "scenario", "")
	seedVNStaff(t, "v100", 10, "director", "")
	seedVNStaffEid(t, "v100", 10, "scenario", 5, "")
	seedVNStaff(t, "v100", 20, "future-role", "")
	seedVNStaff(t, "v999", 10, "music", "")

	c50 := seedVNDBCharAnchor(t, "c50")
	seedVNSeiyuu(t, "v100", "c50", 20, "主演")
	seedVNSeiyuu(t, "v100", "c999", 10, "")

	dry, err := New(testDB, nil, Options{Source: "vndb", DryRun: true}).Run("vndb")
	require.NoError(t, err)
	assert.Equal(t, 2, dry.NamesCreated)
	assert.Equal(t, 4, dry.CreditsWritten, "would-be plans")
	assert.Equal(t, 1, dry.SkippedUnmappedRole)
	assert.Zero(t, scalarInt(t, `SELECT count(*) FROM catalog_credit`), "dry writes nothing")

	st, err := New(testDB, nil, Options{Source: "vndb"}).Run("vndb")
	require.NoError(t, err)
	assert.Equal(t, 2, st.NamesCreated)
	assert.Equal(t, 3, st.CreditsWritten, "scenario eid-dup collapsed")
	assert.Equal(t, 1, st.Already, "the collapsed duplicate")
	assert.Equal(t, 1, st.SkippedUnmappedRole)
	assert.Zero(t, st.Errors)

	assert.Equal(t, int64(3), scalarInt(t, `SELECT count(*) FROM catalog_credit WHERE source_id=2`))
	assert.Equal(t, int64(3), scalarInt(t, fmt.Sprintf(`SELECT count(*) FROM catalog_credit WHERE work_id=%d`, work)))

	assert.Equal(t, int64(2), scalarInt(t, `SELECT count(*) FROM catalog_credit_name`))
	assert.Zero(t, scalarInt(t, `SELECT count(*) FROM catalog_credit_name WHERE person_id IS NOT NULL`))
	assert.Zero(t, scalarInt(t, `SELECT count(*) FROM catalog_person`))

	assert.Equal(t, int64(2), scalarInt(t, `SELECT count(*) FROM catalog_external_ref
		WHERE entity_type=1 AND source_id=2 AND link_kind=0 AND matched_by='rule:vndb-staff-import' AND external_id IN ('10','20')`))
	cn10 := scalarInt(t, `SELECT entity_id FROM catalog_external_ref WHERE entity_type=1 AND source_id=2 AND external_id='10'`)
	var name10, lang10 string
	require.NoError(t, testDB.Raw(`SELECT name, lang FROM catalog_credit_name WHERE id=?`, cn10).Row().Scan(&name10, &lang10))
	assert.Equal(t, "織田薫", name10)
	assert.Equal(t, "ja", lang10)
	assert.Equal(t, "en", func() string {
		var l string
		cn20 := scalarInt(t, `SELECT entity_id FROM catalog_external_ref WHERE entity_type=1 AND source_id=2 AND external_id='20'`)
		require.NoError(t, testDB.Raw(`SELECT lang FROM catalog_credit_name WHERE id=?`, cn20).Scan(&l).Error)
		return l
	}())

	assert.Equal(t, int64(1), scalarInt(t, fmt.Sprintf(`SELECT count(*) FROM catalog_credit WHERE work_id=%d AND role_id=247 AND character_id IS NULL`, work)))
	assert.Equal(t, int64(1), scalarInt(t, fmt.Sprintf(`SELECT count(*) FROM catalog_credit WHERE work_id=%d AND role_id=173 AND character_id IS NULL`, work)))

	assert.Equal(t, int64(1), scalarInt(t, fmt.Sprintf(`SELECT count(*) FROM catalog_credit WHERE work_id=%d AND role_id=1 AND character_id=%d`, work, c50)))
	var vaNote string
	require.NoError(t, testDB.Raw(fmt.Sprintf(`SELECT note FROM catalog_credit WHERE role_id=1 AND character_id=%d`, c50)).Scan(&vaNote).Error)
	assert.Equal(t, "主演", vaNote)

	assert.Equal(t, int64(2), scalarInt(t, fmt.Sprintf(`SELECT count(*) FROM catalog_revision WHERE action=%d AND entity_type=1`, model.RevisionActionImported)))

	st2, err := New(testDB, nil, Options{Source: "vndb"}).Run("vndb")
	require.NoError(t, err)
	assert.Zero(t, st2.NamesCreated)
	assert.Zero(t, st2.CreditsWritten)
	assert.Equal(t, 4, st2.Already)
	assert.Equal(t, int64(3), scalarInt(t, `SELECT count(*) FROM catalog_credit WHERE source_id=2`), "still exactly 3 credits")
}

func TestVNDBCreditsWave_RefinesStaffNotes(t *testing.T) {
	clean(t)
	work := seedVNDBWork(t, "v200")
	seedVNDBStaff(t, "s3", "ja")
	seedVNDBAlias(t, 30, "s3", "かつらぎ", "")
	seedVNDBAlias(t, 31, "s3", "ムービー屋", "")
	seedVNDBAlias(t, 32, "s3", "多芸な人", "")
	seedVNStaff(t, "v200", 30, "staff", "Programming")
	seedVNStaff(t, "v200", 31, "staff", "Movie assistance")
	seedVNStaff(t, "v200", 32, "staff", "Planning, script")

	st, err := New(testDB, nil, Options{Source: "vndb"}).Run("vndb")
	require.NoError(t, err)
	assert.Equal(t, 4, st.CreditsWritten, "composite note plans one credit per position")

	assert.Equal(t, int64(1), scalarInt(t, fmt.Sprintf(
		`SELECT count(*) FROM catalog_credit WHERE work_id=%d AND role_id=238 AND note='Programming'`, work)))
	assert.Equal(t, int64(1), scalarInt(t, fmt.Sprintf(
		`SELECT count(*) FROM catalog_credit WHERE work_id=%d AND role_id=2 AND note='Movie assistance'`, work)))

	assert.Equal(t, int64(2), scalarInt(t, fmt.Sprintf(
		`SELECT count(*) FROM catalog_credit WHERE work_id=%d AND note='Planning, script' AND role_id IN (291,238)`, work)))

	for note, roleID := range StaffNoteRoleTable() {
		assert.Equal(t, int64(1), scalarInt(t, fmt.Sprintf(
			`SELECT count(*) FROM catalog_role WHERE id=%d`, roleID)),
			"note %q targets a role id absent from the seeded vocabulary", note)
	}
}
