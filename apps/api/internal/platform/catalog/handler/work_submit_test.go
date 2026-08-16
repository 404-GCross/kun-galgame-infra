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

func TestSubmitFaceEndToEnd(t *testing.T) {
	db := openCatalogTestDB(t)
	for _, tbl := range []string{
		"catalog_claim_event", "edit_suppressed_row", "catalog_work_title", "catalog_work_intro",
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

	status, raw = userEditReq(t, app, "POST", UserPrefix+"/works/submit", token, body)
	require.Equal(t, fiber.StatusConflict, status, string(raw))
	var conflict struct {
		Data dto.WorkSubmitConflictInfo `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &conflict))
	assert.Equal(t, minted.Data.WorkID, conflict.Data.WorkID)
	assert.Equal(t, model.ClaimStateKeyPending, conflict.Data.CurrentState)
	assert.Equal(t, service.ClaimMatchClaim, conflict.Data.MatchedBy)

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

	status, raw = userEditReq(t, app, "POST", UserPrefix+"/works/submit", token,
		`{"product_work_id":80002,
		  "fields":{"catalog.work.display_name":"x","catalog.work.covers":[]}}`)
	assert.Equal(t, fiber.StatusUnprocessableEntity, status, string(raw))

	items, total, err := claims.PendingClaims(t.Context(), "kungal", 10)
	require.NoError(t, err)
	assert.EqualValues(t, 2, total)
	require.Len(t, items, 2)
	assert.Equal(t, minted.Data.WorkID, items[0].WorkID)
	assert.Equal(t, issued.Data.WorkID, items[1].WorkID)
}
