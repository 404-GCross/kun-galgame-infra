package editing_test

import (
	"errors"
	"testing"

	"api/internal/platform/editing"
)

func TestPolicyEvaluationMatrix(t *testing.T) {
	anon := anonActor(1)
	trusted := trustedActor(2)
	editor := editorActor(3)
	reviewer := reviewerActor(4)

	proposeCases := []struct {
		rule                            string
		anon, trusted, editor, reviewer bool
	}{
		{editing.ProposeOpen, true, true, true, true},
		{editing.ProposeTrusted, false, true, false, false},
		{editing.ProposePerm(permPropose), false, false, true, true},
		{editing.ProposeLocked, false, false, false, false},
	}
	for _, c := range proposeCases {
		p := editing.Policy{Propose: c.rule, Review: editing.ReviewPerm(permReview), Automerge: editing.AutomergeNever}
		for actor, want := range map[*editing.PolicyContext]bool{
			&anon: c.anon, &trusted: c.trusted, &editor: c.editor, &reviewer: c.reviewer,
		} {
			if got := p.AllowsPropose(*actor); got != want {
				t.Errorf("propose %q for uid %d = %v, want %v", c.rule, actor.UserID, got, want)
			}
		}
	}

	automergeCases := []struct {
		rule          string
		anon, trusted bool
	}{
		{editing.AutomergeNever, false, false},
		{editing.AutomergeTrusted, false, true},
		{editing.AutomergeAlways, true, true},
	}
	for _, c := range automergeCases {
		p := editing.Policy{Propose: editing.ProposeOpen, Review: editing.ReviewPerm(permReview), Automerge: c.rule}
		if got := p.AllowsAutomerge(anon); got != c.anon {
			t.Errorf("automerge %q for anon = %v, want %v", c.rule, got, c.anon)
		}
		if got := p.AllowsAutomerge(trusted); got != c.trusted {
			t.Errorf("automerge %q for trusted = %v, want %v", c.rule, got, c.trusted)
		}
	}

	reviewMerge := editing.Policy{Propose: editing.ProposeOpen, Review: editing.ReviewPerm(permReview), Automerge: editing.AutomergeReview}
	if reviewMerge.AllowsAutomerge(anon) || reviewMerge.AllowsAutomerge(trusted) {
		t.Error("automerge=review must reject a non-reviewer")
	}
	if !reviewMerge.AllowsAutomerge(reviewer) {
		t.Error("automerge=review must accept a review-perm holder")
	}
	owner := anonActor(5)
	owner.IsEntityOwner = true
	if reviewMerge.AllowsAutomerge(owner) {
		t.Error("automerge=review WITHOUT OwnerReview must reject a bare owner")
	}
	ownerMerge := editing.Policy{Propose: editing.ProposeOpen, Review: editing.ReviewPerm(permReview), Automerge: editing.AutomergeReview, OwnerReview: true}
	if !ownerMerge.AllowsAutomerge(owner) {
		t.Error("automerge=review + OwnerReview must accept the asserted owner")
	}

	rp := editing.Policy{Propose: editing.ProposeOpen, Review: editing.ReviewPerm(permReview), Automerge: editing.AutomergeNever}
	if rp.AllowsReview(editor) {
		t.Error("review must require the review perm")
	}
	if !rp.AllowsReview(reviewer) {
		t.Error("reviewer must pass the review rule")
	}
	if rp.AllowsReview(editing.PolicyContext{UserID: 9}) {
		t.Error("nil HasPerm must grant nothing")
	}
}

func TestRegistryValidation(t *testing.T) {
	base := func() editing.EntityTypeSpec { return widgetSpec(testDB) }

	cases := []struct {
		name   string
		mutate func(*editing.EntityTypeSpec)
	}{
		{"type not family-prefixed", func(s *editing.EntityTypeSpec) { s.Type = "other.widget" }},
		{"missing closures", func(s *editing.EntityTypeSpec) { s.LoadSnapshot = nil }},
		{"no fields", func(s *editing.EntityTypeSpec) { s.Fields = nil }},
		{"uppercase key", func(s *editing.EntityTypeSpec) { s.Fields[0].Key = "test.widget.Name" }},
		{"key not type-prefixed", func(s *editing.EntityTypeSpec) { s.Fields[0].Key = "test.other.name" }},
		{"duplicate key", func(s *editing.EntityTypeSpec) { s.Fields[1].Key = s.Fields[0].Key }},
		{"missing validate", func(s *editing.EntityTypeSpec) { s.Fields[0].Validate = nil }},
		{"bad propose rule", func(s *editing.EntityTypeSpec) { s.DefaultPolicy.Propose = "sometimes" }},
		{"bare perm propose", func(s *editing.EntityTypeSpec) { s.DefaultPolicy.Propose = "perm:" }},
		{"bad review rule", func(s *editing.EntityTypeSpec) { s.DefaultPolicy.Review = "open" }},
		{"bad automerge rule", func(s *editing.EntityTypeSpec) { s.DefaultPolicy.Automerge = "maybe" }},
		{"overlay on unknown field", func(s *editing.EntityTypeSpec) {
			s.SiteOverlays["x"] = map[string]editing.Policy{"test.widget.ghost": s.DefaultPolicy}
		}},
	}
	for _, c := range cases {
		spec := base()
		c.mutate(&spec)
		if err := editing.NewRegistry().Register(spec); err == nil {
			t.Errorf("%s: registration must fail", c.name)
		}
	}

	reg := editing.NewRegistry()
	if err := reg.Register(base()); err != nil {
		t.Fatalf("valid spec: %v", err)
	}
	if err := reg.Register(base()); err == nil {
		t.Error("duplicate type must fail")
	}
}

func TestSiteOverlay(t *testing.T) {
	e := newEngine(t)
	createWidget(t, 1)

	onOverlaySite := func(pc editing.PolicyContext) editing.PolicyContext {
		pc.Site = overlaySite
		return pc
	}

	var lockedErr *editing.LockedFieldError
	if _, _, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: "test.widget", EntityID: 1,
		Patch: map[string]any{fOpen: "x"}, Actor: onOverlaySite(anonActor(300)),
	}); !errors.As(err, &lockedErr) {
		t.Fatalf("overlay-locked field: %v", err)
	}

	_, rev, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: "test.widget", EntityID: 1,
		Patch: map[string]any{fName: "overlay direct"}, Actor: onOverlaySite(anonActor(300)),
	})
	if err != nil {
		t.Fatalf("overlay-open field: %v", err)
	}
	if rev == nil || rev.Action != editing.ActionDirect {
		t.Fatal("overlay must grant open+always on name")
	}
	if w := loadWidget(t, 1); w.Name != "overlay direct" {
		t.Fatalf("apply: %+v", w)
	}

	var permErr *editing.PermissionError
	if _, _, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
		EntityType: "test.widget", EntityID: 1,
		Patch: map[string]any{fName: "y"}, Actor: anonActor(300),
	}); !errors.As(err, &permErr) {
		t.Fatalf("default site must still perm-gate name: %v", err)
	}
}

func TestSchemaProjection(t *testing.T) {
	e := newEngine(t)

	find := func(projs []editing.FieldProjection, key string) editing.FieldProjection {
		t.Helper()
		for _, p := range projs {
			if p.Key == key {
				return p
			}
		}
		t.Fatalf("projection missing %s", key)
		return editing.FieldProjection{}
	}

	anonProj, err := e.SchemaProjection(testCtx, "test.widget", 0, anonActor(1))
	if err != nil {
		t.Fatal(err)
	}
	if p := find(anonProj, fName); p.CanPropose || p.CanReview || p.Locked {
		t.Errorf("anon on name: %+v", p)
	}
	if p := find(anonProj, fOpen); !p.CanPropose || !p.WouldAutomerge {
		t.Errorf("anon on open_note: %+v", p)
	}
	if p := find(anonProj, fTrusted); !p.CanPropose || p.WouldAutomerge {
		t.Errorf("anon on trusted_note: %+v", p)
	}
	if p := find(anonProj, fLocked); !p.Locked || p.CanPropose {
		t.Errorf("anon on locked_code: %+v", p)
	}
	if p := find(anonProj, fLegacy); !p.Deprecated || p.CanPropose {
		t.Errorf("anon on legacy: %+v", p)
	}

	revProj, err := e.SchemaProjection(testCtx, "test.widget", 0, reviewerActor(2))
	if err != nil {
		t.Fatal(err)
	}
	if p := find(revProj, fName); !p.CanPropose || !p.CanReview || p.WouldAutomerge {
		t.Errorf("reviewer on name: %+v", p)
	}

	trustedProj, err := e.SchemaProjection(testCtx, "test.widget", 0, trustedActor(3))
	if err != nil {
		t.Fatal(err)
	}
	if p := find(trustedProj, fTrusted); !p.WouldAutomerge {
		t.Errorf("trusted on trusted_note: %+v", p)
	}

	overlayActor := anonActor(4)
	overlayActor.Site = overlaySite
	overlayProj, err := e.SchemaProjection(testCtx, "test.widget", 0, overlayActor)
	if err != nil {
		t.Fatal(err)
	}
	if p := find(overlayProj, fOpen); !p.Locked {
		t.Errorf("overlay on open_note: %+v", p)
	}
	if p := find(overlayProj, fName); !p.CanPropose || !p.WouldAutomerge {
		t.Errorf("overlay on name: %+v", p)
	}

	if _, err := e.SchemaProjection(testCtx, "test.ghost", 0, anonActor(1)); !errors.Is(err, editing.ErrUnknownEntityType) {
		t.Fatalf("unknown type: %v", err)
	}
}

func TestListAndDiff(t *testing.T) {
	e := newEngine(t)
	createWidget(t, 1)

	for i, note := range []string{"one", "two"} {
		if _, _, err := e.CreateProposal(testCtx, editing.CreateProposalInput{
			EntityType: "test.widget", EntityID: 1,
			Patch: map[string]any{fOpen: note}, Actor: anonActor(int64(300 + i)),
		}); err != nil {
			t.Fatal(err)
		}
	}

	revs, err := e.ListRevisions(testCtx, "test.widget", 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(revs) != 2 || revs[0].Seq != 2 || revs[1].Seq != 1 {
		t.Fatalf("revisions newest-first: %+v", revs)
	}

	diffs, err := e.Diff(testCtx, "test.widget", 1, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs) != 1 {
		t.Fatalf("diffs: %+v", diffs)
	}
	d := diffs[0]
	if d.Key != fOpen || d.From != "one" || d.To != "two" || d.DiffHint != editing.DiffHintLines {
		t.Fatalf("diff: %+v", d)
	}

	diffs, err = e.Diff(testCtx, "test.widget", 1, 2, 2)
	if err != nil || len(diffs) != 0 {
		t.Fatalf("self diff: %v %v", diffs, err)
	}
	if _, err := e.Diff(testCtx, "test.widget", 1, 1, 42); !errors.Is(err, editing.ErrRevisionNotFound) {
		t.Fatalf("missing revision: %v", err)
	}

	props, err := e.ListProposals(testCtx, editing.ProposalFilter{
		EntityType: "test.widget", EntityID: 1, Status: editing.StatusMerged,
	})
	if err != nil || len(props) != 2 {
		t.Fatalf("merged proposals: %d err=%v", len(props), err)
	}
	props, err = e.ListProposals(testCtx, editing.ProposalFilter{Status: editing.StatusOpen})
	if err != nil || len(props) != 0 {
		t.Fatalf("open proposals: %d err=%v", len(props), err)
	}
	props, err = e.ListProposals(testCtx, editing.ProposalFilter{Site: "test-site", Status: -1})
	if err != nil || len(props) != 2 {
		t.Fatalf("site proposals: %d err=%v", len(props), err)
	}
}
