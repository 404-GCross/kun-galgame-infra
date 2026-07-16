package importer

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedVNDBWork creates a claimed work + an EXACT VNDB work anchor whose
// external_id is the VNDB vid VERBATIM ("v100") — the roster gate. The claim's
// product_work_id is derived from the vid so distinct works never collide on
// uq_catalog_work_claim.
func seedVNDBWork(t *testing.T, vid string) int64 {
	t.Helper()
	pwid, err := strconv.ParseInt(strings.TrimPrefix(vid, "v"), 10, 64)
	require.NoError(t, err)
	var workID int64
	require.NoError(t, testDB.Raw(`INSERT INTO catalog_work (medium_id, site, product_work_id, olang, display_name, content_rating, status, extra, field_provenance)
		VALUES (1,'galgame_wiki',?,'ja','w',0,0,'{}','{}') RETURNING id`, pwid).Scan(&workID).Error)
	require.NoError(t, testDB.Exec(`INSERT INTO catalog_external_ref (entity_type, entity_id, source_id, external_id, link_kind, matched_by)
		VALUES (5, ?, 2, ?, 0, 'rule:test-vndb-work-anchor')`, workID, vid).Error)
	return workID
}

func seedVNDBChar(t *testing.T, id, native, romaji, image string) {
	t.Helper()
	require.NoError(t, testDB.Exec(`INSERT INTO src_vndb.chars (id, image, sex, gender, main, main_spoil, description, ingested_at)
		VALUES (?,?,'','','',0,'',now())`, id, image).Error)
	require.NoError(t, testDB.Exec(`INSERT INTO src_vndb.chars_names (id, lang, name, latin) VALUES (?, 'ja', ?, ?)`, id, native, romaji).Error)
}

func seedVNDBImage(t *testing.T, id string, sexual, violence int16) {
	t.Helper()
	require.NoError(t, testDB.Exec(`INSERT INTO src_vndb.images (id, width, height, c_votecount, c_sexual_avg, c_sexual_stddev, c_violence_avg, c_violence_stddev, c_weight)
		VALUES (?,250,300,5,?,0,?,0,1)`, id, sexual, violence).Error)
}

func seedCharVN(t *testing.T, charID, vid, role string, spoil int16) {
	t.Helper()
	require.NoError(t, testDB.Exec(`INSERT INTO src_vndb.chars_vns (id, vid, rid, role, spoil) VALUES (?,?,'',?,?)`, charID, vid, role, spoil).Error)
}

// TestRosterVNDBWave covers the fresh-import path: gate by exact VNDB work
// anchor, kind + spoiler mapping, native display_name + romaji spelling_variant
// alias, the portrait-backfill threshold, zero persons, and idempotency.
func TestRosterVNDBWave(t *testing.T) {
	clean(t)
	work := seedVNDBWork(t, "v100")
	// c3 lives only on the un-anchored v999 → out of gate (never loaded).
	seedVNDBChar(t, "c1", "主人公", "Shujinkou", "ch1")
	seedVNDBChar(t, "c2", "悪役", "Akuyaku", "ch2")
	seedVNDBChar(t, "c3", "端役", "Hayaku", "")
	seedVNDBImage(t, "ch1", 0, 0)   // qualifies (safe)
	seedVNDBImage(t, "ch2", 200, 0) // does NOT qualify (explicit)
	seedCharVN(t, "c1", "v100", "main", 0)
	seedCharVN(t, "c2", "v100", "appears", 2)
	seedCharVN(t, "c3", "v999", "main", 0)

	st, err := New(testDB, nil, Options{Source: "vndb"}).RunRoster("vndb")
	require.NoError(t, err)
	assert.Equal(t, 2, st.CharactersCreated, "c1+c2 created; c3 out of gate")
	assert.Zero(t, st.AttachedExisting)
	assert.Equal(t, 2, st.EdgesWritten)
	assert.Equal(t, 1, st.PortraitCandidates, "only c1's portrait clears the threshold")
	assert.Zero(t, st.SkippedNoWorkAnchor)
	assert.Zero(t, st.Errors)

	// kind: main→main(1), appears→appears(3). spoiler: from chars_vns.spoil.
	c1 := vndbCharID(t, "c1")
	c2 := vndbCharID(t, "c2")
	assert.Equal(t, int64(model.WorkCharacterKindMain), scalarInt(t, fmt.Sprintf(`SELECT kind FROM catalog_work_character WHERE work_id=%d AND character_id=%d`, work, c1)))
	assert.Equal(t, int64(model.WorkCharacterKindAppears), scalarInt(t, fmt.Sprintf(`SELECT kind FROM catalog_work_character WHERE work_id=%d AND character_id=%d`, work, c2)))
	assert.Equal(t, int64(model.SpoilerNone), scalarInt(t, fmt.Sprintf(`SELECT spoiler FROM catalog_work_character WHERE work_id=%d AND character_id=%d`, work, c1)))
	assert.Equal(t, int64(model.SpoilerSevere), scalarInt(t, fmt.Sprintf(`SELECT spoiler FROM catalog_work_character WHERE work_id=%d AND character_id=%d`, work, c2)))

	// New entity: display_name = native, self anchor rule, edge provenance.
	var dn string
	require.NoError(t, testDB.Raw(`SELECT display_name FROM catalog_character WHERE id=?`, c1).Scan(&dn).Error)
	assert.Equal(t, "主人公", dn)
	assert.Equal(t, int64(1), scalarInt(t, `SELECT count(*) FROM catalog_external_ref WHERE entity_type=4 AND source_id=2 AND external_id='c1' AND link_kind=0 AND matched_by='rule:vndb-character-import'`))
	assert.Equal(t, int64(1), scalarInt(t, `SELECT count(*) FROM catalog_work_character WHERE matched_by='import:character-roster-vndb' AND character_id=`+fmt.Sprint(c1)))
	// romaji → spelling_variant alias.
	assert.Equal(t, int64(1), scalarInt(t, fmt.Sprintf(`SELECT count(*) FROM catalog_character_alias WHERE character_id=%d AND name='Shujinkou' AND kind=%d`, c1, model.AliasKindSpellingVariant)))

	// Portrait backfill: c1 only (c2 explicit), keyed by its catalog character id.
	assert.Equal(t, int64(1), scalarInt(t, `SELECT count(*) FROM src_vndb.portrait_backfill`))
	assert.Equal(t, int64(1), scalarInt(t, fmt.Sprintf(`SELECT count(*) FROM src_vndb.portrait_backfill WHERE catalog_character_id=%d AND image_id='ch1'`, c1)))

	// Identity discipline: no persons; c3 never created.
	assert.Zero(t, scalarInt(t, `SELECT count(*) FROM catalog_person`))
	assert.Zero(t, scalarInt(t, `SELECT count(*) FROM catalog_external_ref WHERE entity_type=4 AND source_id=2 AND external_id='c3'`))

	// Idempotency: a second run writes nothing.
	st2, err := New(testDB, nil, Options{Source: "vndb"}).RunRoster("vndb")
	require.NoError(t, err)
	assert.Zero(t, st2.CharactersCreated)
	assert.Zero(t, st2.AttachedExisting)
	assert.Zero(t, st2.EdgesWritten)
	assert.Zero(t, st2.AliasesCreated)
	assert.Equal(t, 2, st2.Already)
}

// TestRosterVNDBAttach proves the same-work same-name rule: a VNDB character
// whose romaji equals an existing entity's alias on a shared work ATTACHES to
// that entity (no new row) — the edge on the shared work keeps the first
// source's kind (not overwritten), while a NEW work gets a fresh edge.
func TestRosterVNDBAttach(t *testing.T) {
	clean(t)
	shared := seedVNDBWork(t, "v200") // existing entity already has an edge here
	fresh := seedVNDBWork(t, "v201")  // no existing edge here

	// A pre-existing (Bangumi-style) character on the shared work: display_name
	// differs from VNDB (space + 铃/鈴), but its romaji alias matches VNDB's.
	var existing int64
	require.NoError(t, testDB.Raw(`INSERT INTO catalog_character (display_name, lang, description, field_provenance)
		VALUES ('神尾観铃','ja','','{}') RETURNING id`).Scan(&existing).Error)
	require.NoError(t, testDB.Exec(`INSERT INTO catalog_external_ref (entity_type, entity_id, source_id, external_id, link_kind, matched_by)
		VALUES (4, ?, 3, '2', 0, 'rule:bangumi-character-import')`, existing).Error)
	require.NoError(t, testDB.Exec(`INSERT INTO catalog_character_alias (character_id, name, lang, kind, is_primary_for_locale)
		VALUES (?, 'Kamio Misuzu', 'ja', ?, false)`, existing, model.AliasKindSpellingVariant).Error)
	require.NoError(t, testDB.Exec(`INSERT INTO catalog_work_character (work_id, character_id, kind, spoiler, matched_by, created_at, updated_at)
		VALUES (?, ?, ?, 0, 'import:character-roster-bangumi', now(), now())`, shared, existing, model.WorkCharacterKindSecondary).Error)

	// VNDB char c50 shares the native/romaji and appears on both works.
	seedVNDBChar(t, "c50", "神尾 観鈴", "Kamio Misuzu", "")
	seedCharVN(t, "c50", "v200", "primary", 0) // primary → main(1); conflicts on shared
	seedCharVN(t, "c50", "v201", "primary", 0) // fresh work → new edge

	st, err := New(testDB, nil, Options{Source: "vndb"}).RunRoster("vndb")
	require.NoError(t, err)
	assert.Zero(t, st.CharactersCreated, "matched an existing entity → no new row")
	assert.Equal(t, 1, st.AttachedExisting)
	assert.Equal(t, 1, st.EdgesWritten, "fresh work edge only")
	assert.Equal(t, 1, st.Already, "shared-work edge already exists → not overwritten")

	// The VNDB self-anchor points at the EXISTING entity via the attach rule.
	assert.Equal(t, existing, scalarInt(t, `SELECT entity_id FROM catalog_external_ref WHERE entity_type=4 AND source_id=2 AND external_id='c50' AND matched_by='rule:same-work-character-name'`))
	// Still exactly one catalog character (no duplicate).
	assert.Equal(t, int64(1), scalarInt(t, `SELECT count(*) FROM catalog_character`))
	// Shared-work edge kept the first source's kind (secondary), NOT VNDB's main.
	assert.Equal(t, int64(model.WorkCharacterKindSecondary), scalarInt(t, fmt.Sprintf(`SELECT kind FROM catalog_work_character WHERE work_id=%d AND character_id=%d`, shared, existing)))
	// Fresh work got a new edge pointing at the same entity, kind=main(1) from VNDB.
	assert.Equal(t, int64(model.WorkCharacterKindMain), scalarInt(t, fmt.Sprintf(`SELECT kind FROM catalog_work_character WHERE work_id=%d AND character_id=%d`, fresh, existing)))

	// Idempotency: a second run resolves via the anchor and writes nothing.
	st2, err := New(testDB, nil, Options{Source: "vndb"}).RunRoster("vndb")
	require.NoError(t, err)
	assert.Zero(t, st2.CharactersCreated)
	assert.Zero(t, st2.AttachedExisting)
	assert.Zero(t, st2.EdgesWritten)
	assert.Equal(t, 2, st2.Already)
}

func vndbCharID(t *testing.T, extID string) int64 {
	t.Helper()
	return scalarInt(t, `SELECT entity_id FROM catalog_external_ref WHERE entity_type=4 AND source_id=2 AND external_id='`+extID+`'`)
}
