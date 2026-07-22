package importer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedVNRelation inserts one src row "vid is-a <rel> of id" (args ordered to
// read id-rel-vid; columns are (id, vid, relation)).
func seedVNRelation(t *testing.T, id, rel, vid string) {
	t.Helper()
	require.NoError(t, testDB.Exec(`INSERT INTO src_vndb.vn_relations (id, vid, relation, official) VALUES (?,?,?,true)`, id, vid, rel).Error)
}

// TestVNDBRelations exercises the VNDB vocabulary mapping, the both-ends
// anchor gate, the self-loop guard, direction folding of inverse pairs
// (seq/preq, fan/orig) into one directed edge, symmetric normalization (a<b),
// the unmapped-relation bucket, and idempotency.
func TestVNDBRelations(t *testing.T) {
	if testDB == nil {
		t.Skip("no test db")
	}
	clean(t)

	// Four vndb-anchored works (external_id == "v<id>"). VNDB's (id, vid) is a
	// natural key — a given ordered pair carries exactly one relation — so each
	// seeded row uses a distinct pair.
	w100 := seedVNDBWork(t, "v100")
	w200 := seedVNDBWork(t, "v200")
	w300 := seedVNDBWork(t, "v300")
	w400 := seedVNDBWork(t, "v400")

	seedVNRelation(t, "v100", "seq", "v200")  // v200 is sequel of v100 → w200 sequel_of w100
	seedVNRelation(t, "v200", "preq", "v100") // mirror: v100 is prequel of v200 → same edge (folded)
	seedVNRelation(t, "v100", "ser", "v300")  // same_series (symmetric)
	seedVNRelation(t, "v300", "ser", "v100")  // mirror (folded)
	seedVNRelation(t, "v100", "fan", "v400")  // v400 is fandisc of v100 → w400 fandisc_of w100
	seedVNRelation(t, "v400", "orig", "v100") // mirror: v100 is original of v400 → same edge (folded)
	seedVNRelation(t, "v100", "seq", "v100")  // self-loop (guarded)
	seedVNRelation(t, "v200", "xyz", "v300")  // unknown relation → skipped_unmapped
	seedVNRelation(t, "v100", "seq", "v999")  // v999 unanchored → skipped_unanchored

	// Dry: nothing written, counts closed.
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

	// Run.
	st, err := New(testDB, nil, Options{}).RunVNDBRelations()
	require.NoError(t, err)
	assert.Equal(t, 3, st.EdgesWritten)

	// Direction: the sequel edge is w200 --sequel_of--> w100 (v200 is the later work).
	assert.Equal(t, int64(1), scalarInt(t,
		`SELECT count(*) FROM catalog_work_relation WHERE a_work_id=`+itoa64(w200)+` AND b_work_id=`+itoa64(w100)+` AND relation_type_id=2 AND source_id=2`),
		"v200 is the sequel of v100")
	assert.Zero(t, scalarInt(t,
		`SELECT count(*) FROM catalog_work_relation WHERE a_work_id=`+itoa64(w100)+` AND b_work_id=`+itoa64(w200)+` AND relation_type_id=2`),
		"the prequel mirror is folded away, not written")
	// Direction: the fandisc edge is w400 --fandisc_of--> w100 (v400 is the fandisc).
	assert.Equal(t, int64(1), scalarInt(t,
		`SELECT count(*) FROM catalog_work_relation WHERE a_work_id=`+itoa64(w400)+` AND b_work_id=`+itoa64(w100)+` AND relation_type_id=4 AND source_id=2`),
		"v400 is the fandisc of v100")
	// Symmetric same_series normalizes a<b.
	lo, hi := w100, w300
	if lo > hi {
		lo, hi = hi, lo
	}
	assert.Equal(t, int64(1), scalarInt(t,
		`SELECT count(*) FROM catalog_work_relation WHERE a_work_id=`+itoa64(lo)+` AND b_work_id=`+itoa64(hi)+` AND relation_type_id=7`), "same_series a<b")

	// Idempotent: a second run writes nothing; every mapped-anchored row (primary
	// and mirror) now resolves to an existing edge.
	st2, err := New(testDB, nil, Options{}).RunVNDBRelations()
	require.NoError(t, err)
	assert.Zero(t, st2.Edges)
	assert.Zero(t, st2.EdgesWritten)
	assert.Equal(t, 6, st2.AlreadyInDB, "3 primary + 3 mirror rows all already in DB")
}

// TestVNDBRelationsLimit proves --limit caps the plan and a subsequent full run
// lands the remainder (incremental, idempotent).
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
	seedVNRelation(t, "v1", "ser", "v2") // edge A
	seedVNRelation(t, "v1", "ser", "v3") // edge B
	seedVNRelation(t, "v2", "ser", "v3") // edge C

	// Limit 1: exactly one edge written.
	st, err := New(testDB, nil, Options{Limit: 1}).RunVNDBRelations()
	require.NoError(t, err)
	assert.Equal(t, 1, st.Edges)
	assert.Equal(t, 1, st.EdgesWritten)
	assert.Equal(t, int64(1), scalarInt(t, `SELECT count(*) FROM catalog_work_relation`))

	// Full run: the other two land; the first is already-in-db.
	st2, err := New(testDB, nil, Options{}).RunVNDBRelations()
	require.NoError(t, err)
	assert.Equal(t, 2, st2.EdgesWritten)
	assert.Equal(t, int64(3), scalarInt(t, `SELECT count(*) FROM catalog_work_relation`))
}

// TestVNDBRelationsCrossSourceConverge proves a pair related in BOTH sources
// (Bangumi says A is the sequel of B; VNDB says B is the prequel of A) lands as
// ONE edge — the second lane sees the first lane's edge and folds it.
func TestVNDBRelationsCrossSourceConverge(t *testing.T) {
	if testDB == nil {
		t.Skip("no test db")
	}
	clean(t)

	// One work dual-anchored (bgm bid 500 AND vndb v500); the other likewise.
	wA := seedAnchoredWork(t, 500) // bgm anchor bid=500
	require.NoError(t, testDB.Exec(`INSERT INTO catalog_external_ref (entity_type, entity_id, source_id, external_id, link_kind, matched_by)
		VALUES (5, ?, 2, 'v500', 0, 'rule:test-vndb-work-anchor')`, wA).Error)
	wB := seedAnchoredWork(t, 600) // bgm anchor bid=600
	require.NoError(t, testDB.Exec(`INSERT INTO catalog_external_ref (entity_type, entity_id, source_id, external_id, link_kind, matched_by)
		VALUES (5, ?, 2, 'v600', 0, 'rule:test-vndb-work-anchor')`, wB).Error)

	// Bangumi: 500 --4003--> 600 means "600 is the sequel of 500" → wB sequel_of wA.
	require.NoError(t, testDB.Exec(`INSERT INTO src_bangumi.subject_relation (subject_id, relation_type, related_subject_id, item_order)
		VALUES (500, 4003, 600, 0)`).Error)
	// VNDB: v600 --preq--> v500 means "v500 is the prequel of v600" → wB sequel_of wA (same edge, inverse phrasing).
	seedVNRelation(t, "v600", "preq", "v500")

	bgm, err := New(testDB, nil, Options{}).RunBangumiRelations()
	require.NoError(t, err)
	assert.Equal(t, 1, bgm.EdgesWritten)

	vndb, err := New(testDB, nil, Options{}).RunVNDBRelations()
	require.NoError(t, err)
	assert.Zero(t, vndb.EdgesWritten, "the pair is already in DB from the bgm lane")
	assert.Equal(t, 1, vndb.AlreadyInDB)

	// Exactly one edge, directed wB --sequel_of--> wA (the bgm lane wrote it, source 3).
	assert.Equal(t, int64(1), scalarInt(t, `SELECT count(*) FROM catalog_work_relation`))
	assert.Equal(t, int64(1), scalarInt(t,
		`SELECT count(*) FROM catalog_work_relation WHERE a_work_id=`+itoa64(wB)+` AND b_work_id=`+itoa64(wA)+` AND relation_type_id=2`))
}
