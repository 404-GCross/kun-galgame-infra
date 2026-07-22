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

	seedVNStaff(t, "v100", 10, "scenario", "")     // → role 247
	seedVNStaff(t, "v100", 10, "director", "")     // → role 173 (same alias, 2nd credit)
	seedVNStaffEid(t, "v100", 10, "scenario", 5, "") // duplicate work-level credit (eid ignored) → collapses
	seedVNStaff(t, "v100", 20, "translator", "")   // UNMAPPED → skip
	seedVNStaff(t, "v999", 10, "music", "")        // out of gate → not loaded

	c50 := seedVNDBCharAnchor(t, "c50") // anchored char → VA resolves
	seedVNSeiyuu(t, "v100", "c50", 20, "主演")        // VA credit (alias 20, note passthrough)
	seedVNSeiyuu(t, "v100", "c999", 10, "")          // c999 has no anchor → skip

	// Dry run: plan counts (scenario×2 + director + VA = 4 plans; translator
	// unmapped; c999 VA skipped). 2 credit names to create (aid 10 + 20).
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
