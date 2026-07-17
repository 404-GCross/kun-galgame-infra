package editspec_test

import (
	"errors"
	"testing"

	"api/internal/platform/authz"
	"api/internal/platform/editing"
	"api/internal/platform/galgame/editspec"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/perm"
)

// kungalActor mirrors gameActor but files under the kungal tenant (E3a):
// permissions still resolve through the REAL galgame perm bundles — exactly
// what the edit face's per-family resolver does for the kungal BFF.
func kungalActor(uid int64, tier int16, roles ...string) editing.PolicyContext {
	return editing.PolicyContext{
		UserID: uid, Site: editspec.SiteKungal, TrustTier: tier,
		HasPerm: func(key string) bool {
			return perm.Resolver.Can(roles, authz.Permission(key))
		},
	}
}

// TestKungalSiteOverlay pins the E3a ruling-2 posture end to end over the
// real galgame vocabulary: any logged-in user proposes (open), nothing
// automerges on kungal — not even staff — and admin/ren adjudicate via
// edit.galgame.game.review (amend + merge = the double-attribution crown);
// the special keys (vndb_id here) keep their E2a field policies, and the
// galgame_wiki tenant is untouched by the overlay.
func TestKungalSiteOverlay(t *testing.T) {
	e := newGameEngine(t)
	g := createGame(t, nil)

	// 1. A plain logged-in user (no roles, tier 0) proposes — open, no merge.
	prop, rev, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeGame, EntityID: int64(g.ID),
		Patch: map[string]any{editspec.FieldNameZhCN: "用户提案标题"},
		Note:  "fix title",
		Actor: kungalActor(101, 0),
	})
	if err != nil {
		t.Fatalf("plain-user propose: %v", err)
	}
	if rev != nil {
		t.Fatal("kungal proposals must never automerge")
	}
	if prop.Status != editing.StatusOpen {
		t.Fatalf("status = %d, want open", prop.Status)
	}

	// 2. Even staff at trusted tier stays open (automerge=never on kungal).
	adminProp, rev, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeGame, EntityID: int64(g.ID),
		Patch: map[string]any{editspec.FieldIntroZhCN: "管理员改简介"},
		Actor: kungalActor(102, 3, "admin"),
	})
	if err != nil {
		t.Fatalf("admin propose: %v", err)
	}
	if rev != nil {
		t.Fatal("kungal must not automerge even for staff")
	}
	// Clean it up so the merge below rebases against a quiet entity.
	if err := e.DeclineProposal(testCtx, adminProp.ID, kungalActor(102, 3, "admin"), "dup"); err != nil {
		t.Fatalf("decline: %v", err)
	}

	// 3. A moderator cannot adjudicate (review = edit.galgame.game.review,
	//    the OwnerOverride axis: admin/ren only).
	var permErr *editing.PermissionError
	if _, err := e.MergeProposal(testCtx, prop.ID, kungalActor(103, 3, "moderator"), ""); !errors.As(err, &permErr) {
		t.Fatalf("moderator merge: %v, want PermissionError", err)
	}

	// 4. Admin amends (corrects the proposed value) then merges — the crown
	//    mechanism: corrected value lands, double attribution on the revision.
	if _, err := e.AmendProposal(testCtx, prop.ID, editing.AmendInput{
		Set:   map[string]any{editspec.FieldNameZhCN: "修正后标题"},
		Note:  "typo fixed in review",
		Actor: kungalActor(102, 3, "admin"),
	}); err != nil {
		t.Fatalf("amend: %v", err)
	}
	merged, err := e.MergeProposal(testCtx, prop.ID, kungalActor(102, 3, "admin"), "looks good")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if merged.ActorUID != 101 {
		t.Fatalf("revision actor = %d, want proposer 101", merged.ActorUID)
	}
	if merged.AmenderUID == nil || *merged.AmenderUID != 102 {
		t.Fatalf("revision amender = %v, want 102", merged.AmenderUID)
	}
	var after model.Galgame
	if err := testDB.First(&after, g.ID).Error; err != nil {
		t.Fatalf("reload galgame: %v", err)
	}
	if after.NameZhCN != "修正后标题" {
		t.Fatalf("name_zh_cn = %q, want the amended value", after.NameZhCN)
	}

	// 5. Special keys keep the E2a posture on kungal: vndb_id propose is
	//    perm-gated (plain user 403) and automerges at trusted tier for a
	//    holder (moderator+).
	if _, _, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeGame, EntityID: int64(g.ID),
		Patch: map[string]any{editspec.FieldVNDBID: "v123"},
		Actor: kungalActor(101, 0),
	}); !errors.As(err, &permErr) {
		t.Fatalf("plain-user vndb_id propose: %v, want PermissionError", err)
	}
	_, rev, err = e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeGame, EntityID: int64(g.ID),
		Patch: map[string]any{editspec.FieldVNDBID: "v123"},
		Actor: kungalActor(103, 3, "moderator"),
	})
	if err != nil {
		t.Fatalf("moderator vndb_id propose: %v", err)
	}
	if rev == nil {
		t.Fatal("vndb_id must keep the E2a trusted automerge on kungal")
	}

	// 6. The galgame_wiki tenant is untouched: a trusted direct edit still
	//    automerges under the default policy.
	_, rev, err = e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeGame, EntityID: int64(g.ID),
		Patch: map[string]any{editspec.FieldNameZhTW: "舊線直編"},
		Actor: gameActor(42, editing.TrustedTier),
	})
	if err != nil {
		t.Fatalf("old-wire direct edit: %v", err)
	}
	if rev == nil {
		t.Fatal("the galgame_wiki default automerge=trusted must be unaffected by the kungal overlay")
	}
}
