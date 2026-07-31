package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"testing"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/service"
	siteModel "api/internal/platform/site/model"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The lifecycle face over HTTP (wave 155 W2/W3): the status codes are the
// contract downstream products branch on, so they are what these cases pin.

func TestSetupLifecycle_RegistersOperations(t *testing.T) {
	app := fiber.New()
	api := Setup(app, nil, nil, nil, nil, nil)
	SetupLifecycle(api, nil, nil, nil)
	paths := api.OpenAPI().Paths
	for _, p := range []string{
		"/api/v1/catalog/works/{id}/claim-actions/{action}",
		"/api/v1/catalog/claim-events/feed",
		"/api/v1/catalog/edit-revisions/feed",
	} {
		assert.NotNilf(t, paths[p], "operation %s must be registered", p)
	}
}

func TestSetupAdmin_RegistersClaimQueue(t *testing.T) {
	api := SetupAdmin(fiber.New(), nil, nil, nil)
	paths := api.OpenAPI().Paths
	for _, p := range []string{
		"/api/v1/admin/catalog/claims/pending",
		"/api/v1/admin/catalog/claims/{id}/{action}",
	} {
		assert.NotNilf(t, paths[p], "operation %s must be registered", p)
	}
}

// lifecycleApp wires the face the way cmd/catalog does, with the client the
// path-scoped S2SAuth would have injected.
func lifecycleApp(client *siteModel.OAuthClient, claims *service.ClaimLifecycleService) *fiber.App {
	app := fiber.New()
	app.Use("/api/v1/catalog", func(c fiber.Ctx) error {
		if client != nil {
			c.Locals(localClient, client)
		}
		return c.Next()
	})
	api := Setup(app, nil, nil, nil, nil, nil)
	SetupLifecycle(api, claims, nil, nil)
	return app
}

// editGet issues a GET on the S2S face and returns the raw body — the feeds
// need the bytes, not a decoded map.
func editGet(t *testing.T, app *fiber.App, url string) (int, []byte) {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest("GET", url, nil))
	require.NoError(t, err)
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, raw
}

// TestLifecycleFaceAuthority: an unknown action never reaches the service, a
// site may not move another site's claim, and reviewing needs the review
// permission — all decided before any transaction opens (nil service proves it).
func TestLifecycleFaceAuthority(t *testing.T) {
	app := lifecycleApp(&siteModel.OAuthClient{ID: "c1", CatalogSite: "kungal"}, nil)

	status, _ := editPost(t, app, "/api/v1/catalog/works/1/claim-actions/frobnicate",
		`{"site":"kungal","actor":{"user_id":1}}`)
	assert.Equal(t, fiber.StatusBadRequest, status, "an unknown action is a 400")

	status, _ = editPost(t, app, "/api/v1/catalog/works/1/claim-actions/publish",
		`{"site":"moyu","actor":{"user_id":1}}`)
	assert.Equal(t, fiber.StatusForbidden, status, "a client may only act for its own site")

	status, _ = editPost(t, app, "/api/v1/catalog/works/1/claim-actions/approve",
		`{"actor":{"user_id":1,"roles":["user"]}}`)
	assert.Equal(t, fiber.StatusForbidden, status, "reviewing requires catalog.claim.review")
}

// TestLifecycleFaceEndToEnd drives a submission over HTTP and pins the 409 the
// whole action vocabulary hangs on.
func TestLifecycleFaceEndToEnd(t *testing.T) {
	db := openCatalogTestDB(t)
	for _, tbl := range []string{"catalog_claim_event", "catalog_work_title", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	work := &model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "投稿", Status: model.WorkStatusLive}
	require.NoError(t, db.Create(work).Error)

	claims := service.NewClaimLifecycleService(db)
	app := lifecycleApp(&siteModel.OAuthClient{ID: "kungal-client", CatalogSite: "kungal"}, claims)
	path := func(action string) string {
		return fmt.Sprintf("/api/v1/catalog/works/%d/claim-actions/%s", work.ID, action)
	}

	status, raw := editPost(t, app, path("claim"),
		`{"site":"kungal","product_work_id":777,"actor":{"user_id":5}}`)
	require.Equal(t, fiber.StatusOK, status, string(raw))
	var claimed struct {
		Data service.ClaimActionResult `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &claimed))
	assert.Nil(t, claimed.Data.From, "the birth event carries no prior state")
	assert.Equal(t, model.ClaimStateKeyDraft, claimed.Data.To)

	// publish is legal from draft; submit afterwards is NOT (live is not a
	// submit source) — the 409 must name the state that blocked it.
	status, raw = editPost(t, app, path("publish"), `{"site":"kungal","actor":{"user_id":5}}`)
	require.Equal(t, fiber.StatusOK, status, string(raw))

	status, raw = editPost(t, app, path("submit"), `{"site":"kungal","actor":{"user_id":5}}`)
	require.Equal(t, fiber.StatusConflict, status, string(raw))
	var conflict struct {
		Data struct {
			CurrentState string   `json:"current_state"`
			AllowedFrom  []string `json:"allowed_from"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &conflict))
	assert.Equal(t, model.ClaimStateKeyLive, conflict.Data.CurrentState)
	assert.NotEmpty(t, conflict.Data.AllowedFrom)

	// A reviewer (asserted moderator — wave 157 re-keyed these four actions to
	// catalog.claim.review, which moderators hold) may act across tenants. A
	// decline with no reason is refused as malformed BEFORE the transaction
	// opens — the input check runs ahead of the state machine, so the caller is
	// told the actual problem rather than "wrong state".
	status, raw = editPost(t, app, path("decline"), `{"actor":{"user_id":9,"roles":["moderator"]}}`)
	assert.Equal(t, fiber.StatusUnprocessableEntity, status, string(raw))
	status, raw = editPost(t, app, path("ban"), `{"actor":{"user_id":9,"roles":["moderator"]},"reason":"policy"}`)
	require.Equal(t, fiber.StatusOK, status, string(raw))

	// The feed serves what just happened, oldest first.
	status, raw = editGet(t, app, "/api/v1/catalog/claim-events/feed?since=0&limit=10")
	require.Equal(t, fiber.StatusOK, status, string(raw))
	var feed struct {
		Data struct {
			Items []struct {
				ID      int64  `json:"id"`
				ToState string `json:"to_state"`
			} `json:"items"`
			NextSince int64 `json:"next_since"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &feed))
	require.Len(t, feed.Data.Items, 3)
	assert.Equal(t, model.ClaimStateKeyDraft, feed.Data.Items[0].ToState)
	assert.Equal(t, model.ClaimStateKeyHidden, feed.Data.Items[2].ToState)
	assert.Equal(t, feed.Data.Items[2].ID, feed.Data.NextSince)

	// An exhausted cursor echoes itself rather than rewinding.
	status, raw = editGet(t, app, fmt.Sprintf("/api/v1/catalog/claim-events/feed?since=%d", feed.Data.NextSince))
	require.Equal(t, fiber.StatusOK, status, string(raw))
	var tail struct {
		Data struct {
			Items     []json.RawMessage `json:"items"`
			NextSince int64             `json:"next_since"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &tail))
	assert.Empty(t, tail.Data.Items)
	assert.Equal(t, feed.Data.NextSince, tail.Data.NextSince)
}
