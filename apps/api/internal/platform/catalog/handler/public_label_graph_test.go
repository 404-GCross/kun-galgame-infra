// public_label_graph_test.go — the WIRE of GET
// /v1/catalog/labels/{id}/relation-graph (wave 188).
//
// The service suite pins the traversal; what is pinned here is the shape a
// consumer parses and the miss posture: nodes/edges always present, logo_hash
// never omitted, the seed first, and the same 404 / 301 the labels/{id} lane
// already serves — a stale id must learn where the identity went on this route
// too, not only on the detail one.
package handler

import (
	"testing"

	"api/internal/platform/catalog/model"
	apierrors "api/pkg/errors"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublicLabelRelationGraphWire(t *testing.T) {
	db := openCatalogTestDB(t)
	live, mid, _ := seedMergedLabels(t, db)
	require.NoError(t, db.Exec("TRUNCATE catalog_label_relation RESTART IDENTITY CASCADE").Error)
	app := publicApp(db)

	parent := model.CatalogLabel{DisplayName: "親会社", Kind: model.LabelKindGameBrand, LogoHash: "logohash-oya"}
	require.NoError(t, db.Create(&parent).Error)
	// The fact "parent is the parent of live", stored mirrored the way the
	// importer stores it.
	require.NoError(t, db.Create(&model.CatalogLabelRelation{
		LabelID: live, OtherLabelID: parent.ID, Relation: model.LabelRelationParent,
		SourceID: 2, MatchedBy: "rule:test",
	}).Error)
	require.NoError(t, db.Create(&model.CatalogLabelRelation{
		LabelID: parent.ID, OtherLabelID: live, Relation: model.LabelRelationSubsidiary,
		SourceID: 2, MatchedBy: "rule:test",
	}).Error)

	t.Run("graph shape", func(t *testing.T) {
		resp, body := getRaw(t, app, "/v1/catalog/labels/"+itoa(live)+"/relation-graph")
		require.Equal(t, 200, resp.StatusCode)
		data := body["data"].(map[string]any)
		nodes := data["nodes"].([]any)
		require.Len(t, nodes, 2)

		seed := nodes[0].(map[string]any)
		assert.EqualValues(t, live, seed["id"], "nodes[0] must be the seed")
		assert.Equal(t, "生存ブランド", seed["name"])
		// logo_hash is ALWAYS on the wire — "" is the real answer "no logo".
		require.Contains(t, seed, "logo_hash")
		assert.Equal(t, "", seed["logo_hash"])
		assert.EqualValues(t, 0, seed["work_count"])
		assert.Equal(t, "logohash-oya", nodes[1].(map[string]any)["logo_hash"])

		// One fact, stored twice, on the wire once — in the canonical direction.
		edges := data["edges"].([]any)
		require.Len(t, edges, 1)
		e := edges[0].(map[string]any)
		assert.EqualValues(t, live, e["from"])
		assert.EqualValues(t, parent.ID, e["to"])
		assert.Equal(t, "parent", e["relation"])
	})

	t.Run("a label with no relations is a one-node graph, not a 404", func(t *testing.T) {
		lonely := model.CatalogLabel{DisplayName: "孤立ブランド", Kind: model.LabelKindGameBrand}
		require.NoError(t, db.Create(&lonely).Error)
		resp, body := getRaw(t, app, "/v1/catalog/labels/"+itoa(lonely.ID)+"/relation-graph")
		require.Equal(t, 200, resp.StatusCode)
		data := body["data"].(map[string]any)
		assert.Len(t, data["nodes"].([]any), 1)
		// [] rather than null: a consumer iterating edges must not have to
		// null-check the key.
		require.NotNil(t, data["edges"])
		assert.Len(t, data["edges"].([]any), 0)
	})

	t.Run("absent id is a 404", func(t *testing.T) {
		resp, body := getRaw(t, app, "/v1/catalog/labels/"+itoa(live+9999)+"/relation-graph")
		require.Equal(t, 404, resp.StatusCode)
		assert.EqualValues(t, apierrors.ErrNotFound, body["code"])
	})

	t.Run("merged id redirects like the detail lane", func(t *testing.T) {
		resp, body := getRaw(t, app, "/v1/catalog/labels/"+itoa(mid)+"/relation-graph")
		require.Equal(t, 301, resp.StatusCode)
		assert.Equal(t, "/v1/catalog/labels/"+itoa(live), resp.Header.Get("Location"))
		assert.EqualValues(t, live, body["data"].(map[string]any)["current_id"])
	})
}
