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

	// A repeat submission is a 409 that hands back the existing work.
	status, raw = editPost(t, app, "/api/v1/catalog/works/submit", body)
	require.Equal(t, fiber.StatusConflict, status, string(raw))
	var conflict struct {
		Data dto.WorkSubmitConflictInfo `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &conflict))
	assert.Equal(t, minted.Data.WorkID, conflict.Data.WorkID)
	assert.Equal(t, model.ClaimStateKeyPending, conflict.Data.CurrentState)

	// A payload key outside the submission subset is a 422, not a silent drop.
	status, raw = editPost(t, app, "/api/v1/catalog/works/submit",
		`{"site":"kungal","product_work_id":80002,"actor":{"user_id":5},
		  "fields":{"catalog.work.display_name":"x","catalog.work.covers":[]}}`)
	assert.Equal(t, fiber.StatusUnprocessableEntity, status, string(raw))

	// And the minted submission is immediately in the review queue.
	items, total, err := claims.PendingClaims(t.Context(), "kungal", 10)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, items, 1)
	assert.Equal(t, minted.Data.WorkID, items[0].WorkID)
}
