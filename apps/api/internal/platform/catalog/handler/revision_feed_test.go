package handler

import (
	"encoding/json"
	"testing"

	"api/internal/platform/catalog/editspec"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/service"
	"api/internal/platform/editing"
	siteModel "api/internal/platform/site/model"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type revisionFeedBody struct {
	Data struct {
		Items []struct {
			ID            int64  `json:"id"`
			EntityType    string `json:"entity_type"`
			EntityID      int64  `json:"entity_id"`
			Site          string `json:"site"`
			ProductWorkID *int64 `json:"product_work_id"`
			ActorUID      int64  `json:"actor_uid"`
		} `json:"items"`
		NextSince int64 `json:"next_since"`
	} `json:"data"`
}

func mkRevision(t *testing.T, db *gorm.DB, entityType string, entityID int64, site string, seq int) {
	t.Helper()
	require.NoError(t, db.Create(&editing.Revision{
		EntityFamily: "catalog", EntityType: entityType, EntityID: entityID, Seq: seq,
		Action: 2, ChangedFields: datatypes.JSON(`["display_name"]`), Snapshot: datatypes.JSON(`{}`),
		ActorUID: 5, Site: site,
	}).Error)
}

func TestRevisionFeedProjectsProductWorkID(t *testing.T) {
	db := openCatalogTestDB(t)
	for _, tbl := range []string{"edit_revision", "catalog_work_title", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}

	site, product := "kungal", int64(777)
	claimed := &model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "claimed",
		Status: model.WorkStatusLive, Site: &site, ProductWorkID: &product}
	require.NoError(t, db.Create(claimed).Error)
	unclaimed := &model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "unclaimed",
		Status: model.WorkStatusLive}
	require.NoError(t, db.Create(unclaimed).Error)

	mkRevision(t, db, editspec.TypeWork, claimed.ID, "kungal", 1)
	mkRevision(t, db, editspec.TypeWork, unclaimed.ID, "kungal", 1)
	mkRevision(t, db, editspec.TypeWork, claimed.ID, "moyu", 2)
	mkRevision(t, db, "catalog.label", claimed.ID, "kungal", 1)

	app := fiber.New()
	app.Use("/api/v1/catalog", func(c fiber.Ctx) error {
		c.Locals(localClient, &siteModel.OAuthClient{ID: "kungal-client", CatalogSite: "kungal"})
		return c.Next()
	})
	api := Setup(app, nil, nil, nil, nil, nil)
	SetupLifecycle(api, service.NewClaimLifecycleService(db),
		editing.NewEngine(db, editing.NewRegistry()), nil)

	status, raw := editGet(t, app, "/api/v1/catalog/edit-revisions/feed?since=0&limit=100")
	require.Equal(t, fiber.StatusOK, status, string(raw))
	var feed revisionFeedBody
	require.NoError(t, json.Unmarshal(raw, &feed))
	require.Len(t, feed.Data.Items, 4)

	assert.Equal(t, claimed.ID, feed.Data.Items[0].EntityID)
	require.NotNil(t, feed.Data.Items[0].ProductWorkID, "a claimed work carries its product id")
	assert.Equal(t, product, *feed.Data.Items[0].ProductWorkID)
	assert.Nil(t, feed.Data.Items[1].ProductWorkID, "an unclaimed work has no product id")
	assert.Nil(t, feed.Data.Items[2].ProductWorkID, "another tenant's revision resolves to nothing")
	assert.Nil(t, feed.Data.Items[3].ProductWorkID, "only catalog.work is projected")
	assert.Equal(t, feed.Data.Items[3].ID, feed.Data.NextSince)

	status, raw = editGet(t, app, "/api/v1/catalog/edit-revisions/feed?since=0&site=moyu")
	require.Equal(t, fiber.StatusOK, status, string(raw))
	var moyu revisionFeedBody
	require.NoError(t, json.Unmarshal(raw, &moyu))
	require.Len(t, moyu.Data.Items, 1)
	assert.Equal(t, "moyu", moyu.Data.Items[0].Site)

	status, raw = editGet(t, app,
		"/api/v1/catalog/edit-revisions/feed?since=0&limit=1&entity_type=catalog.work&site=kungal")
	require.Equal(t, fiber.StatusOK, status, string(raw))
	var page revisionFeedBody
	require.NoError(t, json.Unmarshal(raw, &page))
	require.Len(t, page.Data.Items, 1)
	assert.Equal(t, claimed.ID, page.Data.Items[0].EntityID)

	status, raw = editGet(t, app,
		"/api/v1/catalog/edit-revisions/feed?since="+itoa(page.Data.NextSince)+"&limit=10&entity_type=catalog.work&site=kungal")
	require.Equal(t, fiber.StatusOK, status, string(raw))
	var rest revisionFeedBody
	require.NoError(t, json.Unmarshal(raw, &rest))
	require.Len(t, rest.Data.Items, 1)
	assert.Equal(t, unclaimed.ID, rest.Data.Items[0].EntityID)
}
