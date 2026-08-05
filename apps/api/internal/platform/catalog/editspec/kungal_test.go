package editspec_test

import (
	"errors"
	"testing"

	"api/internal/platform/authz"
	"api/internal/platform/catalog/editspec"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/perm"
	"api/internal/platform/editing"
)

// kungalActor mirrors realActor but files under the kungal tenant: permissions
// still resolve through the REAL catalog perm bundles — exactly what the edit
// face's per-family resolver does for the kungal BFF's asserted actor.
func kungalActor(uid int64, roles ...string) editing.PolicyContext {
	return editing.PolicyContext{
		UserID: uid, Site: "kungal", TrustTier: 0,
		HasPerm: func(key string) bool {
			return perm.Resolver.Can(roles, authz.Permission(key))
		},
	}
}

// TestKungalSiteOverlay pins the kungal posture over the real catalog
// vocabulary — the posture the N5 re-anchoring lost when kungal's edits moved
// from galgame.game to catalog.work: any logged-in user proposes (open); a
// reviewer's own edit direct-merges (automerge=review) — admin/ren via
// edit.catalog.work.review, while a moderator (no review perm) still queues —
// and the default tenant stays perm-gated with no automerge.
func TestKungalSiteOverlay(t *testing.T) {
	e := newEngine(t)
	work := createWork(t, "初期名")

	// 1. A plain logged-in user (no roles) proposes — open, no merge. On the
	//    default tenant the same actor cannot propose at all (pinned by
	//    TestWorkPilotEndToEnd).
	prop, rev, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeWork, EntityID: work.ID,
		Patch: map[string]any{editspec.FieldWorkDisplayName: "用户提案标题"},
		Note:  "fix title",
		Actor: kungalActor(101),
	})
	if err != nil {
		t.Fatalf("plain-user propose: %v", err)
	}
	if rev != nil {
		t.Fatal("a plain user's kungal proposal must never automerge")
	}
	if prop.Status != editing.StatusOpen {
		t.Fatalf("status = %d, want open", prop.Status)
	}

	// 2. Direct edit keys on REVIEW capability (automerge=review): admin holds
	//    edit.catalog.work.review, so their own edit applies immediately.
	_, rev, err = e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeWork, EntityID: work.ID,
		Patch: map[string]any{editspec.FieldWorkOLang: "en"},
		Actor: kungalActor(102, "admin"),
	})
	if err != nil {
		t.Fatalf("admin direct edit: %v", err)
	}
	if rev == nil || rev.Action != editing.ActionDirect {
		t.Fatalf("admin (review perm) must direct-edit on kungal: %+v", rev)
	}
	//    A moderator holds NO work review perm, so their edit still files an
	//    open proposal — automerge keys on the review perm, not on staff
	//    standing.
	modProp, rev, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeWork, EntityID: work.ID,
		Patch: map[string]any{editspec.FieldWorkDisplayName: "モデレーター改題"},
		Actor: kungalActor(103, "moderator"),
	})
	if err != nil {
		t.Fatalf("moderator propose: %v", err)
	}
	if rev != nil {
		t.Fatal("moderator (no review perm) must not automerge on kungal")
	}
	if err := e.DeclineProposal(testCtx, modProp.ID, kungalActor(102, "admin"), "dup"); err != nil {
		t.Fatalf("decline: %v", err)
	}

	// 3. A moderator cannot adjudicate the queue either.
	var permErr *editing.PermissionError
	if _, err := e.MergeProposal(testCtx, prop.ID, kungalActor(103, "moderator"), ""); !errors.As(err, &permErr) {
		t.Fatalf("moderator merge: %v, want PermissionError", err)
	}

	// 4. Admin amends then merges — corrected value lands with double
	//    attribution.
	if _, err := e.AmendProposal(testCtx, prop.ID, editing.AmendInput{
		Set:   map[string]any{editspec.FieldWorkDisplayName: "修正后标题"},
		Note:  "typo fixed in review",
		Actor: kungalActor(102, "admin"),
	}); err != nil {
		t.Fatalf("amend: %v", err)
	}
	merged, err := e.MergeProposal(testCtx, prop.ID, kungalActor(102, "admin"), "looks good")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if merged.ActorUID != 101 {
		t.Fatalf("revision actor = %d, want proposer 101", merged.ActorUID)
	}
	if merged.AmenderUID == nil || *merged.AmenderUID != 102 {
		t.Fatalf("revision amender = %v, want 102", merged.AmenderUID)
	}
	var after model.CatalogWork
	if err := testDB.First(&after, work.ID).Error; err != nil {
		t.Fatalf("reload work: %v", err)
	}
	if after.DisplayName != "修正后标题" {
		t.Fatalf("display_name = %q, want the amended value", after.DisplayName)
	}
}

// TestKungalOwnerReview pins the E3b owner posture on catalog.work: the
// BFF-asserted entry creator (a plain user, no roles) direct-edits their own
// game and adjudicates others' proposals on it — the reported regression was
// exactly this capability going missing — while the same assertion grants
// nothing outside the kungal overlay.
func TestKungalOwnerReview(t *testing.T) {
	e := newEngine(t)
	work := createWork(t, "初期名")

	asOwner := func(pc editing.PolicyContext) editing.PolicyContext {
		pc.IsEntityOwner = true
		return pc
	}
	owner := asOwner(kungalActor(200))

	// 1. The owner's OWN edit direct-merges (automerge=review via OwnerReview):
	//    the creator edits their claimed game without queuing a proposal to
	//    adjudicate against themselves.
	_, rev, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeWork, EntityID: work.ID,
		Patch: map[string]any{editspec.FieldWorkDisplayName: "创建者直接改名"},
		Actor: owner,
	})
	if err != nil {
		t.Fatalf("owner direct edit: %v", err)
	}
	if rev == nil || rev.Action != editing.ActionDirect {
		t.Fatalf("owner must direct-edit their game on kungal: %+v", rev)
	}
	if rev.ActorUID != 200 {
		t.Fatalf("owner direct-edit actor = %d, want the owner 200", rev.ActorUID)
	}

	// 2. Owner (no perms) amends and merges another user's proposal on their
	//    game — double attribution.
	prop, _, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeWork, EntityID: work.ID,
		Patch: map[string]any{editspec.FieldWorkOLang: "en"},
		Actor: kungalActor(101),
	})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	if _, err := e.AmendProposal(testCtx, prop.ID, editing.AmendInput{
		Set:   map[string]any{editspec.FieldWorkOLang: "zh"},
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

	// 3. The same owner assertion grants nothing on the default tenant (no
	//    overlay there): the perm gate still decides.
	var permErr *editing.PermissionError
	if _, _, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: editspec.TypeWork, EntityID: work.ID,
		Patch: map[string]any{editspec.FieldWorkDisplayName: "x"},
		Actor: asOwner(realActor(200, "user")),
	}); !errors.As(err, &permErr) {
		t.Fatalf("owner on default tenant: %v, want PermissionError", err)
	}
}
