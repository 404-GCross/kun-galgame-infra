package importer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBangumiRelations exercises the mapping, direction folding (inverse pair →
// one directed edge), the both-ends anchor gate, the self-loop guard, the
// skip-other bucket, and idempotency.
func TestBangumiRelations(t *testing.T) {
	if testDB == nil {
		t.Skip("no test db")
	}
	clean(t)

	// Three anchored works (bid == bangumi subject id via the exact ref).
	w100 := seedAnchoredWork(t, 100)
	w200 := seedAnchoredWork(t, 200)
	w300 := seedAnchoredWork(t, 300)

	require.NoError(t, testDB.Exec(`INSERT INTO src_bangumi.subject_relation (subject_id, relation_type, related_subject_id, item_order) VALUES
		(100, 4003, 200, 0),   -- 200 is the sequel of 100 → w200 sequel_of w100
		(200, 4002, 100, 0),   -- mirror: 100 is the prequel of 200 → same edge (folded)
		(100, 4008, 300, 0),   -- same_setting (symmetric)
		(300, 4008, 100, 0),   -- mirror (folded)
		(100, 4010, 300, 0),   -- alternative_version (new key, symmetric)
		(100, 4008, 100, 0),   -- self-loop (guarded)
		(100, 4099, 200, 0),   -- 其他 → skipped_other
		(100, 4003, 999, 0)`).Error) // 999 unanchored → skipped_unanchored

	// Dry: nothing written, counts closed.
	dry, err := New(testDB, nil, Options{DryRun: true}).RunBangumiRelations()
	require.NoError(t, err)
	assert.Equal(t, 8, dry.TotalRows)
	assert.Equal(t, 7, dry.Mapped)
	assert.Equal(t, 3, dry.Edges, "sequel + same_setting + alternative_version")
	assert.Equal(t, 2, dry.InverseFolded, "the two mirror rows")
	assert.Equal(t, 1, dry.SkippedOther)
	assert.Equal(t, 1, dry.SkippedUnanchored)
	assert.Equal(t, 1, dry.SkippedSelf)
	assert.Zero(t, dry.EdgesWritten)
	assert.Equal(t, dry.Mapped, dry.Edges+dry.InverseFolded+dry.AlreadyInDB+dry.SkippedUnanchored+dry.SkippedSelf)
	assert.Zero(t, scalarInt(t, `SELECT count(*) FROM catalog_work_relation`), "dry writes nothing")

	// Run.
	st, err := New(testDB, nil, Options{}).RunBangumiRelations()
	require.NoError(t, err)
	assert.Equal(t, 3, st.EdgesWritten)

	// Direction: the sequel edge is w200 --sequel_of--> w100 (200 is later).
	assert.Equal(t, int64(1), scalarInt(t,
		`SELECT count(*) FROM catalog_work_relation WHERE a_work_id=`+itoa64(w200)+` AND b_work_id=`+itoa64(w100)+` AND relation_type_id=2 AND source_id=3`),
		"200 is the sequel of 100")
	assert.Zero(t, scalarInt(t,
		`SELECT count(*) FROM catalog_work_relation WHERE a_work_id=`+itoa64(w100)+` AND b_work_id=`+itoa64(w200)+` AND relation_type_id=2`),
		"the reverse row is folded away, not written")
	// Symmetric edges normalize a<b.
	lo, hi := w100, w300
	if lo > hi {
		lo, hi = hi, lo
	}
	assert.Equal(t, int64(1), scalarInt(t,
		`SELECT count(*) FROM catalog_work_relation WHERE a_work_id=`+itoa64(lo)+` AND b_work_id=`+itoa64(hi)+` AND relation_type_id=8`), "same_setting a<b")
	assert.Equal(t, int64(1), scalarInt(t,
		`SELECT count(*) FROM catalog_work_relation WHERE a_work_id=`+itoa64(lo)+` AND b_work_id=`+itoa64(hi)+` AND relation_type_id=12`), "alternative_version a<b")

	// Idempotent: a second run writes nothing; every mapped-anchored row (primary
	// and mirror) now resolves to an existing edge.
	st2, err := New(testDB, nil, Options{}).RunBangumiRelations()
	require.NoError(t, err)
	assert.Zero(t, st2.Edges)
	assert.Zero(t, st2.EdgesWritten)
	assert.Equal(t, 5, st2.AlreadyInDB, "3 primary + 2 mirror rows all already in DB")
}
