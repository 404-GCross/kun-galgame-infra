package releasemeta

import (
	"context"
	"encoding/json"
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
	wiki := "galgame_wiki"

	// Two claimed works the wiki calls r18; a human ruled the first one
	// all-ages through the editing engine.
	pw1, pw2 := int64(5001), int64(5002)
	edited := mkWork(t, medium, "human-ruled", &wiki, &pw1, 0)
	untouched := mkWork(t, medium, "never-edited", &wiki, &pw2, 0)
	mkWikiGalgame(t, 5001, "r18")
	mkWikiGalgame(t, 5002, "r18")

	changed, err := json.Marshal([]string{editspec.FieldWorkContentRating})
	require.NoError(t, err)
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
		"a work nobody edited is still filled from the wiki verdict")
}
