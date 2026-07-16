package importer

import (
	"fmt"
	"testing"

	"api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedExactWork creates a claimed work + an EXACT external anchor for one
// source — the roster gate. Returns the new work id.
func seedExactWork(t *testing.T, source int16, extID int64) int64 {
	t.Helper()
	var workID int64
	require.NoError(t, testDB.Raw(`INSERT INTO catalog_work (medium_id, site, product_work_id, olang, display_name, content_rating, status, extra, field_provenance)
		VALUES (1,'galgame_wiki',?, 'ja','w',0,0,'{}','{}') RETURNING id`, extID).Scan(&workID).Error)
	require.NoError(t, testDB.Exec(`INSERT INTO catalog_external_ref (entity_type, entity_id, source_id, external_id, link_kind, matched_by)
		VALUES (5, ?, ?, ?, 0, 'rule:test-work-anchor')`, workID, source, fmt.Sprint(extID)).Error)
	return workID
}

func seedBangumiChar(t *testing.T, id int64, name string) {
	t.Helper()
	require.NoError(t, testDB.Exec(`INSERT INTO src_bangumi.character (id, role, name, infobox_raw, parse_error, summary, comments, collects, parser_version, ingested_at)
		VALUES (?,1,?,'','','',0,0,'v',now())`, id, name).Error)
}

func TestRosterBangumiWave(t *testing.T) {
	clean(t)
	work := seedExactWork(t, 3, 100) // exact-anchored bangumi subject 100
	// subject 999 is NOT anchored → its roster row is out of gate.

	seedBangumiChar(t, 50, "渚")  // main
	seedBangumiChar(t, 51, "モブ") // type 4 → unknown
	seedBangumiChar(t, 52, "秋生") // only on the un-anchored subject 999
	require.NoError(t, testDB.Exec(`INSERT INTO src_bangumi.subject_character (character_id, subject_id, type, item_order)
		VALUES (50,100,1,0),(51,100,4,1),(52,999,1,0)`).Error)

	st, err := New(testDB, nil, Options{Source: "bangumi"}).RunRoster("bangumi")
	require.NoError(t, err)
	assert.Equal(t, 2, st.CharactersCreated, "chars 50+51 created; 52 out of gate")
	assert.Equal(t, 2, st.EdgesWritten)
	// Bangumi is query-gated (JOIN to exact work anchors), so the un-anchored
	// subject 999 is never loaded — the gate is proven below by char 52 never
	// being created, not by a skip counter.
	assert.Zero(t, st.SkippedNoWorkAnchor)
	assert.Zero(t, st.Errors)

	// Kind mapping: type 1 → main(1), type 4 → unknown(0).
	assert.Equal(t, int64(model.WorkCharacterKindMain),
		scalarInt(t, fmt.Sprintf(`SELECT kind FROM catalog_work_character wc
			JOIN catalog_external_ref r ON r.entity_type=4 AND r.entity_id=wc.character_id AND r.source_id=3 AND r.external_id='50'
			WHERE wc.work_id=%d`, work)))
	assert.Equal(t, int64(model.WorkCharacterKindUnknown),
		scalarInt(t, fmt.Sprintf(`SELECT kind FROM catalog_work_character wc
			JOIN catalog_external_ref r ON r.entity_type=4 AND r.entity_id=wc.character_id AND r.source_id=3 AND r.external_id='51'
			WHERE wc.work_id=%d`, work)))

	// New character carries a self exact anchor with the step-13 rule.
	var anchorRule string
	require.NoError(t, testDB.Raw(`SELECT matched_by FROM catalog_external_ref WHERE entity_type=4 AND source_id=3 AND external_id='50'`).Scan(&anchorRule).Error)
	assert.Equal(t, "rule:bangumi-character-import", anchorRule)

	// Edge provenance is the 'import:<job>' form.
	var mb string
	require.NoError(t, testDB.Raw(`SELECT matched_by FROM catalog_work_character LIMIT 1`).Scan(&mb).Error)
	assert.Equal(t, "import:character-roster-bangumi", mb)

	// Identity discipline: no persons, no credits — roster is independent.
	assert.Zero(t, scalarInt(t, `SELECT count(*) FROM catalog_person`), "no person rows")
	assert.Zero(t, scalarInt(t, `SELECT count(*) FROM catalog_credit`), "roster does not touch credits")
	// Char 52 (out of gate) was never created.
	assert.Zero(t, scalarInt(t, `SELECT count(*) FROM catalog_external_ref WHERE entity_type=4 AND source_id=3 AND external_id='52'`))

	// Idempotency: a second run writes nothing.
	st2, err := New(testDB, nil, Options{Source: "bangumi"}).RunRoster("bangumi")
	require.NoError(t, err)
	assert.Zero(t, st2.CharactersCreated)
	assert.Zero(t, st2.EdgesWritten)
	assert.Equal(t, 2, st2.Already, "both edges already present")
}

func TestRosterEGWave(t *testing.T) {
	clean(t)
	work := seedExactWork(t, 5, 7) // exact EG anchor for game 7
	// game 8 is NOT anchored.
	testDB.Exec(`INSERT INTO characters (id, raw) VALUES (800,'{"name":"EGキャラ"}'),(801,'{"name":"別キャラ"}')`)
	testDB.Exec(`INSERT INTO appearances (game, character_id) VALUES (7,800),(8,801)`)

	st, err := New(testDB, testDB, Options{Source: "eg"}).RunRoster("eg")
	require.NoError(t, err)
	assert.Equal(t, 1, st.CharactersCreated)
	assert.Equal(t, 1, st.EdgesWritten)
	assert.Equal(t, 1, st.SkippedNoWorkAnchor, "game 8 out of gate")

	// EG has no main/supporting → kind=unknown(0); provenance is the EG job.
	assert.Equal(t, int64(model.WorkCharacterKindUnknown),
		scalarInt(t, fmt.Sprintf(`SELECT kind FROM catalog_work_character WHERE work_id=%d`, work)))
	var mb string
	require.NoError(t, testDB.Raw(`SELECT matched_by FROM catalog_work_character WHERE work_id=?`, work).Scan(&mb).Error)
	assert.Equal(t, "import:character-roster-eg", mb)
	// The created character carries the step-13 EG rule anchor.
	assert.Equal(t, int64(1), scalarInt(t, `SELECT count(*) FROM catalog_external_ref WHERE entity_type=4 AND source_id=5 AND external_id='800' AND link_kind=0 AND matched_by='rule:eg-character-import'`))

	// Idempotency.
	st2, err := New(testDB, testDB, Options{Source: "eg"}).RunRoster("eg")
	require.NoError(t, err)
	assert.Zero(t, st2.CharactersCreated)
	assert.Zero(t, st2.EdgesWritten)
	assert.Equal(t, 1, st2.Already)
}

// TestRosterReusesExistingCharacter proves the 补建 path's inverse: a character
// that already has an exact anchor (e.g. created by the step-13 credit wave) is
// NOT re-created — the roster edge reuses it.
func TestRosterReusesExistingCharacter(t *testing.T) {
	clean(t)
	work := seedExactWork(t, 3, 100)
	// Pre-create a catalog character + its self anchor for bangumi char 60.
	var charID int64
	require.NoError(t, testDB.Raw(`INSERT INTO catalog_character (display_name, lang, description, field_provenance)
		VALUES ('既存','ja','','{}') RETURNING id`).Scan(&charID).Error)
	require.NoError(t, testDB.Exec(`INSERT INTO catalog_external_ref (entity_type, entity_id, source_id, external_id, link_kind, matched_by)
		VALUES (4, ?, 3, '60', 0, 'rule:bangumi-character-import')`, charID).Error)

	seedBangumiChar(t, 60, "既存")
	require.NoError(t, testDB.Exec(`INSERT INTO src_bangumi.subject_character (character_id, subject_id, type, item_order) VALUES (60,100,2,0)`).Error)

	st, err := New(testDB, nil, Options{Source: "bangumi"}).RunRoster("bangumi")
	require.NoError(t, err)
	assert.Zero(t, st.CharactersCreated, "existing character reused, not recreated")
	assert.Equal(t, 1, st.EdgesWritten)
	// Exactly one character row for bangumi char 60 (no duplicate).
	assert.Equal(t, int64(1), scalarInt(t, `SELECT count(*) FROM catalog_external_ref WHERE entity_type=4 AND source_id=3 AND external_id='60'`))
	// Edge points at the pre-existing character with kind=secondary (type 2).
	assert.Equal(t, int64(model.WorkCharacterKindSecondary),
		scalarInt(t, fmt.Sprintf(`SELECT kind FROM catalog_work_character WHERE work_id=%d AND character_id=%d`, work, charID)))
}
