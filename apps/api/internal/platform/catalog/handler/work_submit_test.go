package handler

import (
	"encoding/json"
	"testing"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/service"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The submission mint over HTTP (wave 162). Status codes are the contract a
// wizard branches on: 422 malformed payload, 409 already submitted (with the
// existing work attached so the wizard resumes instead of retrying), 200 with
// the minted identity.
//
// The mint used to answer on the S2S face too, with the tenant and the
// submitter asserted in the body; wave 185 retired that door and left the
// user-token twin, which derives both from the token. So this drives the twin —
// the wire contract above is the same one, and the tenant refusals the S2S
// cases pinned are now the token gate's own subject
// (user_claims_face_test.go's gate matrix).

func TestSubmitFaceEndToEnd(t *testing.T) {
	db := openCatalogTestDB(t)
	for _, tbl := range []string{
		"catalog_claim_event", "catalog_work_title", "catalog_work_intro",
		"catalog_external_ref", "catalog_release", "catalog_revision", "catalog_work",
	} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	claims := service.NewClaimLifecycleService(db)
	app := userClaimApp(db)
	token := userToken(t, 5, ScopeCatalogEdit, "kungal-client")

	body := `{"product_work_id":80001,
	          "fields":{"catalog.work.display_name":"投稿ゲーム","catalog.work.olang":"ja",
	                    "catalog.work.content_rating":2,
	                    "catalog.work.titles":[{"lang":"ja","title":"投稿ゲーム","kind":0}],
	                    "catalog.work.links":["https://vndb.org/v19658"]},
	          "released":{"y":2020,"m":3,"d":14}}`
	status, raw := userEditReq(t, app, "POST", UserPrefix+"/works/submit", token, body)
	require.Equal(t, fiber.StatusOK, status, string(raw))
	var minted struct {
		Data dto.WorkSubmitResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &minted))
	assert.Equal(t, model.ClaimStateKeyPending, minted.Data.ClaimState)
	assert.NotZero(t, minted.Data.WorkID)
	assert.NotZero(t, minted.Data.EventID)
	assert.NotZero(t, minted.Data.ReleaseID, "a submitted date becomes a curated release row")
	assert.EqualValues(t, 80001, minted.Data.ProductWorkID, "a supplied id is echoed verbatim")

	// A repeat submission is a 409 that hands back the existing work.
	status, raw = userEditReq(t, app, "POST", UserPrefix+"/works/submit", token, body)
	require.Equal(t, fiber.StatusConflict, status, string(raw))
	var conflict struct {
		Data dto.WorkSubmitConflictInfo `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &conflict))
	assert.Equal(t, minted.Data.WorkID, conflict.Data.WorkID)
	assert.Equal(t, model.ClaimStateKeyPending, conflict.Data.CurrentState)
	assert.Equal(t, service.ClaimMatchClaim, conflict.Data.MatchedBy)

	// product_work_id OMITTED: the registry issues the identity and hands it
	// back, and the wire must not require the field at all (charter
	// §6.P4-verdict 1). A retry is then recognized by the VNDB link.
	issuedBody := `{"fields":{"catalog.work.display_name":"番号なし",
	                          "catalog.work.links":["https://vndb.org/v11111"]}}`
	status, raw = userEditReq(t, app, "POST", UserPrefix+"/works/submit", token, issuedBody)
	require.Equal(t, fiber.StatusOK, status, string(raw))
	var issued struct {
		Data dto.WorkSubmitResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &issued))
	assert.NotZero(t, issued.Data.WorkID)
	assert.Equal(t, issued.Data.WorkID, issued.Data.ProductWorkID,
		"an omitted product id is issued as the minted work id")

	status, raw = userEditReq(t, app, "POST", UserPrefix+"/works/submit", token, issuedBody)
	require.Equal(t, fiber.StatusConflict, status, string(raw))
	require.NoError(t, json.Unmarshal(raw, &conflict))
	assert.Equal(t, issued.Data.WorkID, conflict.Data.WorkID)
	assert.Equal(t, service.ClaimMatchAnchor, conflict.Data.MatchedBy)
	assert.Equal(t, "vndb:v11111", conflict.Data.Anchor)

	// A payload key outside the submission subset is a 422, not a silent drop.
	status, raw = userEditReq(t, app, "POST", UserPrefix+"/works/submit", token,
		`{"product_work_id":80002,
		  "fields":{"catalog.work.display_name":"x","catalog.work.covers":[]}}`)
	assert.Equal(t, fiber.StatusUnprocessableEntity, status, string(raw))

	// And both minted submissions — the supplied-id one and the issued-id one —
	// are immediately in the review queue, oldest first.
	items, total, err := claims.PendingClaims(t.Context(), "kungal", 10)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	require.Len(t, items, 2)
	assert.Equal(t, minted.Data.WorkID, items[0].WorkID)
	assert.Equal(t, issued.Data.WorkID, items[1].WorkID)
}
