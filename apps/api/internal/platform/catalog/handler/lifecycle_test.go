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

func TestSetupLifecycle_RegistersOperations(t *testing.T) {
	app := fiber.New()
	api := Setup(app, nil, nil, nil, nil, nil)
	SetupLifecycle(api, nil, nil, nil)
	paths := api.OpenAPI().Paths
	for _, p := range []string{
		"/api/v1/catalog/claim-events/feed",
		"/api/v1/catalog/edit-revisions/feed",
		"/api/v1/catalog/users/{uid}/claims",
	} {
		assert.NotNilf(t, paths[p], "operation %s must be registered", p)
	}
	for _, p := range []string{
		"/api/v1/catalog/works/{id}/claim-actions/{action}",
		"/api/v1/catalog/works/submit",
	} {
		assert.Nilf(t, paths[p], "operation %s must NOT be registered on the S2S face", p)
	}
}

func TestSetupAdmin_RegistersClaimQueue(t *testing.T) {
	api := SetupAdmin(fiber.New(), nil, nil, nil, nil)
	paths := api.OpenAPI().Paths
	for _, p := range []string{
		"/api/v1/admin/catalog/claims/pending",
		"/api/v1/admin/catalog/claims/{id}/{action}",
	} {
		assert.NotNilf(t, paths[p], "operation %s must be registered", p)
	}
}

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

func editGet(t *testing.T, app *fiber.App, url string) (int, []byte) {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest("GET", url, nil))
	require.NoError(t, err)
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, raw
}

func actOnClaim(t *testing.T, claims *service.ClaimLifecycleService, p service.ClaimActionParams) *service.ClaimActionResult {
	t.Helper()
	res, err := claims.Act(t.Context(), p)
	require.NoError(t, err)
	return res
}

func TestClaimEventFeedOverHTTP(t *testing.T) {
	db := openCatalogTestDB(t)
	for _, tbl := range []string{"catalog_claim_event", "catalog_work_title", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	work := &model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "投稿", Status: model.WorkStatusLive}
	require.NoError(t, db.Create(work).Error)

	claims := service.NewClaimLifecycleService(db)
	app := lifecycleApp(&siteModel.OAuthClient{ID: "kungal-client", CatalogSite: "kungal"}, claims)

	productWorkID := int64(777)
	claimed := actOnClaim(t, claims, service.ClaimActionParams{
		WorkID: work.ID, Action: service.ClaimActionClaim, Site: "kungal",
		ProductWorkID: &productWorkID, ActorUID: 5,
	})
	assert.Nil(t, claimed.From, "the birth event carries no prior state")
	assert.Equal(t, model.ClaimStateKeyDraft, claimed.To)

	actOnClaim(t, claims, service.ClaimActionParams{
		WorkID: work.ID, Action: service.ClaimActionPublish, Site: "kungal", ActorUID: 5,
	})
	actOnClaim(t, claims, service.ClaimActionParams{
		WorkID: work.ID, Action: service.ClaimActionBan, ActorUID: 9, Reason: "policy",
	})

	status, raw := editGet(t, app, "/api/v1/catalog/claim-events/feed?since=0&limit=10")
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
