package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"api/internal/platform/catalog/editspec"
	"api/internal/platform/catalog/model"
	"api/internal/platform/editing"
	siteModel "api/internal/platform/site/model"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSetupEdit_RegistersOperations: spec smoke — the whole edit face
// registers with a nil engine (the gen-openapi convention; handlers never
// run during spec export).
func TestSetupEdit_RegistersOperations(t *testing.T) {
	api := Setup(fiber.New(), nil, nil, nil, nil, nil)
	SetupEdit(api, nil)
	paths := api.OpenAPI().Paths
	for _, p := range []string{
		"/api/v1/catalog/edit/proposals",
		"/api/v1/catalog/edit/proposals/{id}",
		"/api/v1/catalog/edit/proposals/{id}/amendments",
		"/api/v1/catalog/edit/proposals/{id}/merge",
		"/api/v1/catalog/edit/proposals/{id}/decline",
		"/api/v1/catalog/edit/proposals/{id}/withdraw",
		"/api/v1/catalog/edit/revisions",
		"/api/v1/catalog/edit/diff",
		"/api/v1/catalog/edit/revert",
		"/api/v1/catalog/edit/schema/{entity_type}",
	} {
		assert.NotNilf(t, paths[p], "operation %s must be registered", p)
	}
}

// editApp mirrors claimApp for the edit face: the /api/v1/catalog prefix
// injects the given client (standing in for S2SAuth), and the edit face is
// registered over the supplied engine.
func editApp(client *siteModel.OAuthClient, engine *editing.Engine) *fiber.App {
	app := fiber.New()
	app.Use("/api/v1/catalog", func(c fiber.Ctx) error {
		if client != nil {
			c.Locals(localClient, client)
		}
		return c.Next()
	})
	api := Setup(app, nil, nil, nil, nil, nil)
	SetupEdit(api, engine)
	return app
}

func editPost(t *testing.T, app *fiber.App, path, body string) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, raw
}

// TestEditSiteBinding_Forbidden: write paths 403 for unbound/mismatched
// clients BEFORE the engine is touched (nil engine is never reached).
func TestEditSiteBinding_Forbidden(t *testing.T) {
	body := `{"entity_type":"catalog.work","entity_id":1,"site":"letmoe",` +
		`"patch":{"catalog.work.display_name":"x"},"actor":{"user_id":1}}`
	for _, tc := range []struct {
		name   string
		client *siteModel.OAuthClient
	}{
		{"unbound", &siteModel.OAuthClient{ID: "c1", CatalogSite: ""}},
		{"wrong-site", &siteModel.OAuthClient{ID: "c2", CatalogSite: "kungal"}},
	} {
		app := editApp(tc.client, nil)
		status, _ := editPost(t, app, "/api/v1/catalog/edit/proposals", body)
		assert.Equalf(t, fiber.StatusForbidden, status, "%s must 403", tc.name)
	}
}

// TestEditFaceEndToEnd drives the pilot over HTTP: propose (403 for a plain
// user, open for admin) → merge (ren) → revision list + schema projection.
// DB-backed — skips when the catalog test database is unreachable.
func TestEditFaceEndToEnd(t *testing.T) {
	db := openCatalogTestDB(t)
	for _, tbl := range []string{"edit_proposal_amendment", "edit_proposal", "edit_revision", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	work := &model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "面テスト", Status: model.WorkStatusLive}
	require.NoError(t, db.Create(work).Error)

	reg := editing.NewRegistry()
	require.NoError(t, editspec.RegisterWork(reg, db))
	engine := editing.NewEngine(db, reg)
	app := editApp(&siteModel.OAuthClient{ID: "letmoe-client", CatalogSite: "letmoe"}, engine)

	// A plain user fails the propose rule → 403.
	status, _ := editPost(t, app, "/api/v1/catalog/edit/proposals", fmt.Sprintf(
		`{"entity_type":"catalog.work","entity_id":%d,"site":"letmoe",
		  "patch":{"catalog.work.display_name":"だめ"},"actor":{"user_id":9,"roles":["user"]}}`, work.ID))
	assert.Equal(t, fiber.StatusForbidden, status)

	// An unknown field key → 422.
	status, _ = editPost(t, app, "/api/v1/catalog/edit/proposals", fmt.Sprintf(
		`{"entity_type":"catalog.work","entity_id":%d,"site":"letmoe",
		  "patch":{"catalog.work.ghost":"x"},"actor":{"user_id":9,"roles":["admin"]}}`, work.ID))
	assert.Equal(t, fiber.StatusUnprocessableEntity, status)

	// Admin proposes → 200, proposal open, not merged (automerge=never).
	status, raw := editPost(t, app, "/api/v1/catalog/edit/proposals", fmt.Sprintf(
		`{"entity_type":"catalog.work","entity_id":%d,"site":"letmoe","note":"改名",
		  "patch":{"catalog.work.display_name":"新しい名前"},"actor":{"user_id":100,"roles":["admin"]}}`, work.ID))
	require.Equal(t, fiber.StatusOK, status, string(raw))
	var created struct {
		Data struct {
			Proposal struct {
				ID     int64  `json:"id"`
				Status string `json:"status"`
			} `json:"proposal"`
			Merged bool `json:"merged"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &created))
	assert.Equal(t, "open", created.Data.Proposal.Status)
	assert.False(t, created.Data.Merged)

	// ren merges → 200 with the produced revision.
	status, raw = editPost(t, app,
		fmt.Sprintf("/api/v1/catalog/edit/proposals/%d/merge", created.Data.Proposal.ID),
		`{"note":"ok","actor":{"user_id":200,"roles":["ren"]}}`)
	require.Equal(t, fiber.StatusOK, status, string(raw))
	var mergedResp struct {
		Data struct {
			Seq           int      `json:"seq"`
			Action        string   `json:"action"`
			ChangedFields []string `json:"changed_fields"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &mergedResp))
	assert.Equal(t, 1, mergedResp.Data.Seq)
	assert.Equal(t, "merged", mergedResp.Data.Action)
	assert.Equal(t, []string{"catalog.work.display_name"}, mergedResp.Data.ChangedFields)

	var after model.CatalogWork
	require.NoError(t, db.First(&after, work.ID).Error)
	assert.Equal(t, "新しい名前", after.DisplayName)

	// Revision log over HTTP.
	req := httptest.NewRequest("GET", fmt.Sprintf(
		"/api/v1/catalog/edit/revisions?entity_type=catalog.work&entity_id=%d", work.ID), nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusOK, resp.StatusCode)

	// Schema projection: an admin can propose+review, would not automerge; a
	// plain user gets an all-false projection.
	req = httptest.NewRequest("GET",
		"/api/v1/catalog/edit/schema/catalog.work?site=letmoe&user_id=100&roles=admin", nil)
	resp, err = app.Test(req)
	require.NoError(t, err)
	require.Equal(t, fiber.StatusOK, resp.StatusCode)
	raw, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	var schema struct {
		Data struct {
			Fields []struct {
				Key            string `json:"key"`
				CanPropose     bool   `json:"can_propose"`
				CanReview      bool   `json:"can_review"`
				WouldAutomerge bool   `json:"would_automerge"`
			} `json:"fields"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &schema))
	require.Len(t, schema.Data.Fields, 3)
	for _, f := range schema.Data.Fields {
		assert.True(t, f.CanPropose, f.Key)
		assert.True(t, f.CanReview, f.Key)
		assert.False(t, f.WouldAutomerge, f.Key)
	}
}
