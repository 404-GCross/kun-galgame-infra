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
// The default-policy legs run on the NON-overlaid "kungal" tenant — since E1
// the letmoe sites carry a real overlay (trusted + owner), exercised by the
// dedicated letmoe leg below. DB-backed — skips when the catalog test
// database is unreachable.
func TestEditFaceEndToEnd(t *testing.T) {
	db := openCatalogTestDB(t)
	for _, tbl := range []string{"edit_proposal_amendment", "edit_proposal", "edit_revision", "catalog_work_title", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	work := &model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "面テスト", Status: model.WorkStatusLive}
	require.NoError(t, db.Create(work).Error)

	reg := editing.NewRegistry()
	require.NoError(t, editspec.RegisterWork(reg, db))
	engine := editing.NewEngine(db, reg)
	app := editApp(&siteModel.OAuthClient{ID: "kungal-client", CatalogSite: "kungal"}, engine)

	// A plain user fails the propose rule → 403.
	status, _ := editPost(t, app, "/api/v1/catalog/edit/proposals", fmt.Sprintf(
		`{"entity_type":"catalog.work","entity_id":%d,"site":"kungal",
		  "patch":{"catalog.work.display_name":"だめ"},"actor":{"user_id":9,"roles":["user"]}}`, work.ID))
	assert.Equal(t, fiber.StatusForbidden, status)

	// An unknown field key → 422.
	status, _ = editPost(t, app, "/api/v1/catalog/edit/proposals", fmt.Sprintf(
		`{"entity_type":"catalog.work","entity_id":%d,"site":"kungal",
		  "patch":{"catalog.work.ghost":"x"},"actor":{"user_id":9,"roles":["admin"]}}`, work.ID))
	assert.Equal(t, fiber.StatusUnprocessableEntity, status)

	// Admin proposes → 200, proposal open, not merged (automerge=never).
	status, raw := editPost(t, app, "/api/v1/catalog/edit/proposals", fmt.Sprintf(
		`{"entity_type":"catalog.work","entity_id":%d,"site":"kungal","note":"改名",
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
		"/api/v1/catalog/edit/schema/catalog.work?site=kungal&user_id=100&roles=admin", nil)
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
	require.Len(t, schema.Data.Fields, 4)
	for _, f := range schema.Data.Fields {
		assert.True(t, f.CanPropose, f.Key)
		assert.True(t, f.CanReview, f.Key)
		assert.False(t, f.WouldAutomerge, f.Key)
	}
}

// TestEditFaceLetmoeTenant drives the E1 letmoe overlay over HTTP: a trusted
// letmoe user direct-edits a letmoe-claimed work (owner automerge, single
// revision, roles asserted empty), a below-tier user 403s, and the schema
// projection is entity-aware through the entity_id query param.
func TestEditFaceLetmoeTenant(t *testing.T) {
	db := openCatalogTestDB(t)
	for _, tbl := range []string{"edit_proposal_amendment", "edit_proposal", "edit_revision", "catalog_work_title", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	site, productID := "letmoe", int64(77)
	owned := &model.CatalogWork{
		MediumID: 1, Site: &site, ProductWorkID: &productID,
		OLang: "ja", DisplayName: "自家作品", Status: model.WorkStatusLive,
	}
	require.NoError(t, db.Create(owned).Error)
	public := &model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "公共作品", Status: model.WorkStatusLive}
	require.NoError(t, db.Create(public).Error)

	reg := editing.NewRegistry()
	require.NoError(t, editspec.RegisterWork(reg, db))
	app := editApp(&siteModel.OAuthClient{ID: "letmoe-client", CatalogSite: "letmoe"}, editing.NewEngine(db, reg))

	// Below trusted tier → 403 (propose=trusted on letmoe sites).
	status, _ := editPost(t, app, "/api/v1/catalog/edit/proposals", fmt.Sprintf(
		`{"entity_type":"catalog.work","entity_id":%d,"site":"letmoe",
		  "patch":{"catalog.work.display_name":"x"},"actor":{"user_id":9,"trust_tier":0}}`, owned.ID))
	assert.Equal(t, fiber.StatusForbidden, status)

	// Trusted letmoe user edits the OWNED work's titles → merged directly.
	status, raw := editPost(t, app, "/api/v1/catalog/edit/proposals", fmt.Sprintf(
		`{"entity_type":"catalog.work","entity_id":%d,"site":"letmoe","note":"整理条目",
		  "patch":{"catalog.work.titles":[{"lang":"ja","title":"自家作品・改","kind":0}]},
		  "actor":{"user_id":100,"trust_tier":2}}`, owned.ID))
	require.Equal(t, fiber.StatusOK, status, string(raw))
	var created struct {
		Data struct {
			Merged   bool `json:"merged"`
			Revision *struct {
				Seq    int    `json:"seq"`
				Action string `json:"action"`
			} `json:"revision"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &created))
	assert.True(t, created.Data.Merged)
	require.NotNil(t, created.Data.Revision)
	assert.Equal(t, 1, created.Data.Revision.Seq)
	assert.Equal(t, "direct", created.Data.Revision.Action)
	var after model.CatalogWork
	require.NoError(t, db.First(&after, owned.ID).Error)
	assert.Equal(t, "自家作品・改", after.DisplayName) // derived from titles

	// The same trusted user on the PUBLIC work → open proposal.
	status, raw = editPost(t, app, "/api/v1/catalog/edit/proposals", fmt.Sprintf(
		`{"entity_type":"catalog.work","entity_id":%d,"site":"letmoe",
		  "patch":{"catalog.work.display_name":"公共作品・提案"},"actor":{"user_id":100,"trust_tier":2}}`, public.ID))
	require.Equal(t, fiber.StatusOK, status, string(raw))
	var open struct {
		Data struct {
			Merged   bool `json:"merged"`
			Proposal struct {
				ID     int64  `json:"id"`
				Status string `json:"status"`
			} `json:"proposal"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &open))
	assert.False(t, open.Data.Merged)
	assert.Equal(t, "open", open.Data.Proposal.Status)

	// "My proposals" filter (proposer_uid).
	req := httptest.NewRequest("GET",
		"/api/v1/catalog/edit/proposals?site=letmoe&proposer_uid=100&status=open", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	require.Equal(t, fiber.StatusOK, resp.StatusCode)
	raw, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	var list struct {
		Data struct {
			Items []struct {
				ID          int64 `json:"id"`
				ProposerUID int64 `json:"proposer_uid"`
			} `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &list))
	require.Len(t, list.Data.Items, 1)
	assert.Equal(t, open.Data.Proposal.ID, list.Data.Items[0].ID)

	// Entity-aware schema projection: owned → would_automerge, public → not.
	wouldAutomerge := func(entityID int64) bool {
		t.Helper()
		req := httptest.NewRequest("GET", fmt.Sprintf(
			"/api/v1/catalog/edit/schema/catalog.work?site=letmoe&user_id=100&trust_tier=2&entity_id=%d", entityID), nil)
		resp, err := app.Test(req)
		require.NoError(t, err)
		require.Equal(t, fiber.StatusOK, resp.StatusCode)
		raw, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		var schema struct {
			Data struct {
				Fields []struct {
					Key            string `json:"key"`
					WouldAutomerge bool   `json:"would_automerge"`
				} `json:"fields"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(raw, &schema))
		require.NotEmpty(t, schema.Data.Fields)
		all := true
		for _, f := range schema.Data.Fields {
			all = all && f.WouldAutomerge
		}
		return all
	}
	assert.True(t, wouldAutomerge(owned.ID))
	assert.False(t, wouldAutomerge(public.ID))
}
