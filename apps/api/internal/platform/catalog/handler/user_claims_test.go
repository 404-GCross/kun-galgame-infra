package handler

import (
	"encoding/json"
	"fmt"
	"testing"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/service"
	siteModel "api/internal/platform/site/model"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type userClaimsBody struct {
	Data struct {
		Items []struct {
			WorkID       int64   `json:"work_id"`
			DisplayName  string  `json:"display_name"`
			ClaimState   string  `json:"claim_state"`
			LastToState  string  `json:"last_to_state"`
			LastReason   *string `json:"last_reason"`
			LastEventID  int64   `json:"last_event_id"`
			LastActorUID int64   `json:"last_actor_uid"`
			ActedCount   int     `json:"acted_count"`
		} `json:"items"`
		NextBefore int64 `json:"next_before"`
		Total      int64 `json:"total"`
	} `json:"data"`
}

func TestUserClaimsFace(t *testing.T) {
	db := openCatalogTestDB(t)
	for _, tbl := range []string{"catalog_claim_event", "catalog_work_title", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	claims := service.NewClaimLifecycleService(db)
	app := lifecycleApp(&siteModel.OAuthClient{ID: "kungal-client", CatalogSite: "kungal"}, claims)

	var ids []int64
	for i, name := range []string{"一作目", "二作目"} {
		w := &model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: name, Status: model.WorkStatusLive}
		require.NoError(t, db.Create(w).Error)
		ids = append(ids, w.ID)
		productWorkID := int64(6100 + i)
		actOnClaim(t, claims, service.ClaimActionParams{
			WorkID: w.ID, Action: service.ClaimActionClaim, Site: "kungal",
			ProductWorkID: &productWorkID, ActorUID: 5,
		})
	}
	actOnClaim(t, claims, service.ClaimActionParams{
		WorkID: ids[0], Action: service.ClaimActionSubmit, Site: "kungal", ActorUID: 5,
	})
	actOnClaim(t, claims, service.ClaimActionParams{
		WorkID: ids[0], Action: service.ClaimActionDecline, ActorUID: 77, Reason: "重複",
	})

	status, raw := editGet(t, app, "/api/v1/catalog/users/5/claims")
	require.Equal(t, fiber.StatusOK, status, string(raw))
	var page userClaimsBody
	require.NoError(t, json.Unmarshal(raw, &page))
	require.Len(t, page.Data.Items, 2)
	assert.EqualValues(t, 2, page.Data.Total)
	head := page.Data.Items[0]
	assert.Equal(t, ids[0], head.WorkID, "most recent activity first")
	assert.Equal(t, model.ClaimStateKeyDeclined, head.ClaimState)
	assert.Equal(t, model.ClaimStateKeyDeclined, head.LastToState)
	assert.EqualValues(t, 77, head.LastActorUID, "the verdict's author, not the submitter")
	require.NotNil(t, head.LastReason)
	assert.Equal(t, "重複", *head.LastReason)
	assert.Equal(t, 2, head.ActedCount, "the user's own moves on that work")

	status, raw = editGet(t, app, "/api/v1/catalog/users/5/claims?claim_state=draft")
	require.Equal(t, fiber.StatusOK, status, string(raw))
	var drafts userClaimsBody
	require.NoError(t, json.Unmarshal(raw, &drafts))
	require.Len(t, drafts.Data.Items, 1)
	assert.EqualValues(t, 1, drafts.Data.Total)
	assert.Equal(t, ids[1], drafts.Data.Items[0].WorkID)

	status, raw = editGet(t, app, "/api/v1/catalog/users/5/claims?limit=1")
	require.Equal(t, fiber.StatusOK, status, string(raw))
	var first userClaimsBody
	require.NoError(t, json.Unmarshal(raw, &first))
	require.Len(t, first.Data.Items, 1)
	require.NotZero(t, first.Data.NextBefore)

	status, raw = editGet(t, app, fmt.Sprintf("/api/v1/catalog/users/5/claims?limit=1&before=%d", first.Data.NextBefore))
	require.Equal(t, fiber.StatusOK, status, string(raw))
	var second userClaimsBody
	require.NoError(t, json.Unmarshal(raw, &second))
	require.Len(t, second.Data.Items, 1)
	assert.NotEqual(t, first.Data.Items[0].WorkID, second.Data.Items[0].WorkID)

	status, raw = editGet(t, app, fmt.Sprintf("/api/v1/catalog/users/5/claims?limit=1&before=%d", second.Data.NextBefore))
	require.Equal(t, fiber.StatusOK, status, string(raw))
	var end userClaimsBody
	require.NoError(t, json.Unmarshal(raw, &end))
	assert.Empty(t, end.Data.Items)
	assert.Zero(t, end.Data.NextBefore, "an exhausted cursor is 0, not a rewind")

	status, raw = editGet(t, app, "/api/v1/catalog/users/6/claims")
	require.Equal(t, fiber.StatusOK, status, string(raw))
	var stranger userClaimsBody
	require.NoError(t, json.Unmarshal(raw, &stranger))
	assert.Empty(t, stranger.Data.Items)
	assert.Zero(t, stranger.Data.Total)

	status, _ = editGet(t, app, "/api/v1/catalog/users/5/claims?claim_state=frobnicate")
	assert.Equal(t, fiber.StatusBadRequest, status)
}

func TestClaimEventFeedActorFilter(t *testing.T) {
	db := openCatalogTestDB(t)
	for _, tbl := range []string{"catalog_claim_event", "catalog_work_title", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	claims := service.NewClaimLifecycleService(db)
	app := lifecycleApp(&siteModel.OAuthClient{ID: "kungal-client", CatalogSite: "kungal"}, claims)

	w := &model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "フィード対象", Status: model.WorkStatusLive}
	require.NoError(t, db.Create(w).Error)
	productWorkID := int64(6200)
	actOnClaim(t, claims, service.ClaimActionParams{
		WorkID: w.ID, Action: service.ClaimActionClaim, Site: "kungal",
		ProductWorkID: &productWorkID, ActorUID: 5,
	})
	actOnClaim(t, claims, service.ClaimActionParams{
		WorkID: w.ID, Action: service.ClaimActionSubmit, Site: "kungal", ActorUID: 5,
	})
	actOnClaim(t, claims, service.ClaimActionParams{
		WorkID: w.ID, Action: service.ClaimActionApprove, ActorUID: 77,
	})

	var feed struct {
		Data struct {
			Items []struct {
				ActorUID int64 `json:"actor_uid"`
			} `json:"items"`
		} `json:"data"`
	}
	status, raw := editGet(t, app, "/api/v1/catalog/claim-events/feed?actor_uid=5")
	require.Equal(t, fiber.StatusOK, status, string(raw))
	require.NoError(t, json.Unmarshal(raw, &feed))
	require.Len(t, feed.Data.Items, 2)
	for _, it := range feed.Data.Items {
		assert.EqualValues(t, 5, it.ActorUID)
	}

	status, raw = editGet(t, app, "/api/v1/catalog/claim-events/feed")
	require.Equal(t, fiber.StatusOK, status, string(raw))
	require.NoError(t, json.Unmarshal(raw, &feed))
	assert.Len(t, feed.Data.Items, 3, "the unfiltered feed is untouched")
}
