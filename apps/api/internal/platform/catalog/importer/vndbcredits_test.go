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

// seedVNDBCharAnchor mints a catalog character + its source-2 exact anchor (the
// roster wave's product), so a VA credit's cid resolves.
func seedVNDBCharAnchor(t *testing.T, cid string) int64 {
	t.Helper()
	var id int64
	require.NoError(t, testDB.Raw(`INSERT INTO catalog_character (display_name, lang, description, field_provenance)
		VALUES ('c','ja','','{}') RETURNING id`).Scan(&id).Error)
	require.NoError(t, testDB.Exec(`INSERT INTO catalog_external_ref (entity_type, entity_id, source_id, external_id, link_kind, matched_by)
		VALUES (4, ?, 2, ?, 0, 'rule:vndb-character-import')`, id, cid).Error)
	return id
}

// seedOrphanCreditName mints a bare orphan credit_name and returns its id.
func seedOrphanCreditName(t *testing.T, name string) int64 {
	t.Helper()
	var id int64
	require.NoError(t, testDB.Raw(`INSERT INTO catalog_credit_name (name, lang, kind, link_visibility, note, field_provenance)
		VALUES (?, 'ja', 0, 0, '', '{}') RETURNING id`, name).Scan(&id).Error)
	return id
}

// seedVNDBRef attaches a source-2 ref of any grade to an entity — the shape a
// merge leaves behind when it demotes two competing exacts to probable.
func seedVNDBRef(t *testing.T, entityType int16, entityID int64, extID string, linkKind int16) {
	t.Helper()
	require.NoError(t, testDB.Exec(`INSERT INTO catalog_external_ref (entity_type, entity_id, source_id, external_id, link_kind, matched_by)
		VALUES (?, ?, 2, ?, ?, 'rule:test-seed')`, entityType, entityID, extID, linkKind).Error)
}

// TestVNDBCreditsWave_ClaimedAliasIsNotAVacancy pins the wave-200 ruling on the
// credit_name axis: an alias id an ALIVE credit_name already answers to at
// probable grade is not free. The importer must not mint a second body for it
// (that would re-split the merge that produced the probable ref), must not
// re-grade the probable ref, and must say so in its own counter. An id that also
// carries an alive EXACT anchor is not claimed at all and resumes normally.
func TestVNDBCreditsWave_ClaimedAliasIsNotAVacancy(t *testing.T) {
	clean(t)
	work := seedVNDBWork(t, "v300")
	seedVNDBStaff(t, "s4", "ja")
	seedVNDBAlias(t, 40, "s4", "掛谷", "Kakeya")
	seedVNDBAlias(t, 41, "s4", "牧野", "Makino")
	seedVNStaff(t, "v300", 40, "scenario", "") // alias 40 is claimed at probable → skipped
	seedVNStaff(t, "v300", 41, "director", "") // alias 41 has an alive exact → resumes

	// The merge survivor: one credit_name holding BOTH demoted ids (the real
	// prod shape — 144 probable refs over 72 entities, two each).
	survivor := seedOrphanCreditName(t, "掛谷")
	seedVNDBRef(t, model.EntityTypeCreditName, survivor, "40", model.LinkKindProbable)
	seedVNDBRef(t, model.EntityTypeCreditName, survivor, "99", model.LinkKindProbable)
	// alias 41 already imported normally (exact) AND carries a stray probable on
	// another entity — exactness decides which link resolves, not whether the id
	// is free, so this must NOT be counted as claimed.
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

	// No duplicate body, and no fresh exact anchor squatting id 40.
	assert.Equal(t, int64(3), scalarInt(t, `SELECT count(*) FROM catalog_credit_name`), "no duplicate minted")
	assert.Zero(t, scalarInt(t, `SELECT count(*) FROM catalog_external_ref
		WHERE entity_type=1 AND source_id=2 AND external_id='40' AND link_kind=0`), "id 40 never re-claimed as exact")
	// The probable ref is untouched: re-grading is an adjudication, not an import.
	assert.Equal(t, int64(1), scalarIntA(t, `SELECT count(*) FROM catalog_external_ref
		WHERE entity_type=1 AND source_id=2 AND external_id='40' AND link_kind=? AND entity_id=?`,
		model.LinkKindProbable, survivor))
	// alias 40's credit is dropped whole; alias 41's resumes onto its exact holder.
	assert.Equal(t, int64(1), scalarInt(t, `SELECT count(*) FROM catalog_credit`))
	assert.Equal(t, int64(1), scalarIntA(t, `SELECT count(*) FROM catalog_credit WHERE work_id=? AND credit_name_id=?`, work, imported))
}

// TestVNDBCreditsWave_VAResolutionIsLivenessAware pins the character axis: a VA
// credit is dropped — never mis-resolved and never crashed on — when the cid's
// holder is alive-but-probable or retired, and each reason gets its own counter
// so an unattended run reports which follow-up it needs.
func TestVNDBCreditsWave_VAResolutionIsLivenessAware(t *testing.T) {
	clean(t)
	work := seedVNDBWork(t, "v400")
	seedVNDBStaff(t, "s5", "ja")
	seedVNDBAlias(t, 50, "s5", "声優", "Seiyuu")

	alive := seedVNDBCharAnchor(t, "cA") // alive exact anchor → resolves
	// cB: alive character, source-2 ref only at probable grade (merge survivor).
	claimed := seedVNDBCharAnchor(t, "cB")
	require.NoError(t, testDB.Exec(`UPDATE catalog_external_ref SET link_kind = ?
		WHERE entity_type=4 AND source_id=2 AND external_id='cB'`, model.LinkKindProbable).Error)
	// cC: exact anchor whose holder is soft-deleted — it still squats the
	// non-liveness-aware exact identity index.
	retired := seedVNDBCharAnchor(t, "cC")
	require.NoError(t, testDB.Exec(`UPDATE catalog_character SET deleted_at = now() WHERE id = ?`, retired).Error)
	// cD: soft-deleted holder at PROBABLE grade — the id really is free again, so
	// this is an ordinary "roster never imported it" miss, not a claim.
	freed := seedVNDBCharAnchor(t, "cD")
	require.NoError(t, testDB.Exec(`UPDATE catalog_external_ref SET link_kind = ?
		WHERE entity_type=4 AND source_id=2 AND external_id='cD'`, model.LinkKindProbable).Error)
	require.NoError(t, testDB.Exec(`UPDATE catalog_character SET deleted_at = now() WHERE id = ?`, freed).Error)

	seedVNSeiyuu(t, "v400", "cA", 50, "")
	seedVNSeiyuu(t, "v400", "cB", 50, "")
	seedVNSeiyuu(t, "v400", "cC", 50, "")
	seedVNSeiyuu(t, "v400", "cD", 50, "")
	seedVNSeiyuu(t, "v400", "cE", 50, "") // never imported at all

	st, err := New(testDB, nil, Options{Source: "vndb"}).Run("vndb")
	require.NoError(t, err)
	assert.Zero(t, st.Errors)
	assert.Equal(t, 1, st.SkippedClaimedProbableChar, "cB only")
	assert.Equal(t, 1, st.SkippedRetiredExactChar, "cC only")
	assert.Zero(t, st.SkippedClaimedProbableName, "the alias itself is unclaimed")
	assert.Equal(t, 1, st.CreditsWritten, "only cA's VA credit survives")
	assert.Equal(t, int64(1), scalarIntA(t, `SELECT count(*) FROM catalog_credit WHERE work_id=? AND character_id=?`, work, alive))
	// Nothing was hung off the demoted or retired characters.
	assert.Zero(t, scalarIntA(t, `SELECT count(*) FROM catalog_credit WHERE character_id IN (?,?,?)`, claimed, retired, freed))
}

// TestVNDBCreditsWave covers the fresh-import path: exact-work-anchor gate, the
// seeded role map (mapped vs unmapped role), orphan credit names + self anchors,
// eid collapse, VA credits routed through roster char anchors (with the
// no-anchor skip), note passthrough, source_id=2, and idempotency.
func TestVNDBCreditsWave(t *testing.T) {
	clean(t)
	work := seedVNDBWork(t, "v100") // in gate (source-2 exact anchor)
	// v999 is NOT anchored → its vn_staff row is out of gate (never loaded).

	seedVNDBStaff(t, "s1", "ja")
	seedVNDBStaff(t, "s2", "en")
	seedVNDBAlias(t, 10, "s1", "織田薫", "Oda Kaoru")
	seedVNDBAlias(t, 20, "s2", "OdaKaoru", "")

	seedVNStaff(t, "v100", 10, "scenario", "")       // → role 247
	seedVNStaff(t, "v100", 10, "director", "")       // → role 173 (same alias, 2nd credit)
	seedVNStaffEid(t, "v100", 10, "scenario", 5, "") // duplicate work-level credit (eid ignored) → collapses
	// A role with no map row → skip. Every real VNDB role now maps (step 80 gave
	// translator/editor/qa reserved-band slots 3/4/5), so this stands in for a
	// hypothetical future role, still exercising the unmapped-skip path.
	seedVNStaff(t, "v100", 20, "future-role", "") // UNMAPPED → skip
	seedVNStaff(t, "v999", 10, "music", "")       // out of gate → not loaded

	c50 := seedVNDBCharAnchor(t, "c50")      // anchored char → VA resolves
	seedVNSeiyuu(t, "v100", "c50", 20, "主演") // VA credit (alias 20, note passthrough)
	seedVNSeiyuu(t, "v100", "c999", 10, "")  // c999 has no anchor → skip

	// Dry run: plan counts (scenario×2 + director + VA = 4 plans; the future-role
	// row unmapped; c999 VA skipped). 2 credit names to create (aid 10 + 20).
	dry, err := New(testDB, nil, Options{Source: "vndb", DryRun: true}).Run("vndb")
	require.NoError(t, err)
	assert.Equal(t, 2, dry.NamesCreated)
	assert.Equal(t, 4, dry.CreditsWritten, "would-be plans")
	assert.Equal(t, 1, dry.SkippedUnmappedRole)
	assert.Zero(t, scalarInt(t, `SELECT count(*) FROM catalog_credit`), "dry writes nothing")

	// Apply: the two eid-differing scenario rows collapse → 3 distinct credits.
	st, err := New(testDB, nil, Options{Source: "vndb"}).Run("vndb")
	require.NoError(t, err)
	assert.Equal(t, 2, st.NamesCreated)
	assert.Equal(t, 3, st.CreditsWritten, "scenario eid-dup collapsed")
	assert.Equal(t, 1, st.Already, "the collapsed duplicate")
	assert.Equal(t, 1, st.SkippedUnmappedRole)
	assert.Zero(t, st.Errors)

	// All three credits are source-2, on the in-gate work.
	assert.Equal(t, int64(3), scalarInt(t, `SELECT count(*) FROM catalog_credit WHERE source_id=2`))
	assert.Equal(t, int64(3), scalarInt(t, fmt.Sprintf(`SELECT count(*) FROM catalog_credit WHERE work_id=%d`, work)))

	// Orphan discipline: exactly 2 credit names, all person_id NULL, zero persons.
	assert.Equal(t, int64(2), scalarInt(t, `SELECT count(*) FROM catalog_credit_name`))
	assert.Zero(t, scalarInt(t, `SELECT count(*) FROM catalog_credit_name WHERE person_id IS NOT NULL`))
	assert.Zero(t, scalarInt(t, `SELECT count(*) FROM catalog_person`))

	// Self anchors keyed by the alias aid, with the wave's rule.
	assert.Equal(t, int64(2), scalarInt(t, `SELECT count(*) FROM catalog_external_ref
		WHERE entity_type=1 AND source_id=2 AND link_kind=0 AND matched_by='rule:vndb-staff-import' AND external_id IN ('10','20')`))
	// Credit name content: native name + owning staff's language.
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

	// Role mapping: scenario→247, director→173, both charcter-less.
	assert.Equal(t, int64(1), scalarInt(t, fmt.Sprintf(`SELECT count(*) FROM catalog_credit WHERE work_id=%d AND role_id=247 AND character_id IS NULL`, work)))
	assert.Equal(t, int64(1), scalarInt(t, fmt.Sprintf(`SELECT count(*) FROM catalog_credit WHERE work_id=%d AND role_id=173 AND character_id IS NULL`, work)))

	// VA credit: voice-actor role, the resolved character, note passthrough.
	assert.Equal(t, int64(1), scalarInt(t, fmt.Sprintf(`SELECT count(*) FROM catalog_credit WHERE work_id=%d AND role_id=1 AND character_id=%d`, work, c50)))
	var vaNote string
	require.NoError(t, testDB.Raw(fmt.Sprintf(`SELECT note FROM catalog_credit WHERE role_id=1 AND character_id=%d`, c50)).Scan(&vaNote).Error)
	assert.Equal(t, "主演", vaNote)

	// Imported revisions: one per created credit name.
	assert.Equal(t, int64(2), scalarInt(t, fmt.Sprintf(`SELECT count(*) FROM catalog_revision WHERE action=%d AND entity_type=1`, model.RevisionActionImported)))

	// Idempotency: a second apply writes nothing. Already counts every
	// materialized plan row that conflicted (4 = the 3 distinct credits + the
	// still-present eid duplicate), so the real signal is CreditsWritten == 0.
	st2, err := New(testDB, nil, Options{Source: "vndb"}).Run("vndb")
	require.NoError(t, err)
	assert.Zero(t, st2.NamesCreated)
	assert.Zero(t, st2.CreditsWritten)
	assert.Equal(t, 4, st2.Already)
	assert.Equal(t, int64(3), scalarInt(t, `SELECT count(*) FROM catalog_credit WHERE source_id=2`), "still exactly 3 credits")
}

// The staff catch-all refines by note at plan time (staffnotes.go): a noted
// engine credit lands on 程序, an unmapped note stays in 其他, and every
// refinement target must be a seeded vocabulary row — a typo'd id here would
// otherwise only surface as an FK error mid-import.
func TestVNDBCreditsWave_RefinesStaffNotes(t *testing.T) {
	clean(t)
	work := seedVNDBWork(t, "v200")
	seedVNDBStaff(t, "s3", "ja")
	seedVNDBAlias(t, 30, "s3", "かつらぎ", "")
	seedVNDBAlias(t, 31, "s3", "ムービー屋", "")
	seedVNDBAlias(t, 32, "s3", "多芸な人", "")
	seedVNStaff(t, "v200", 30, "staff", "Programming")      // refined → 程序
	seedVNStaff(t, "v200", 31, "staff", "Movie assistance") // unmapped note → 其他
	seedVNStaff(t, "v200", 32, "staff", "Planning, script") // composite → 企画 + 程序

	st, err := New(testDB, nil, Options{Source: "vndb"}).Run("vndb")
	require.NoError(t, err)
	assert.Equal(t, 4, st.CreditsWritten, "composite note plans one credit per position")

	assert.Equal(t, int64(1), scalarInt(t, fmt.Sprintf(
		`SELECT count(*) FROM catalog_credit WHERE work_id=%d AND role_id=238 AND note='Programming'`, work)))
	assert.Equal(t, int64(1), scalarInt(t, fmt.Sprintf(
		`SELECT count(*) FROM catalog_credit WHERE work_id=%d AND role_id=2 AND note='Movie assistance'`, work)))

	// The composite row lands as two credits sharing the verbatim note.
	assert.Equal(t, int64(2), scalarInt(t, fmt.Sprintf(
		`SELECT count(*) FROM catalog_credit WHERE work_id=%d AND note='Planning, script' AND role_id IN (291,238)`, work)))

	for note, roleID := range StaffNoteRoleTable() {
		assert.Equal(t, int64(1), scalarInt(t, fmt.Sprintf(
			`SELECT count(*) FROM catalog_role WHERE id=%d`, roleID)),
			"note %q targets a role id absent from the seeded vocabulary", note)
	}
}
