package importer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedVNRelation(t *testing.T, id, rel, vid string) {
	t.Helper()
	require.NoError(t, testDB.Exec(`INSERT INTO src_vndb.vn_relations (id, vid, relation, official) VALUES (?,?,?,true)`, id, vid, rel).Error)
}

func TestVNDBRelations(t *testing.T) {
	if testDB == nil {
		t.Skip("no test db")
	}
	clean(t)

	w100 := seedVNDBWork(t, "v100")
	w200 := seedVNDBWork(t, "v200")
	w300 := seedVNDBWork(t, "v300")
	w400 := seedVNDBWork(t, "v400")

	seedVNRelation(t, "v100", "seq", "v200")
	seedVNRelation(t, "v200", "preq", "v100")
	seedVNRelation(t, "v100", "ser", "v300")
	seedVNRelation(t, "v300", "ser", "v100")
	seedVNRelation(t, "v100", "fan", "v400")
	seedVNRelation(t, "v400", "orig", "v100")
	seedVNRelation(t, "v100", "seq", "v100")
	seedVNRelation(t, "v200", "xyz", "v300")
	seedVNRelation(t, "v100", "seq", "v999")

	dry, err := New(testDB, nil, Options{DryRun: true}).RunVNDBRelations()
	require.NoError(t, err)
	assert.Equal(t, 9, dry.TotalRows)
	assert.Equal(t, 8, dry.Mapped, "all but the xyz row map")
	assert.Equal(t, 3, dry.Edges, "sequel + same_series + fandisc")
	assert.Equal(t, 3, dry.InverseFolded, "preq, ser-mirror, orig")
	assert.Equal(t, 1, dry.SkippedUnmapped)
	assert.Equal(t, 1, dry.SkippedUnanchored)
	assert.Equal(t, 1, dry.SkippedSelf)
	assert.Zero(t, dry.EdgesWritten)
	assert.Equal(t, dry.Mapped, dry.Edges+dry.InverseFolded+dry.AlreadyInDB+dry.SkippedUnanchored+dry.SkippedSelf)
	assert.Zero(t, scalarInt(t, `SELECT count(*) FROM catalog_work_relation`), "dry writes nothing")

	st, err := New(testDB, nil, Options{}).RunVNDBRelations()
	require.NoError(t, err)
	assert.Equal(t, 3, st.EdgesWritten)

	assert.Equal(t, int64(1), scalarInt(t,
		`SELECT count(*) FROM catalog_work_relation WHERE a_work_id=`+itoa64(w200)+` AND b_work_id=`+itoa64(w100)+` AND relation_type_id=2 AND source_id=2`),
		"v200 is the sequel of v100")
	assert.Zero(t, scalarInt(t,
		`SELECT count(*) FROM catalog_work_relation WHERE a_work_id=`+itoa64(w100)+` AND b_work_id=`+itoa64(w200)+` AND relation_type_id=2`),
		"the prequel mirror is folded away, not written")
	assert.Equal(t, int64(1), scalarInt(t,
		`SELECT count(*) FROM catalog_work_relation WHERE a_work_id=`+itoa64(w400)+` AND b_work_id=`+itoa64(w100)+` AND relation_type_id=4 AND source_id=2`),
		"v400 is the fandisc of v100")
	lo, hi := w100, w300
	if lo > hi {
		lo, hi = hi, lo
	}
	assert.Equal(t, int64(1), scalarInt(t,
		`SELECT count(*) FROM catalog_work_relation WHERE a_work_id=`+itoa64(lo)+` AND b_work_id=`+itoa64(hi)+` AND relation_type_id=7`), "same_series a<b")

	st2, err := New(testDB, nil, Options{}).RunVNDBRelations()
	require.NoError(t, err)
	assert.Zero(t, st2.Edges)
	assert.Zero(t, st2.EdgesWritten)
	assert.Equal(t, 6, st2.AlreadyInDB, "3 primary + 3 mirror rows all already in DB")
}

func TestVNDBRelationsLimit(t *testing.T) {
	if testDB == nil {
		t.Skip("no test db")
	}
	clean(t)

	w1 := seedVNDBWork(t, "v1")
	w2 := seedVNDBWork(t, "v2")
	w3 := seedVNDBWork(t, "v3")
	_ = w1
	_ = w2
	_ = w3
	seedVNRelation(t, "v1", "ser", "v2")
	seedVNRelation(t, "v1", "ser", "v3")
	seedVNRelation(t, "v2", "ser", "v3")

	st, err := New(testDB, nil, Options{Limit: 1}).RunVNDBRelations()
	require.NoError(t, err)
	assert.Equal(t, 1, st.Edges)
	assert.Equal(t, 1, st.EdgesWritten)
	assert.Equal(t, int64(1), scalarInt(t, `SELECT count(*) FROM catalog_work_relation`))

	st2, err := New(testDB, nil, Options{}).RunVNDBRelations()
	require.NoError(t, err)
	assert.Equal(t, 2, st2.EdgesWritten)
	assert.Equal(t, int64(3), scalarInt(t, `SELECT count(*) FROM catalog_work_relation`))
}

func TestVNDBRelationsCrossSourceConverge(t *testing.T) {
	if testDB == nil {
		t.Skip("no test db")
	}
	clean(t)

	wA := seedAnchoredWork(t, 500)
	require.NoError(t, testDB.Exec(`INSERT INTO catalog_external_ref (entity_type, entity_id, source_id, external_id, link_kind, matched_by)
		VALUES (5, ?, 2, 'v500', 0, 'rule:test-vndb-work-anchor')`, wA).Error)
	wB := seedAnchoredWork(t, 600)
	require.NoError(t, testDB.Exec(`INSERT INTO catalog_external_ref (entity_type, entity_id, source_id, external_id, link_kind, matched_by)
		VALUES (5, ?, 2, 'v600', 0, 'rule:test-vndb-work-anchor')`, wB).Error)

	require.NoError(t, testDB.Exec(`INSERT INTO src_bangumi.subject_relation (subject_id, relation_type, related_subject_id, item_order)
		VALUES (500, 4003, 600, 0)`).Error)
	seedVNRelation(t, "v600", "preq", "v500")

	bgm, err := New(testDB, nil, Options{}).RunBangumiRelations()
	require.NoError(t, err)
	assert.Equal(t, 1, bgm.EdgesWritten)

	vndb, err := New(testDB, nil, Options{}).RunVNDBRelations()
	require.NoError(t, err)
	assert.Zero(t, vndb.EdgesWritten, "the pair is already in DB from the bgm lane")
	assert.Equal(t, 1, vndb.AlreadyInDB)

	assert.Equal(t, int64(1), scalarInt(t, `SELECT count(*) FROM catalog_work_relation`))
	assert.Equal(t, int64(1), scalarInt(t,
		`SELECT count(*) FROM catalog_work_relation WHERE a_work_id=`+itoa64(wB)+` AND b_work_id=`+itoa64(wA)+` AND relation_type_id=2`))
}

func TestVNDBRelationsTouchesBothEnds(t *testing.T) {
	if testDB == nil {
		t.Skip("no test db")
	}
	clean(t)

	w100 := seedVNDBWork(t, "v100")
	w200 := seedVNDBWork(t, "v200")
	wLone := seedVNDBWork(t, "v900")
	seedVNRelation(t, "v100", "seq", "v200")

	stamp := "2020-01-01T00:00:00Z"
	require.NoError(t, testDB.Exec(`UPDATE catalog_work SET updated_at = ?`, stamp).Error)
	updatedAt := func(id int64) string {
		var ts string
		require.NoError(t, testDB.Raw(
			`SELECT to_char(updated_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SSZ') FROM catalog_work WHERE id = ?`,
			id).Scan(&ts).Error)
		return ts
	}

	_, err := New(testDB, nil, Options{DryRun: true}).RunVNDBRelations()
	require.NoError(t, err)
	assert.Equal(t, stamp, updatedAt(w100), "dry run must not touch")

	st, err := New(testDB, nil, Options{}).RunVNDBRelations()
	require.NoError(t, err)
	require.Equal(t, 1, st.EdgesWritten)
	assert.NotEqual(t, stamp, updatedAt(w100), "the a end is republished")
	assert.NotEqual(t, stamp, updatedAt(w200), "the b end is republished too")
	assert.Equal(t, stamp, updatedAt(wLone), "an unrelated work stays put")

	touchedA, touchedB := updatedAt(w100), updatedAt(w200)
	st2, err := New(testDB, nil, Options{}).RunVNDBRelations()
	require.NoError(t, err)
	require.Zero(t, st2.EdgesWritten)
	assert.Equal(t, touchedA, updatedAt(w100), "a no-op re-run must not drift the watermark")
	assert.Equal(t, touchedB, updatedAt(w200))
}
