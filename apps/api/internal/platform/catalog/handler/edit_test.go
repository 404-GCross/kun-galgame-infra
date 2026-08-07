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
	"api/internal/platform/catalog/dto"
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
// run during spec export). The second list is the wave-181 retirement stated as
// a test: those paths belong to the user plane now, and re-registering one here
// would put an asserted-actor door back on a moderation op.
func TestSetupEdit_RegistersOperations(t *testing.T) {
	api := Setup(fiber.New(), nil, nil, nil, nil, nil)
	SetupEdit(api, nil, nil)
	paths := api.OpenAPI().Paths
	for _, p := range []string{
		"/api/v1/catalog/edit/proposals",
		"/api/v1/catalog/edit/proposals/{id}/withdraw",
		"/api/v1/catalog/edit/revisions",
		"/api/v1/catalog/edit/diff",
		"/api/v1/catalog/edit/schema/{entity_type}",
	} {
		assert.NotNilf(t, paths[p], "operation %s must be registered", p)
	}
	for _, p := range []string{
		"/api/v1/catalog/edit/proposals/{id}",
		"/api/v1/catalog/edit/proposals/{id}/amendments",
		"/api/v1/catalog/edit/proposals/{id}/merge",
		"/api/v1/catalog/edit/proposals/{id}/decline",
		"/api/v1/catalog/edit/revert",
		"/api/v1/catalog/edit/snapshot",
		"/api/v1/catalog/works/{workID}/covers/{coverID}/vote",
	} {
		assert.Nilf(t, paths[p], "operation %s must NOT be registered on the S2S face", p)
	}
}

// TestS2SFace_RetiredPathsAreGone is the runtime half of the same claim: a
// retired path is not merely absent from the spec, it answers nothing.
func TestS2SFace_RetiredPathsAreGone(t *testing.T) {
	app := editApp(&siteModel.OAuthClient{ID: "c", CatalogSite: "site-a"}, nil)
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/v1/catalog/edit/snapshot?entity_type=widget.thing&entity_id=1"},
		{"GET", "/api/v1/catalog/edit/proposals/1"},
		{"POST", "/api/v1/catalog/edit/proposals/1/merge"},
		{"POST", "/api/v1/catalog/edit/revert"},
		{"PUT", "/api/v1/catalog/works/1/covers/1/vote"},
		{"POST", "/api/v1/catalog/edit/images"},
	} {
		resp, err := app.Test(httptest.NewRequest(tc.method, tc.path, nil))
		require.NoError(t, err)
		assert.Containsf(t, []int{fiber.StatusNotFound, fiber.StatusMethodNotAllowed},
			resp.StatusCode, "%s %s", tc.method, tc.path)
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

// mergeViaEngine merges a proposal through the engine with an asserted actor.
// The S2S merge op retired in wave 181 and merging over HTTP is pinned on the
// user plane (user_edit_test.go); the cases below are about what the ENGINE
// decides, so they reach it where it lives — still through policyCtx, because
// the family-routed permission resolution is part of what they claim.
func mergeViaEngine(t *testing.T, engine *editing.Engine, perms PermResolvers, id int64, actor dto.EditActor) (*editing.Revision, error) {
	t.Helper()
	prop, _, _, err := engine.GetProposal(context.Background(), id)
	require.NoError(t, err)
	s := &EditServer{engine: engine, perms: perms}
	return engine.MergeProposal(context.Background(), id, s.policyCtx(actor, prop.Site, prop.EntityFamily), "")
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
// The default-policy legs run on the NON-overlaid "nextmoe" tenant — kungal
// and the letmoe sites both carry real overlays now (kungal: open propose +
// automerge=review; letmoe: trusted + owner), exercised by their own legs. DB-backed — skips when the catalog test
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
	app := editApp(&siteModel.OAuthClient{ID: "nextmoe-client", CatalogSite: "nextmoe"}, engine)

	// A plain user fails the propose rule → 403.
	status, _ := editPost(t, app, "/api/v1/catalog/edit/proposals", fmt.Sprintf(
		`{"entity_type":"catalog.work","entity_id":%d,"site":"nextmoe",
		  "patch":{"catalog.work.display_name":"だめ"},"actor":{"user_id":9,"roles":["user"]}}`, work.ID))
	assert.Equal(t, fiber.StatusForbidden, status)

	// An unknown field key → 422.
	status, _ = editPost(t, app, "/api/v1/catalog/edit/proposals", fmt.Sprintf(
		`{"entity_type":"catalog.work","entity_id":%d,"site":"nextmoe",
		  "patch":{"catalog.work.ghost":"x"},"actor":{"user_id":9,"roles":["admin"]}}`, work.ID))
	assert.Equal(t, fiber.StatusUnprocessableEntity, status)

	// Admin proposes → 200, proposal open, not merged (automerge=never).
	status, raw := editPost(t, app, "/api/v1/catalog/edit/proposals", fmt.Sprintf(
		`{"entity_type":"catalog.work","entity_id":%d,"site":"nextmoe","note":"改名",
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

	// ren merges → the produced revision.
	rev, err := mergeViaEngine(t, engine, PermResolvers{"catalog": perm.Resolver},
		created.Data.Proposal.ID, dto.EditActor{UserID: 200, Roles: []string{"ren"}})
	require.NoError(t, err)
	view := revisionView(rev)
	assert.Equal(t, 1, view.Seq)
	assert.Equal(t, "merged", view.Action)
	assert.Equal(t, []string{"catalog.work.display_name"}, view.ChangedFields)

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
		"/api/v1/catalog/edit/schema/catalog.work?site=nextmoe&user_id=100&roles=admin", nil)
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
	// The projection carries catalog.work's whole registered field table, which
	// wave 154 completed (03 定案 §2). Asserted as "every field is proposable
	// and reviewable for this actor, and none automerges" rather than as a
	// count, so adding a field to the matrix does not require editing a number
	// here — the policy claim is what this case is about.
	require.NotEmpty(t, schema.Data.Fields)
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
			Total int64 `json:"total"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &list))
	require.Len(t, list.Data.Items, 1)
	assert.Equal(t, open.Data.Proposal.ID, list.Data.Items[0].ID)
	// wave 162: the total is counted behind the SAME filter, so a product can
	// read "edits by this user" as a number instead of a page length.
	assert.EqualValues(t, 1, list.Data.Total)

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

// TestEditFaceKungalOwner drives the kungal overlay's owner posture over HTTP
// — the wire this face regressed on when the N5 re-anchoring dropped the
// overlay: the product-asserted entry creator (is_entity_owner, a plain user)
// direct-edits their claimed game; the same user without the assertion files
// an open proposal.
func TestEditFaceKungalOwner(t *testing.T) {
	db := openCatalogTestDB(t)
	for _, tbl := range []string{"edit_proposal_amendment", "edit_proposal", "edit_revision", "catalog_work_title", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	work := &model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "認領作品", Status: model.WorkStatusLive}
	require.NoError(t, db.Create(work).Error)

	reg := editing.NewRegistry()
	require.NoError(t, editspec.RegisterWork(reg, db))
	app := editApp(&siteModel.OAuthClient{ID: "kungal-client", CatalogSite: "kungal"}, editing.NewEngine(db, reg))

	// Without the ownership assertion: a plain user's edit files an open
	// proposal (propose=open on kungal, but no review capability → no automerge).
	status, raw := editPost(t, app, "/api/v1/catalog/edit/proposals", fmt.Sprintf(
		`{"entity_type":"catalog.work","entity_id":%d,"site":"kungal",
		  "patch":{"catalog.work.display_name":"路人提案"},"actor":{"user_id":300,"roles":["user"]}}`, work.ID))
	require.Equal(t, fiber.StatusOK, status, string(raw))
	var open struct {
		Data struct {
			Merged bool `json:"merged"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &open))
	assert.False(t, open.Data.Merged)

	// With is_entity_owner asserted: the creator's own edit merges directly
	// (automerge=review via OwnerReview) — the reported "认领的游戏可以直接编辑".
	status, raw = editPost(t, app, "/api/v1/catalog/edit/proposals", fmt.Sprintf(
		`{"entity_type":"catalog.work","entity_id":%d,"site":"kungal",
		  "patch":{"catalog.work.display_name":"創建者直編"},
		  "actor":{"user_id":300,"roles":["user"],"is_entity_owner":true}}`, work.ID))
	require.Equal(t, fiber.StatusOK, status, string(raw))
	var direct struct {
		Data struct {
			Merged   bool `json:"merged"`
			Revision *struct {
				Action string `json:"action"`
			} `json:"revision"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &direct))
	assert.True(t, direct.Data.Merged)
	require.NotNil(t, direct.Data.Revision)
	assert.Equal(t, "direct", direct.Data.Revision.Action)
	var after model.CatalogWork
	require.NoError(t, db.First(&after, work.ID).Error)
	assert.Equal(t, "創建者直編", after.DisplayName)
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

	perms := PermResolvers{
		"widget": authz.NewResolver(authz.Bundles{"editor": {"widget.edit", "widget.review"}}),
		"gizmo":  authz.NewResolver(authz.Bundles{"boss": {"gizmo.edit"}}),
	}
	app := editAppWithPerms(&siteModel.OAuthClient{ID: "c", CatalogSite: "site-a"}, engine, perms)

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
	_, err := mergeViaEngine(t, engine, perms, created.Data.Proposal.ID,
		dto.EditActor{UserID: 6, Roles: []string{"boss"}})
	var permErr *editing.PermissionError
	assert.ErrorAs(t, err, &permErr, "gizmo's role must not review a widget proposal")
	_, err = mergeViaEngine(t, engine, perms, created.Data.Proposal.ID,
		dto.EditActor{UserID: 6, Roles: []string{"editor"}})
	assert.NoError(t, err)
}

// TestEditRevisionLegacyView: migrated rows' provenance (legacy_action +
// legacy_meta note/minor) reaches the wire; new-era rows carry none of it.
// The legacy columns are select-only on the engine model, so the test plants
// them with a raw UPDATE — exactly what the one-shot transform did.
func TestEditRevisionLegacyView(t *testing.T) {
	db := openCatalogTestDB(t)
	for _, tbl := range []string{"edit_proposal_amendment", "edit_proposal", "edit_revision"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	reg := editing.NewRegistry()
	require.NoError(t, reg.Register(fakeFamilySpec("widget")))
	engine := editing.NewEngine(db, reg)
	perms := PermResolvers{"widget": authz.NewResolver(authz.Bundles{"editor": {"widget.edit", "widget.review"}})}
	app := editAppWithPerms(&siteModel.OAuthClient{ID: "c", CatalogSite: "site-a"}, engine, perms)

	// Two merged revisions on entity 1: seq 1 will be dressed up as a
	// migrated row, seq 2 stays new-era.
	for i := 0; i < 2; i++ {
		status, raw := editPost(t, app, "/api/v1/catalog/edit/proposals", fmt.Sprintf(
			`{"entity_type":"widget.thing","entity_id":1,"site":"site-a",
			  "patch":{"widget.thing.name":"v%d"},"actor":{"user_id":5,"roles":["editor"]}}`, i))
		require.Equal(t, fiber.StatusOK, status, string(raw))
		var created struct {
			Data struct {
				Proposal struct {
					ID int64 `json:"id"`
				} `json:"proposal"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(raw, &created))
		_, err := mergeViaEngine(t, engine, perms, created.Data.Proposal.ID,
			dto.EditActor{UserID: 6, Roles: []string{"editor"}})
		require.NoError(t, err)
	}
	require.NoError(t, db.Exec(`UPDATE edit_revision
		SET legacy_action = 'claimed', legacy_meta = '{"note":"旧备注","is_minor":true}'
		WHERE entity_type = 'widget.thing' AND seq = 1`).Error)

	req := httptest.NewRequest("GET", "/api/v1/catalog/edit/revisions?entity_type=widget.thing&entity_id=1", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	require.Equal(t, fiber.StatusOK, resp.StatusCode)
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var out struct {
		Data struct {
			Items []struct {
				Seq          int    `json:"seq"`
				LegacyAction string `json:"legacy_action"`
				LegacyNote   string `json:"legacy_note"`
				LegacyMinor  bool   `json:"legacy_minor"`
			} `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &out))
	require.Len(t, out.Data.Items, 2) // newest first: seq 2, then seq 1
	assert.Empty(t, out.Data.Items[0].LegacyAction)
	assert.Empty(t, out.Data.Items[0].LegacyNote)
	assert.False(t, out.Data.Items[0].LegacyMinor)
	assert.Equal(t, "claimed", out.Data.Items[1].LegacyAction)
	assert.Equal(t, "旧备注", out.Data.Items[1].LegacyNote)
	assert.True(t, out.Data.Items[1].LegacyMinor)
}
