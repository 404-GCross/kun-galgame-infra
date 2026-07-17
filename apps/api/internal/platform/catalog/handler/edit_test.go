package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"api/internal/platform/authz"
	"api/internal/platform/catalog/editspec"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/perm"
	"api/internal/platform/editing"
	siteModel "api/internal/platform/site/model"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestSetupEdit_RegistersOperations: spec smoke — the whole edit face
// registers with a nil engine (the gen-openapi convention; handlers never
// run during spec export).
func TestSetupEdit_RegistersOperations(t *testing.T) {
	api := Setup(fiber.New(), nil, nil, nil, nil, nil)
	SetupEdit(api, nil, nil)
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
		"/api/v1/catalog/edit/snapshot",
	} {
		assert.NotNilf(t, paths[p], "operation %s must be registered", p)
	}
}

// editApp mirrors claimApp for the edit face: the /api/v1/catalog prefix
// injects the given client (standing in for S2SAuth), and the edit face is
// registered over the supplied engine with the catalog family's resolver
// (the E0/E1 posture; multi-family routing has its own test below).
func editApp(client *siteModel.OAuthClient, engine *editing.Engine) *fiber.App {
	return editAppWithPerms(client, engine, PermResolvers{"catalog": perm.Resolver})
}

func editAppWithPerms(client *siteModel.OAuthClient, engine *editing.Engine, perms PermResolvers) *fiber.App {
	app := fiber.New()
	app.Use("/api/v1/catalog", func(c fiber.Ctx) error {
		if client != nil {
			c.Locals(localClient, client)
		}
		return c.Next()
	})
	api := Setup(app, nil, nil, nil, nil, nil)
	SetupEdit(api, engine, perms)
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

// fakeFamilySpec registers a minimal entity type for the family-routing
// tests: a fixed snapshot, a pass-through Txn (Apply is a no-op), and one
// perm-gated field so every policy decision goes through HasPerm.
func fakeFamilySpec(family string) editing.EntityTypeSpec {
	typ := family + ".thing"
	return editing.EntityTypeSpec{
		Family: family,
		Type:   typ,
		LoadSnapshot: func(ctx context.Context, entityID int64) (map[string]any, error) {
			return map[string]any{typ + ".name": "current"}, nil
		},
		Txn: func(ctx context.Context, fn func(tx *gorm.DB) error) error { return fn(nil) },
		DefaultPolicy: editing.Policy{
			Propose:   editing.ProposePerm(family + ".edit"),
			Review:    editing.ReviewPerm(family + ".review"),
			Automerge: editing.AutomergeNever,
		},
		Fields: []editing.FieldSpec{{
			Key: typ + ".name", Kind: editing.KindText, DiffHint: editing.DiffHintInline,
			Validate: func(any) error { return nil },
			Apply:    func(context.Context, *gorm.DB, int64, any) error { return nil },
		}},
	}
}

// TestEditFamilyResolver pins E3a ruling 1: the face routes an asserted
// actor's roles through the vocabulary of the entity's OWN family — no
// hardcoded family name, and a family absent from the resolver map fails
// closed even for a role that exists in another family's bundles. The
// proposal-directed leg (merge) proves the stored entity_family routes too.
func TestEditFamilyResolver(t *testing.T) {
	db := openCatalogTestDB(t)
	for _, tbl := range []string{"edit_proposal_amendment", "edit_proposal", "edit_revision"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}

	reg := editing.NewRegistry()
	require.NoError(t, reg.Register(fakeFamilySpec("widget")))
	require.NoError(t, reg.Register(fakeFamilySpec("gizmo")))
	require.NoError(t, reg.Register(fakeFamilySpec("orphan"))) // NOT in the resolver map
	engine := editing.NewEngine(db, reg)

	app := editAppWithPerms(&siteModel.OAuthClient{ID: "c", CatalogSite: "site-a"}, engine, PermResolvers{
		"widget": authz.NewResolver(authz.Bundles{"editor": {"widget.edit", "widget.review"}}),
		"gizmo":  authz.NewResolver(authz.Bundles{"boss": {"gizmo.edit"}}),
	})

	propose := func(family string, roles string) (int, []byte) {
		t.Helper()
		return editPost(t, app, "/api/v1/catalog/edit/proposals", fmt.Sprintf(
			`{"entity_type":"%s.thing","entity_id":1,"site":"site-a",
			  "patch":{"%s.thing.name":"new"},"actor":{"user_id":5,"roles":[%s]}}`,
			family, family, roles))
	}

	// The widget vocabulary grants "editor"; the gizmo vocabulary does not —
	// the SAME role set must pass one family and fail the other.
	status, raw := propose("widget", `"editor"`)
	require.Equal(t, fiber.StatusOK, status, string(raw))
	status, _ = propose("gizmo", `"editor"`)
	assert.Equal(t, fiber.StatusForbidden, status, "gizmo must not honor widget's role")
	status, _ = propose("gizmo", `"boss"`)
	assert.Equal(t, fiber.StatusOK, status)
	status, _ = propose("widget", `"boss"`)
	assert.Equal(t, fiber.StatusForbidden, status, "widget must not honor gizmo's role")

	// Unmapped family: fail closed no matter the roles.
	status, _ = propose("orphan", `"editor","boss"`)
	assert.Equal(t, fiber.StatusForbidden, status, "a family with no resolver fails closed")

	// Proposal-directed ops route through the STORED entity_family: the
	// widget proposal (id 1) merges under widget.review.
	var created struct {
		Data struct {
			Proposal struct {
				ID int64 `json:"id"`
			} `json:"proposal"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &created))
	status, _ = editPost(t, app,
		fmt.Sprintf("/api/v1/catalog/edit/proposals/%d/merge", created.Data.Proposal.ID),
		`{"actor":{"user_id":6,"roles":["boss"]}}`)
	assert.Equal(t, fiber.StatusForbidden, status, "gizmo's role must not review a widget proposal")
	status, raw = editPost(t, app,
		fmt.Sprintf("/api/v1/catalog/edit/proposals/%d/merge", created.Data.Proposal.ID),
		`{"actor":{"user_id":6,"roles":["editor"]}}`)
	assert.Equal(t, fiber.StatusOK, status, string(raw))
}

// TestEditSnapshot pins the bootstrap read: the entity's CURRENT registered
// field values through the spec's LoadSnapshot (not a stored revision).
func TestEditSnapshot(t *testing.T) {
	db := openCatalogTestDB(t)
	reg := editing.NewRegistry()
	require.NoError(t, reg.Register(fakeFamilySpec("widget")))
	app := editAppWithPerms(nil, editing.NewEngine(db, reg), nil)

	req := httptest.NewRequest("GET", "/api/v1/catalog/edit/snapshot?entity_type=widget.thing&entity_id=1", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	require.Equal(t, fiber.StatusOK, resp.StatusCode)
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var out struct {
		Data struct {
			EntityType string         `json:"entity_type"`
			EntityID   int64          `json:"entity_id"`
			Values     map[string]any `json:"values"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &out))
	assert.Equal(t, "widget.thing", out.Data.EntityType)
	assert.Equal(t, int64(1), out.Data.EntityID)
	assert.Equal(t, map[string]any{"widget.thing.name": "current"}, out.Data.Values)

	// Unknown entity type → 404.
	req = httptest.NewRequest("GET", "/api/v1/catalog/edit/snapshot?entity_type=ghost.thing&entity_id=1", nil)
	resp, err = app.Test(req)
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusNotFound, resp.StatusCode)
}
