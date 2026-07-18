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

// TestKungalSiteOverlay pins the kungal posture end to end over the real
// galgame vocabulary: any logged-in user proposes (open); a reviewer's own
// edit direct-merges (automerge=review) — admin/ren via edit.galgame.game.
// review, while a moderator (staff, no review perm) still queues — and
// admin/ren adjudicate OTHERS' proposals via amend + merge (the double-
// attribution crown); the special keys (vndb_id here) keep their E2a field
// policies, and the galgame_wiki tenant is untouched by the overlay.
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

	// 2. Direct edit keys on REVIEW capability (automerge=review). Admin holds
	//    edit.galgame.game.review, so their own edit applies immediately — a
	//    single-signature direct revision, no proposal to adjudicate.
	_, rev, err = e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeGame, EntityID: int64(g.ID),
		Patch: map[string]any{editspec.FieldIntroZhCN: "管理员直接改简介"},
		Actor: kungalActor(102, 3, "admin"),
	})
	if err != nil {
		t.Fatalf("admin direct edit: %v", err)
	}
	if rev == nil || rev.Action != editing.ActionDirect {
		t.Fatalf("admin (review perm) must direct-edit on kungal: %+v", rev)
	}
	//    A moderator is staff (tier 3) but holds NO review perm on the default
	//    keys, so their edit still files an open proposal — proving automerge
	//    keys on the review perm, not merely on staff standing.
	modProp, rev, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeGame, EntityID: int64(g.ID),
		Patch: map[string]any{editspec.FieldNameJaJP: "モデレーター改題"},
		Actor: kungalActor(103, 3, "moderator"),
	})
	if err != nil {
		t.Fatalf("moderator propose: %v", err)
	}
	if rev != nil {
		t.Fatal("moderator (no review perm) must not automerge on kungal")
	}
	// Clean up the moderator proposal so the merge below rebases against a
	// quiet entity.
	if err := e.DeclineProposal(testCtx, modProp.ID, kungalActor(102, 3, "admin"), "dup"); err != nil {
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

// TestKungalOwnerReview pins the E3b ruling-2 posture over the real galgame
// vocabulary: on kungal, the BFF-asserted entity owner (a plain user, no
// roles) adjudicates the 23 default keys — amend + merge with double
// attribution, the old wire's owner-merge privilege — but never the special
// keys (status/vndb_id keep their field policies), and the galgame_wiki
// tenant grants owners nothing.
func TestKungalOwnerReview(t *testing.T) {
	e := newGameEngine(t)
	g := createGame(t, nil)

	asOwner := func(pc editing.PolicyContext) editing.PolicyContext {
		pc.IsEntityOwner = true
		return pc
	}
	owner := asOwner(kungalActor(200, 0))

	// 1. Owner (no perms) amends and merges another user's default-key
	//    proposal on their own game.
	prop, _, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeGame, EntityID: int64(g.ID),
		Patch: map[string]any{editspec.FieldIntroZhCN: "路人提案简介"},
		Actor: kungalActor(101, 0),
	})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if _, err := e.AmendProposal(testCtx, prop.ID, editing.AmendInput{
		Set:   map[string]any{editspec.FieldIntroZhCN: "创建者修正后的简介"},
		Note:  "owner fix",
		Actor: owner,
	}); err != nil {
		t.Fatalf("owner amend: %v", err)
	}
	merged, err := e.MergeProposal(testCtx, prop.ID, owner, "")
	if err != nil {
		t.Fatalf("owner merge: %v", err)
	}
	if merged.ActorUID != 101 || merged.AmenderUID == nil || *merged.AmenderUID != 200 {
		t.Fatalf("double signature: actor=%d amender=%v", merged.ActorUID, merged.AmenderUID)
	}
	var after model.Galgame
	if err := testDB.First(&after, g.ID).Error; err != nil {
		t.Fatalf("reload galgame: %v", err)
	}
	if after.IntroZhCN != "创建者修正后的简介" {
		t.Fatalf("intro_zh_cn = %q, want the owner-amended value", after.IntroZhCN)
	}

	// 1b. The owner's OWN edit of a default key direct-merges (automerge=review
	//     via OwnerReview): the creator edits their game without queuing a
	//     proposal to adjudicate against themselves — a single-signature direct
	//     revision attributed to the owner.
	_, rev, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeGame, EntityID: int64(g.ID),
		Patch: map[string]any{editspec.FieldNameZhCN: "创建者直接改名"},
		Actor: owner,
	})
	if err != nil {
		t.Fatalf("owner direct edit: %v", err)
	}
	if rev == nil || rev.Action != editing.ActionDirect {
		t.Fatalf("owner must direct-edit a default key on kungal: %+v", rev)
	}
	if rev.ActorUID != 200 {
		t.Fatalf("owner direct-edit actor = %d, want the owner 200", rev.ActorUID)
	}

	// 2. The special keys stay perm-gated: an open status proposal (a
	//    moderator below trusted tier) is beyond the owner.
	statusProp, rev, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeGame, EntityID: int64(g.ID),
		Patch: map[string]any{editspec.FieldStatus: 1},
		Actor: kungalActor(103, 0, "moderator"),
	})
	if err != nil {
		t.Fatalf("status propose: %v", err)
	}
	if rev != nil {
		t.Fatal("tier-0 moderator status proposal must stay open")
	}
	var permErr *editing.PermissionError
	if _, err := e.MergeProposal(testCtx, statusProp.ID, owner, ""); !errors.As(err, &permErr) {
		t.Fatalf("owner on status: %v, want PermissionError", err)
	}

	// 3. The galgame_wiki tenant grants owners nothing (no overlay there).
	wikiProp, _, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeGame, EntityID: int64(g.ID),
		Patch: map[string]any{editspec.FieldNameEnUS: "wiki proposal"},
		Actor: gameActor(104, 0),
	})
	if err != nil {
		t.Fatalf("wiki propose: %v", err)
	}
	if _, err := e.MergeProposal(testCtx, wikiProp.ID, asOwner(gameActor(200, 0)), ""); !errors.As(err, &permErr) {
		t.Fatalf("owner on galgame_wiki: %v, want PermissionError", err)
	}
}
