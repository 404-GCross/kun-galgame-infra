package handler

import (
	"encoding/json"
	"testing"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/service"
	siteModel "api/internal/platform/site/model"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The submission mint over HTTP (wave 162). Status codes are the contract a
// wizard branches on: 403 wrong tenant, 422 malformed payload, 409 already
// submitted (with the existing work attached so the wizard resumes instead of
// retrying), 200 with the minted identity.

func TestSubmitFaceRegistersAndGuardsTheTenant(t *testing.T) {
	app := lifecycleApp(&siteModel.OAuthClient{ID: "c1", CatalogSite: "kungal"}, nil)
	// Decided before any transaction opens — the nil service proves it.
	status, raw := editPost(t, app, "/api/v1/catalog/works/submit",
		`{"site":"moyu","product_work_id":1,"actor":{"user_id":1},"fields":{"catalog.work.display_name":"x"}}`)
	assert.Equal(t, fiber.StatusForbidden, status, string(raw))

	unbound := lifecycleApp(&siteModel.OAuthClient{ID: "c2"}, nil)
	status, raw = editPost(t, unbound, "/api/v1/catalog/works/submit",
		`{"site":"kungal","product_work_id":1,"actor":{"user_id":1},"fields":{"catalog.work.display_name":"x"}}`)
	assert.Equal(t, fiber.StatusForbidden, status, string(raw))
}

func TestSubmitFaceEndToEnd(t *testing.T) {
	db := openCatalogTestDB(t)
	for _, tbl := range []string{
		"catalog_claim_event", "catalog_work_title", "catalog_work_intro",
		"catalog_external_ref", "catalog_release", "catalog_revision", "catalog_work",
	} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	claims := service.NewClaimLifecycleService(db)
	app := lifecycleApp(&siteModel.OAuthClient{ID: "kungal-client", CatalogSite: "kungal"}, claims)

	body := `{"site":"kungal","product_work_id":80001,"actor":{"user_id":5},
	          "fields":{"catalog.work.display_name":"投稿ゲーム","catalog.work.olang":"ja",
	                    "catalog.work.content_rating":2,
	                    "catalog.work.titles":[{"lang":"ja","title":"投稿ゲーム","kind":0}],
	                    "catalog.work.links":["https://vndb.org/v19658"]},
	          "released":{"y":2020,"m":3,"d":14}}`
	status, raw := editPost(t, app, "/api/v1/catalog/works/submit", body)
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
	status, raw = editPost(t, app, "/api/v1/catalog/works/submit", body)
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
	issuedBody := `{"site":"kungal","actor":{"user_id":5},
	                "fields":{"catalog.work.display_name":"番号なし",
	                          "catalog.work.links":["https://vndb.org/v11111"]}}`
	status, raw = editPost(t, app, "/api/v1/catalog/works/submit", issuedBody)
	require.Equal(t, fiber.StatusOK, status, string(raw))
	var issued struct {
		Data dto.WorkSubmitResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &issued))
	assert.NotZero(t, issued.Data.WorkID)
	assert.Equal(t, issued.Data.WorkID, issued.Data.ProductWorkID,
		"an omitted product id is issued as the minted work id")

	status, raw = editPost(t, app, "/api/v1/catalog/works/submit", issuedBody)
	require.Equal(t, fiber.StatusConflict, status, string(raw))
	require.NoError(t, json.Unmarshal(raw, &conflict))
	assert.Equal(t, issued.Data.WorkID, conflict.Data.WorkID)
	assert.Equal(t, service.ClaimMatchAnchor, conflict.Data.MatchedBy)
	assert.Equal(t, "vndb:v11111", conflict.Data.Anchor)

	// A payload key outside the submission subset is a 422, not a silent drop.
	status, raw = editPost(t, app, "/api/v1/catalog/works/submit",
		`{"site":"kungal","product_work_id":80002,"actor":{"user_id":5},
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
