package importer

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scalarIntA is scalarInt with query args (the shared scalarInt is arg-less).
func scalarIntA(t *testing.T, q string, args ...any) int64 {
	t.Helper()
	var n int64
	require.NoError(t, testDB.Raw(q, args...).Scan(&n).Error)
	return n
}

// seedEGRosettaWork mints a gated EG work (source-5 rosetta ref) for a game id.
func seedEGRosettaWork(t *testing.T, game int64) int64 {
	t.Helper()
	var workID int64
	require.NoError(t, testDB.Raw(`INSERT INTO catalog_work (medium_id, site, product_work_id, olang, display_name, content_rating, status, extra, field_provenance, display_nsfw)
		VALUES (1,'galgame_wiki',?, 'ja','w',0,0,'{}','{}',false) RETURNING id`, game).Scan(&workID).Error)
	require.NoError(t, testDB.Exec(`INSERT INTO catalog_external_ref (entity_type, entity_id, source_id, external_id, link_kind, matched_by)
		VALUES (5, ?, 5, ?, 1, 'rule:eg-vndb-rosetta')`, workID, strconv.FormatInt(game, 10)).Error)
	return workID
}

// TestEGMusicWave exercises the four song-credit families: the rosetta gate,
// work-level dedup across songs/edges, canonical-name credit names, the fixed
// per-table roles, the featuring/no_anchor counts, and idempotency.
func TestEGMusicWave(t *testing.T) {
	clean(t)

	workA := seedEGRosettaWork(t, 7)
	workB := seedEGRosettaWork(t, 8)
	// game 9 is ungated (no rosetta ref).

	// creaters: 500 sings + composes, 501 writes lyrics, 502 arranges, 503 sings
	// only an ungated song.
	testDB.Exec(`INSERT INTO creaters (id, raw) VALUES
		(500,'{"name":"歌作曲家"}'), (501,'{"name":"作詞家"}'), (502,'{"name":"編曲家"}'), (503,'{"name":"圏外歌手"}')`)

	// game_music: song 10 → games 7 & 8 (works A & B); song 11 → game 8 (B);
	// song 12 → game 9 (ungated).
	testDB.Exec(`INSERT INTO game_music (music, game) VALUES (10,7),(10,8),(11,8),(12,9)`)

	// singers: 500 on song 10 (→ A,B) and song 11 (→ B, a work-level dup of B);
	// the song-11 row is featuring. 503 on the ungated song 12.
	testDB.Exec(`INSERT INTO singers (raw, music, creater_id) VALUES
		('{"featuring":false}',10,500), ('{"featuring":true}',11,500), ('{"featuring":false}',12,503)`)
	// composers: 500 on song 10 (→ A,B) — same person, different role → own credits.
	testDB.Exec(`INSERT INTO composers (raw, music, creater_id) VALUES ('{"featuring":false}',10,500)`)
	// lyricists: 501 on song 10 (→ A,B).
	testDB.Exec(`INSERT INTO lyricists (raw, music, creater_id) VALUES ('{"featuring":false}',10,501)`)
	// arrangers: 502 on song 11 (→ B).
	testDB.Exec(`INSERT INTO arrangers (raw, music, creater_id) VALUES ('{"featuring":false}',11,502)`)

	st, err := New(testDB, testDB, Options{Source: "eg-music"}).Run("eg-music")
	require.NoError(t, err)
	assert.Equal(t, 3, st.NamesCreated, "creaters 500/501/502 (503 gated out, never created)")
	assert.Equal(t, 7, st.CreditsWritten, "singer 2 + composer 2 + lyric 2 + arrange 1")
	assert.Zero(t, st.Errors)

	// Per-role credit counts by the fixed role ids.
	assert.EqualValues(t, 2, scalarInt(t, `SELECT count(*) FROM catalog_credit WHERE source_id=5 AND role_id=286`), "vocal: (A,500)(B,500)")
	assert.EqualValues(t, 2, scalarInt(t, `SELECT count(*) FROM catalog_credit WHERE source_id=5 AND role_id=158`), "composer: (A,500)(B,500)")
	assert.EqualValues(t, 2, scalarInt(t, `SELECT count(*) FROM catalog_credit WHERE source_id=5 AND role_id=199`), "lyric: (A,501)(B,501)")
	assert.EqualValues(t, 1, scalarInt(t, `SELECT count(*) FROM catalog_credit WHERE source_id=5 AND role_id=115`), "arrange: (B,502)")

	// Work B carries three roles for creater 500's name (vocal+composer) etc.;
	// spot-check that work A has exactly one vocal credit for 500.
	assert.EqualValues(t, 1, scalarIntA(t, `SELECT count(*) FROM catalog_credit c
		JOIN catalog_external_ref r ON r.entity_type=1 AND r.entity_id=c.credit_name_id AND r.source_id=5 AND r.external_id='500'
		WHERE c.work_id=?  AND c.role_id=286`, workA))
	assert.EqualValues(t, 1, scalarIntA(t, `SELECT count(*) FROM catalog_credit c
		JOIN catalog_external_ref r ON r.entity_type=1 AND r.entity_id=c.credit_name_id AND r.source_id=5 AND r.external_id='500'
		WHERE c.work_id=? AND c.role_id=286`, workB))

	// Canonical credit name (not the inline nominal form), orphan, self-anchored.
	assert.EqualValues(t, "歌作曲家", scalarStr(t, `SELECT cn.name FROM catalog_credit_name cn
		JOIN catalog_external_ref r ON r.entity_type=1 AND r.entity_id=cn.id AND r.source_id=5 AND r.external_id='500' WHERE cn.person_id IS NULL`))
	assert.EqualValues(t, "rule:eg-creater-import", scalarStr(t, `SELECT matched_by FROM catalog_external_ref WHERE entity_type=1 AND source_id=5 AND external_id='500'`))
	// The ungated creater 503 was never created.
	assert.Zero(t, scalarInt(t, `SELECT count(*) FROM catalog_external_ref WHERE entity_type=1 AND source_id=5 AND external_id='503'`))
	// No music credit carries a character or label.
	assert.Zero(t, scalarInt(t, `SELECT count(*) FROM catalog_credit WHERE source_id=5 AND (character_id IS NOT NULL OR label_id IS NOT NULL)`))

	// Idempotency: a second run writes nothing.
	st2, err := New(testDB, testDB, Options{Source: "eg-music"}).Run("eg-music")
	require.NoError(t, err)
	assert.Zero(t, st2.NamesCreated)
	assert.Zero(t, st2.CreditsWritten)
	assert.Equal(t, 7, st2.Already, "all 7 credits already present")
}

// TestEGMusicSharesStaffNameSpace proves the music wave reuses a creater already
// anchored by the staff lane (rule:eg-creater-import) rather than recreating it.
func TestEGMusicSharesStaffNameSpace(t *testing.T) {
	clean(t)
	workID := seedEGRosettaWork(t, 7)

	// Staff lane imports creater 500 as an illustrator (shubetu 1) first.
	testDB.Exec(`INSERT INTO creaters (id, raw) VALUES (500,'{"name":"兼任"}')`)
	testDB.Exec(`INSERT INTO staff (game, creater_id, shubetu) VALUES (7,500,1)`)
	_, err := New(testDB, testDB, Options{Source: "eg"}).Run("eg")
	require.NoError(t, err)
	nameID := scalarInt(t, `SELECT entity_id FROM catalog_external_ref WHERE entity_type=1 AND source_id=5 AND external_id='500'`)

	// Music wave credits the same creater as a singer — no new credit name.
	testDB.Exec(`INSERT INTO game_music (music, game) VALUES (20,7)`)
	testDB.Exec(`INSERT INTO singers (raw, music, creater_id) VALUES ('{"featuring":false}',20,500)`)
	st, err := New(testDB, testDB, Options{Source: "eg-music"}).Run("eg-music")
	require.NoError(t, err)
	assert.Zero(t, st.NamesCreated, "creater 500 already anchored by the staff lane → no new name")
	assert.Equal(t, 1, st.CreditsWritten)
	assert.EqualValues(t, nameID, scalarIntA(t, `SELECT credit_name_id FROM catalog_credit WHERE source_id=5 AND role_id=286 AND work_id=?`, workID),
		"the vocal credit reuses the staff-lane credit name")
	assert.EqualValues(t, 1, scalarInt(t, `SELECT count(*) FROM catalog_external_ref WHERE entity_type=1 AND source_id=5 AND external_id='500'`),
		"still exactly one anchor for creater 500")
}
