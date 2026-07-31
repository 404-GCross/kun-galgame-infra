package olangfix

import (
	"context"
	"encoding/json"
	"testing"

	"api/internal/platform/catalog/editspec"
	"api/internal/platform/editing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordOLangEdit appends the edit_revision row a human olang edit would leave.
// It is written verbatim rather than through the engine because the point of
// the check is that the LEDGER is what an importer consults — any writer that
// produced this row must stop the job, whichever face it came from.
func recordOLangEdit(t *testing.T, workID int64, seq int) {
	t.Helper()
	changed, err := json.Marshal([]string{editspec.FieldWorkOLang})
	require.NoError(t, err)
	require.NoError(t, testDB.Create(&editing.Revision{
		EntityFamily: "catalog", EntityType: editspec.TypeWork, EntityID: workID,
		Seq: seq, Action: editing.ActionDirect,
		ChangedFields: changed, Snapshot: []byte(`{}`),
		ActorUID: 42, Site: "nextmoe",
	}).Error)
}

// TestBackfillOLangRespectsCuratedOverride is the curated-override rule's real
// case (03 定案 §0 line 2): a scalar a human edited is that human's decision,
// and the sync lane that used to own the column must leave it alone — while
// every work nobody edited is still corrected in the same pass.
func TestBackfillOLangRespectsCuratedOverride(t *testing.T) {
	f := seedFixture(t)
	require.NoError(t, testDB.Exec("TRUNCATE edit_revision CASCADE").Error)
	ctx := context.Background()

	// A human set vn-en's olang. Its VNDB anchor still says `en`, so without
	// the override this run would overwrite the human's value with the mirror's.
	recordOLangEdit(t, f.vnEN, 1)
	// And one on the wiki lane, to prove the rule is not lane-specific.
	recordOLangEdit(t, f.wikiPT, 1)

	st, err := Run(ctx, Opts{DSN: testDSN, Apply: true})
	require.NoError(t, err)

	assert.Equal(t, 2, st.CuratedOverride, "both edited works are skipped before any decision")
	assert.Equal(t, 3, st.Planned, "the other three candidates are still corrected")
	assert.Equal(t, 3, st.Written)

	// The edited works keep the value they had; the unedited ones move.
	assert.Equal(t, "ja", olangOf(t, f.vnEN), "a human-edited olang must survive the sync lane")
	assert.Equal(t, "ja", olangOf(t, f.wikiPT))
	assert.Equal(t, "ru", olangOf(t, f.vnMulti), "an untouched work is still corrected")
	assert.Equal(t, "en", olangOf(t, f.wikiEN))

	// A revision that changed a DIFFERENT field does not protect olang: the
	// ledger is consulted per field, not per entity.
	require.NoError(t, testDB.Exec("TRUNCATE edit_revision CASCADE").Error)
	changed, err := json.Marshal([]string{editspec.FieldWorkDisplayName})
	require.NoError(t, err)
	require.NoError(t, testDB.Create(&editing.Revision{
		EntityFamily: "catalog", EntityType: editspec.TypeWork, EntityID: f.vnEN,
		Seq: 1, Action: editing.ActionDirect,
		ChangedFields: changed, Snapshot: []byte(`{}`),
		ActorUID: 42, Site: "nextmoe",
	}).Error)

	st, err = Run(ctx, Opts{DSN: testDSN, Apply: true})
	require.NoError(t, err)
	assert.Zero(t, st.CuratedOverride, "a display_name edit says nothing about olang")
	assert.Equal(t, "en", olangOf(t, f.vnEN))
}
