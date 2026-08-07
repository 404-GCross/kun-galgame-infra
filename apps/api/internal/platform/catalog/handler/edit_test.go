package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
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
// run during spec export). The second list is the 181/185 retirement stated as
// a test: those paths belong to the user plane now, and re-registering one here
// would put an asserted-actor door back on a write.
func TestSetupEdit_RegistersOperations(t *testing.T) {
	api := Setup(fiber.New(), nil, nil, nil, nil, nil)
	SetupEdit(api, nil, nil)
	paths := api.OpenAPI().Paths
	for _, p := range []string{
		"/api/v1/catalog/edit/proposals",
		"/api/v1/catalog/edit/revisions",
		"/api/v1/catalog/edit/diff",
	} {
		assert.NotNilf(t, paths[p], "operation %s must be registered", p)
	}
	for _, p := range []string{
		"/api/v1/catalog/edit/proposals/{id}",
		"/api/v1/catalog/edit/proposals/{id}/withdraw",
		"/api/v1/catalog/edit/proposals/{id}/amendments",
		"/api/v1/catalog/edit/proposals/{id}/merge",
		"/api/v1/catalog/edit/proposals/{id}/decline",
		"/api/v1/catalog/edit/revert",
		"/api/v1/catalog/edit/snapshot",
		"/api/v1/catalog/edit/schema/{entity_type}",
		"/api/v1/catalog/works/{workID}/covers/{coverID}/vote",
	} {
		assert.Nilf(t, paths[p], "operation %s must NOT be registered on the S2S face", p)
	}
	// The proposal path survives for the LIST and only for the list: wave 185
	// took the create verb off it, so the same path must answer GET and nothing
	// else. Asserted on the verb rather than on the path, because a path-level
	// check cannot see a write hiding under a surviving read.
	require.NotNil(t, paths["/api/v1/catalog/edit/proposals"].Get)
	assert.Nil(t, paths["/api/v1/catalog/edit/proposals"].Post,
		"filing a proposal belongs to the user plane")
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
		// Wave 185's three, on the same footing.
		{"POST", "/api/v1/catalog/edit/proposals"},
		{"POST", "/api/v1/catalog/edit/proposals/1/withdraw"},
		{"GET", "/api/v1/catalog/edit/schema/catalog.work"},
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

// mergeViaEngine / proposeViaEngine / schemaViaEngine reach the engine with an
// asserted actor. The S2S merge op retired in wave 181 and create / withdraw /
// schema followed in wave 185, so every one of those verbs over HTTP is pinned
// on the user plane (user_edit_test.go); the cases below are about what the
// ENGINE decides, so they reach it where it lives — still through policyCtx,
// because the family-routed permission resolution is part of what they claim.
func mergeViaEngine(t *testing.T, engine *editing.Engine, perms PermResolvers, id int64, actor dto.EditActor) (*editing.Revision, error) {
	t.Helper()
	prop, _, _, err := engine.GetProposal(context.Background(), id)
	require.NoError(t, err)
	s := &EditServer{engine: engine, perms: perms}
	return engine.MergeProposal(context.Background(), id, s.policyCtx(actor, prop.Site, prop.EntityFamily), "")
}

func proposeViaEngine(t *testing.T, engine *editing.Engine, perms PermResolvers, site string,
	actor dto.EditActor, entityType string, entityID int64, patch map[string]any, note string,
) (*editing.Proposal, *editing.Revision, error) {
	t.Helper()
	// Round-trip the patch through JSON so the engine sees what an HTTP body
	// would give it (all numbers as float64) — field validators assert the
	// wire shape, not Go-native fixture types.
	raw, err := json.Marshal(patch)
	require.NoError(t, err)
	var wire map[string]any
	require.NoError(t, json.Unmarshal(raw, &wire))
	s := &EditServer{engine: engine, perms: perms}
	return engine.CreateProposal(context.Background(), editing.CreateProposalInput{
		EntityType: entityType, EntityID: entityID, Patch: wire, Note: note,
		Actor: s.policyCtx(actor, site, familyOf(entityType)),
	})
}

func schemaViaEngine(t *testing.T, engine *editing.Engine, perms PermResolvers, site string,
	actor dto.EditActor, entityType string, entityID int64,
) []editing.FieldProjection {
	t.Helper()
	s := &EditServer{engine: engine, perms: perms}
	fields, err := engine.SchemaProjection(context.Background(), entityType, entityID,
		s.policyCtx(actor, site, familyOf(entityType)))
	require.NoError(t, err)
	return fields
}

// TestEditPolicyDefaultTenant drives the pilot on the NON-overlaid "nextmoe"
// tenant: propose (refused for a plain user, open for admin) → merge (ren) →
// the revision list over HTTP → the schema projection. The write legs reach the
// engine directly, the revision list is a surviving S2S read and is still
// driven over the wire. kungal and the letmoe sites both carry real overlays
// now (kungal: open propose + automerge=review; letmoe: trusted + owner),
// exercised by their own legs. DB-backed — skips when the catalog test
// database is unreachable.
func TestEditPolicyDefaultTenant(t *testing.T) {
	db := openCatalogTestDB(t)
	for _, tbl := range []string{"edit_proposal_amendment", "edit_proposal", "edit_revision", "catalog_work_title", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	work := &model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "面テスト", Status: model.WorkStatusLive}
	require.NoError(t, db.Create(work).Error)

	reg := editing.NewRegistry()
	require.NoError(t, editspec.RegisterWork(reg, db))
	engine := editing.NewEngine(db, reg)
	perms := PermResolvers{"catalog": perm.Resolver}
	app := editApp(&siteModel.OAuthClient{ID: "nextmoe-client", CatalogSite: "nextmoe"}, engine)

	// A plain user fails the propose rule.
	_, _, err := proposeViaEngine(t, engine, perms, "nextmoe",
		dto.EditActor{UserID: 9, Roles: []string{"user"}}, "catalog.work", work.ID,
		map[string]any{"catalog.work.display_name": "だめ"}, "")
	var permErr *editing.PermissionError
	assert.ErrorAs(t, err, &permErr, "a plain user may not propose on the default policy")

	// An unknown field key is refused whatever the role.
	_, _, err = proposeViaEngine(t, engine, perms, "nextmoe",
		dto.EditActor{UserID: 9, Roles: []string{"admin"}}, "catalog.work", work.ID,
		map[string]any{"catalog.work.ghost": "x"}, "")
	var unknownField *editing.UnknownFieldError
	assert.ErrorAs(t, err, &unknownField)

	// Admin proposes → an open proposal, not merged (automerge=never).
	prop, merged, err := proposeViaEngine(t, engine, perms, "nextmoe",
		dto.EditActor{UserID: 100, Roles: []string{"admin"}}, "catalog.work", work.ID,
		map[string]any{"catalog.work.display_name": "新しい名前"}, "改名")
	require.NoError(t, err)
	assert.Equal(t, "open", editing.StatusName[prop.Status])
	assert.Nil(t, merged)

	// ren merges → the produced revision.
	rev, err := mergeViaEngine(t, engine, perms, prop.ID,
		dto.EditActor{UserID: 200, Roles: []string{"ren"}})
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

	// Schema projection: an admin can propose+review, would not automerge.
	fields := schemaViaEngine(t, engine, perms, "nextmoe",
		dto.EditActor{UserID: 100, Roles: []string{"admin"}}, "catalog.work", 0)
	// The projection carries catalog.work's whole registered field table, which
	// wave 154 completed (03 定案 §2). Asserted as "every field is proposable
	// and reviewable for this actor, and none automerges" rather than as a
	// count, so adding a field to the matrix does not require editing a number
	// here — the policy claim is what this case is about.
	require.NotEmpty(t, fields)
	for _, f := range fields {
		assert.True(t, f.CanPropose, f.Key)
		assert.True(t, f.CanReview, f.Key)
		assert.False(t, f.WouldAutomerge, f.Key)
	}
}

// TestEditPolicyLetmoeTenant drives the E1 letmoe overlay: a trusted letmoe
// user direct-edits a letmoe-claimed work (owner automerge, single revision,
// roles empty), a below-tier user is refused, and the schema projection is
// entity-aware. The proposal LIST leg stays on the wire — it is a surviving
// S2S read.
func TestEditPolicyLetmoeTenant(t *testing.T) {
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
	engine := editing.NewEngine(db, reg)
	perms := PermResolvers{"catalog": perm.Resolver}
	app := editApp(&siteModel.OAuthClient{ID: "letmoe-client", CatalogSite: "letmoe"}, engine)

	// Below trusted tier → refused (propose=trusted on letmoe sites).
	_, _, err := proposeViaEngine(t, engine, perms, "letmoe",
		dto.EditActor{UserID: 9, TrustTier: 0}, "catalog.work", owned.ID,
		map[string]any{"catalog.work.display_name": "x"}, "")
	var permErr *editing.PermissionError
	assert.ErrorAs(t, err, &permErr)

	// Trusted letmoe user edits the OWNED work's titles → merged directly.
	_, direct, err := proposeViaEngine(t, engine, perms, "letmoe",
		dto.EditActor{UserID: 100, TrustTier: 2}, "catalog.work", owned.ID,
		map[string]any{"catalog.work.titles": []any{
			map[string]any{"lang": "ja", "title": "自家作品・改", "kind": 0},
		}}, "整理条目")
	require.NoError(t, err)
	require.NotNil(t, direct, "an owner's trusted edit automerges")
	assert.Equal(t, 1, direct.Seq)
	assert.Equal(t, "direct", editing.ActionName[direct.Action])
	var after model.CatalogWork
	require.NoError(t, db.First(&after, owned.ID).Error)
	assert.Equal(t, "自家作品・改", after.DisplayName) // derived from titles

	// The same trusted user on the PUBLIC work → open proposal.
	openProp, merged, err := proposeViaEngine(t, engine, perms, "letmoe",
		dto.EditActor{UserID: 100, TrustTier: 2}, "catalog.work", public.ID,
		map[string]any{"catalog.work.display_name": "公共作品・提案"}, "")
	require.NoError(t, err)
	assert.Nil(t, merged)
	assert.Equal(t, "open", editing.StatusName[openProp.Status])

	// "My proposals" filter (proposer_uid) — a surviving S2S read, over HTTP.
	req := httptest.NewRequest("GET",
		"/api/v1/catalog/edit/proposals?site=letmoe&proposer_uid=100&status=open", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	require.Equal(t, fiber.StatusOK, resp.StatusCode)
	raw, err := io.ReadAll(resp.Body)
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
	assert.Equal(t, openProp.ID, list.Data.Items[0].ID)
	// wave 162: the total is counted behind the SAME filter, so a product can
	// read "edits by this user" as a number instead of a page length.
	assert.EqualValues(t, 1, list.Data.Total)

	// Entity-aware schema projection: owned → would_automerge, public → not.
	wouldAutomerge := func(entityID int64) bool {
		t.Helper()
		fields := schemaViaEngine(t, engine, perms, "letmoe",
			dto.EditActor{UserID: 100, TrustTier: 2}, "catalog.work", entityID)
		require.NotEmpty(t, fields)
		all := true
		for _, f := range fields {
			all = all && f.WouldAutomerge
		}
		return all
	}
	assert.True(t, wouldAutomerge(owned.ID))
	assert.False(t, wouldAutomerge(public.ID))
}

// TestEditPolicyKungalOwner drives the kungal overlay's owner posture — the
// behaviour that regressed when the N5 re-anchoring dropped the overlay: the
// asserted entry creator (is_entity_owner, a plain user) direct-edits their
// claimed game; the same user without the assertion files an open proposal.
// The assertion itself is an S2S notion the user plane derives from the catalog
// instead (user_edit_test.go's ownership case); what is pinned here is the
// OVERLAY's reading of it, so the legs address the engine.
func TestEditPolicyKungalOwner(t *testing.T) {
	db := openCatalogTestDB(t)
	for _, tbl := range []string{"edit_proposal_amendment", "edit_proposal", "edit_revision", "catalog_work_title", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	work := &model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "認領作品", Status: model.WorkStatusLive}
	require.NoError(t, db.Create(work).Error)

	reg := editing.NewRegistry()
	require.NoError(t, editspec.RegisterWork(reg, db))
	engine := editing.NewEngine(db, reg)
	perms := PermResolvers{"catalog": perm.Resolver}

	// Without the ownership assertion: a plain user's edit files an open
	// proposal (propose=open on kungal, but no review capability → no automerge).
	_, merged, err := proposeViaEngine(t, engine, perms, "kungal",
		dto.EditActor{UserID: 300, Roles: []string{"user"}}, "catalog.work", work.ID,
		map[string]any{"catalog.work.display_name": "路人提案"}, "")
	require.NoError(t, err)
	assert.Nil(t, merged)

	// With is_entity_owner asserted: the creator's own edit merges directly
	// (automerge=review via OwnerReview) — the reported "认领的游戏可以直接编辑".
	_, direct, err := proposeViaEngine(t, engine, perms, "kungal",
		dto.EditActor{UserID: 300, Roles: []string{"user"}, IsEntityOwner: true},
		"catalog.work", work.ID,
		map[string]any{"catalog.work.display_name": "創建者直編"}, "")
	require.NoError(t, err)
	require.NotNil(t, direct)
	assert.Equal(t, "direct", editing.ActionName[direct.Action])
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

// TestEditFamilyResolver pins E3a ruling 1: policyCtx routes an actor's roles
// through the vocabulary of the entity's OWN family — no hardcoded family name,
// and a family absent from the resolver map fails closed even for a role that
// exists in another family's bundles. The proposal-directed leg (merge) proves
// the stored entity_family routes too.
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
	propose := func(family string, roles ...string) (*editing.Proposal, error) {
		t.Helper()
		prop, _, err := proposeViaEngine(t, engine, perms, "site-a",
			dto.EditActor{UserID: 5, Roles: roles}, family+".thing", 1,
			map[string]any{family + ".thing.name": "new"}, "")
		return prop, err
	}

	var permErr *editing.PermissionError

	// The widget vocabulary grants "editor"; the gizmo vocabulary does not —
	// the SAME role set must pass one family and fail the other.
	widgetProp, err := propose("widget", "editor")
	require.NoError(t, err)
	_, err = propose("gizmo", "editor")
	assert.ErrorAs(t, err, &permErr, "gizmo must not honor widget's role")
	_, err = propose("gizmo", "boss")
	assert.NoError(t, err)
	_, err = propose("widget", "boss")
	assert.ErrorAs(t, err, &permErr, "widget must not honor gizmo's role")

	// Unmapped family: fail closed no matter the roles.
	_, err = propose("orphan", "editor", "boss")
	assert.ErrorAs(t, err, &permErr, "a family with no resolver fails closed")

	// Proposal-directed ops route through the STORED entity_family: the
	// widget proposal merges under widget.review.
	_, err = mergeViaEngine(t, engine, perms, widgetProp.ID,
		dto.EditActor{UserID: 6, Roles: []string{"boss"}})
	assert.ErrorAs(t, err, &permErr, "gizmo's role must not review a widget proposal")
	_, err = mergeViaEngine(t, engine, perms, widgetProp.ID,
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
		prop, _, err := proposeViaEngine(t, engine, perms, "site-a",
			dto.EditActor{UserID: 5, Roles: []string{"editor"}}, "widget.thing", 1,
			map[string]any{"widget.thing.name": fmt.Sprintf("v%d", i)}, "")
		require.NoError(t, err)
		_, err = mergeViaEngine(t, engine, perms, prop.ID,
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
