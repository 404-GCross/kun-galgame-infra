package importer

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedEGWork creates a work carrying an exact EG work anchor (source 5).
func seedEGWork(t *testing.T, gid string) int64 {
	t.Helper()
	var workID int64
	require.NoError(t, testDB.Raw(`INSERT INTO catalog_work (medium_id, olang, display_name, content_rating, status, extra, display_nsfw)
		VALUES (1, 'ja', 'eg-'||?::text, 0, 0, '{}', false) RETURNING id`, gid).Scan(&workID).Error)
	require.NoError(t, testDB.Exec(`INSERT INTO catalog_external_ref (entity_type, entity_id, source_id, external_id, link_kind, matched_by)
		VALUES (5, ?, 5, ?, 0, 'rule:test')`, workID, gid).Error)
	return workID
}

// seedEGRel inserts one game_relations row "subject is-a <kind> of object".
func seedEGRel(t *testing.T, pk int, subject, object int64, kind string) {
	t.Helper()
	require.NoError(t, testDB.Exec(`INSERT INTO game_relations (raw, game_subject, game_object, synced_at, pk)
		VALUES (?, ?, ?, now(), ?)`, fmt.Sprintf(`{"kind": %q}`, kind), subject, object, pk).Error)
}

// TestEGRelations exercises the EG vocabulary mapping (subject = derived work,
// no flip), the by-design skip bucket (bundling/apend/free), the both-ends
// anchor gate, the self-loop guard, symmetric normalization for transplant,
// duplicate-row folding, cross-source convergence (AlreadyInDB) and
// idempotency — and pins the bookkeeping identity.
func TestEGRelations(t *testing.T) {
	if testDB == nil {
		t.Skip("no test db")
	}
	clean(t)
	require.NoError(t, testDB.Exec(`CREATE TABLE IF NOT EXISTS game_relations
		(raw text, game_subject bigint, game_object bigint, synced_at timestamptz, pk bigint)`).Error)
	require.NoError(t, testDB.Exec(`TRUNCATE game_relations`).Error)

	w1 := seedEGWork(t, "100") // base work (created first → lowest id)
	w2 := seedEGWork(t, "200")
	w3 := seedEGWork(t, "300")
	w4 := seedEGWork(t, "400")
	seedEGWork(t, "500")

	seedEGRel(t, 1, 200, 100, "sequel")     // w2 sequel_of w1
	seedEGRel(t, 2, 200, 100, "sequel")     // duplicate src row → Folded
	seedEGRel(t, 3, 300, 100, "fandisk")    // w3 fandisc_of w1 — pre-asserted by vndb below → AlreadyInDB
	seedEGRel(t, 4, 400, 100, "transplant") // symmetric → (min,max) = (w1,w4)
	seedEGRel(t, 5, 500, 100, "bundling")   // by design
	seedEGRel(t, 6, 500, 100, "banana")     // unknown kind → unmapped
	seedEGRel(t, 7, 999, 100, "sequel")     // 999 unanchored
	seedEGRel(t, 8, 100, 100, "remake")     // self-loop
	// Cross-source convergence: vndb already asserted the fandisc edge.
	require.NoError(t, testDB.Exec(`INSERT INTO catalog_work_relation (a_work_id, b_work_id, relation_type_id, source_id)
		VALUES (?, ?, 4, 2)`, w3, w1).Error)

	// Dry: nothing written, counts closed.
	dry, err := New(testDB, testDB, Options{DryRun: true}).RunEGRelations()
	require.NoError(t, err)
	assert.Equal(t, 8, dry.TotalRows)
	assert.Equal(t, 1, dry.SkippedByDesign, "bundling")
	assert.Equal(t, 1, dry.SkippedUnmapped, "banana")
	assert.Equal(t, 6, dry.Mapped)
	assert.Equal(t, 2, dry.Edges, "sequel + transplant")
	assert.Equal(t, 1, dry.Folded, "duplicate sequel row")
	assert.Equal(t, 1, dry.AlreadyInDB, "vndb asserted the fandisc pair first")
	assert.Equal(t, 1, dry.SkippedUnanchored)
	assert.Equal(t, 1, dry.SkippedSelf)
	assert.Zero(t, dry.EdgesWritten)
	assert.Equal(t, dry.Mapped, dry.Edges+dry.Folded+dry.AlreadyInDB+dry.SkippedUnanchored+dry.SkippedSelf)
	assert.Equal(t, int64(1), scalarInt(t, `SELECT count(*) FROM catalog_work_relation`), "dry writes nothing (only the vndb fixture row)")

	// Run.
	st, err := New(testDB, testDB, Options{}).RunEGRelations()
	require.NoError(t, err)
	assert.Equal(t, 2, st.EdgesWritten)

	// Direction: subject (the later work) lands in a.
	assert.Equal(t, int64(1), scalarInt(t,
		`SELECT count(*) FROM catalog_work_relation WHERE a_work_id=`+itoa64(w2)+` AND b_work_id=`+itoa64(w1)+` AND relation_type_id=2 AND source_id=5`),
		"w2 is the sequel of w1")
	assert.Zero(t, scalarInt(t,
		`SELECT count(*) FROM catalog_work_relation WHERE a_work_id=`+itoa64(w1)+` AND b_work_id=`+itoa64(w2)+` AND relation_type_id=2`),
		"no inverted sequel edge")
	// Symmetric transplant normalizes a<b.
	assert.Equal(t, int64(1), scalarInt(t,
		`SELECT count(*) FROM catalog_work_relation WHERE a_work_id=`+itoa64(w1)+` AND b_work_id=`+itoa64(w4)+` AND relation_type_id=12 AND source_id=5`),
		"transplant normalized to (min,max)")

	// Second run: zero writes, everything converges.
	st2, err := New(testDB, testDB, Options{}).RunEGRelations()
	require.NoError(t, err)
	assert.Zero(t, st2.Edges)
	assert.Zero(t, st2.EdgesWritten)
	assert.Equal(t, 4, st2.AlreadyInDB, "sequel + dup + fandisc + transplant all in db now")
	assert.Equal(t, st2.Mapped, st2.Edges+st2.Folded+st2.AlreadyInDB+st2.SkippedUnanchored+st2.SkippedSelf)
}
