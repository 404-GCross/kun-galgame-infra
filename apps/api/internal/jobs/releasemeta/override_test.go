package releasemeta

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"api/internal/platform/catalog/editspec"
	"api/internal/platform/catalog/model"
	"api/internal/platform/editing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRatingRespectsCuratedOverride is the case the fill-empty guard cannot
// cover on its own: an editor who ruled a work ALL AGES leaves content_rating
// at 0, which is precisely the state this lane reads as "unset". Without the
// curated-override check, the one verdict a human stated explicitly would be
// the one an importer overwrites — with r18, on a work a human called all-ages.
func TestRatingRespectsCuratedOverride(t *testing.T) {
	clean(t)
	require.NoError(t, testDB.Exec("TRUNCATE edit_revision CASCADE").Error)
	ctx := context.Background()
	medium := galgameMedium(t)
	registry, err := resolveRegistry(ctx, testDB)
	require.NoError(t, err)
	wiki := "galgame_wiki"

	// Two works DLsite calls r18; a human ruled the first one all-ages through
	// the editing engine. (The verdict used to come from the wiki's age_limit;
	// wave 149 dropped that lane, so the r18 now comes from DLsite — the source
	// is incidental to what this test pins, which is that a human ruling wins.)
	pw1, pw2 := int64(5001), int64(5002)
	edited := mkWork(t, medium, "human-ruled", &wiki, &pw1, 0)
	untouched := mkWork(t, medium, "never-edited", &wiki, &pw2, 0)
	for i, w := range []int64{edited, untouched} {
		workno := fmt.Sprintf("RJ5000%02d", i)
		mkReleaseAnchor(t, mkRelease(t, w, 2000, 1, 1), workno, registry.dlsiteSource)
		mkDlWork(t, workno, "", "3") // '3' → r18
	}

	changed, err2 := json.Marshal([]string{editspec.FieldWorkContentRating})
	require.NoError(t, err2)
	require.NoError(t, testDB.Create(&editing.Revision{
		EntityFamily: "catalog", EntityType: editspec.TypeWork, EntityID: edited,
		Seq: 1, Action: editing.ActionDirect,
		ChangedFields: changed, Snapshot: []byte(`{}`),
		ActorUID: 42, Site: "nextmoe",
	}).Error)

	st, err := Run(ctx, runOpts(true))
	require.NoError(t, err)

	assert.Equal(t, 1, st.RatingCuratedOverride, "the edited work is skipped before any verdict")
	assert.Equal(t, model.ContentRatingAllAges, workRating(t, edited),
		"a human all-ages ruling must survive the importer")
	assert.Equal(t, model.ContentRatingR18, workRating(t, untouched),
		"a work nobody edited is still filled from the DLsite verdict")
}
