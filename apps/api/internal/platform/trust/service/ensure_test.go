package service

import (
	"context"
	"testing"

	"api/internal/platform/trust/model"
)

// getKind reloads a subject-kind row by its tenant identity (site, key).
func getKind(t *testing.T, site, key string) *model.TrustSubjectKind {
	t.Helper()
	var k model.TrustSubjectKind
	if err := testDB.Where("site = ? AND key = ?", site, key).Take(&k).Error; err != nil {
		t.Fatalf("reload kind %s/%s: %v", site, key, err)
	}
	return &k
}

// outcomes flattens results into a comparable slice.
func outcomes(rs []EnsureSubjectKindResult) []EnsureOutcome {
	out := make([]EnsureOutcome, len(rs))
	for i, r := range rs {
		out[i] = r.Outcome
	}
	return out
}

func assertOutcomes(t *testing.T, got []EnsureSubjectKindResult, want ...EnsureOutcome) {
	t.Helper()
	g := outcomes(got)
	if len(g) != len(want) {
		t.Fatalf("outcome count = %d, want %d (%v)", len(g), len(want), g)
	}
	for i := range want {
		if g[i] != want[i] {
			t.Fatalf("outcome[%d] = %q, want %q (full: %v)", i, g[i], want[i], g)
		}
		if got[i].Key == "" {
			t.Fatalf("result[%d] has empty key", i)
		}
	}
}

// TestEnsureSubjectKindsConvergence pins the whole per-kind convergence matrix
// (step 06 契约裁决 2): create / per-field update / unchanged / deprecated_skipped,
// a fully-idempotent second run, request-order results, and — critically — that a
// declaration never revives a deprecated kind.
func TestEnsureSubjectKindsConvergence(t *testing.T) {
	if testDB == nil {
		t.Skip("TEST_DATABASE_DSN not set")
	}
	cleanTables(t)
	svc := NewRegistryService(testDB)
	ctx := context.Background()
	site := "site-a"

	cb1, cb2 := "https://a.example/cb", "https://a.example/cb2"
	sec1, sec2 := "secret-1", "secret-2"
	yes, no := true, false

	// Round 1 — create: one bare kind, one fully specified. In request order.
	decl := []EnsureSubjectKindItem{
		{Key: "forum_topic"},
		{Key: "forum_reply", CallbackURL: &cb1, CallbackSecret: &sec1, NotifyOnDismiss: &yes},
	}
	res, err := svc.EnsureSubjectKinds(ctx, nil, site, decl)
	if err != nil {
		t.Fatalf("round1 ensure: %v", err)
	}
	assertOutcomes(t, res, EnsureCreated, EnsureCreated)
	if res[0].Key != "forum_topic" || res[1].Key != "forum_reply" {
		t.Fatalf("round1 results out of request order: %+v", res)
	}
	// Create defaults: bare kind has no callback and notify=false.
	if k := getKind(t, site, "forum_topic"); k.CallbackURL != nil || k.NotifyOnDismiss {
		t.Fatalf("bare create should default: %+v", k)
	}
	if k := getKind(t, site, "forum_reply"); derefStr(k.CallbackURL) != cb1 || derefStr(k.CallbackSecret) != sec1 || !k.NotifyOnDismiss {
		t.Fatalf("full create not stored: %+v", k)
	}

	// Round 2 — idempotent: the identical declaration is now all unchanged.
	res, err = svc.EnsureSubjectKinds(ctx, nil, site, decl)
	if err != nil {
		t.Fatalf("round2 ensure: %v", err)
	}
	assertOutcomes(t, res, EnsureUnchanged, EnsureUnchanged)

	// Round 3 — per-field update (callback_url only, sparse): the other fields are
	// omitted and must stay put.
	res, _ = svc.EnsureSubjectKinds(ctx, nil, site, []EnsureSubjectKindItem{{Key: "forum_reply", CallbackURL: &cb2}})
	assertOutcomes(t, res, EnsureUpdated)
	if k := getKind(t, site, "forum_reply"); derefStr(k.CallbackURL) != cb2 || derefStr(k.CallbackSecret) != sec1 || !k.NotifyOnDismiss {
		t.Fatalf("url-only update clobbered siblings: %+v", k)
	}

	// Round 4 — per-field update (notify_on_dismiss only).
	res, _ = svc.EnsureSubjectKinds(ctx, nil, site, []EnsureSubjectKindItem{{Key: "forum_reply", NotifyOnDismiss: &no}})
	assertOutcomes(t, res, EnsureUpdated)
	if k := getKind(t, site, "forum_reply"); k.NotifyOnDismiss || derefStr(k.CallbackURL) != cb2 {
		t.Fatalf("notify-only update clobbered siblings: %+v", k)
	}

	// Round 5 — per-field update (callback_secret only).
	res, _ = svc.EnsureSubjectKinds(ctx, nil, site, []EnsureSubjectKindItem{{Key: "forum_reply", CallbackSecret: &sec2}})
	assertOutcomes(t, res, EnsureUpdated)
	if k := getKind(t, site, "forum_reply"); derefStr(k.CallbackSecret) != sec2 {
		t.Fatalf("secret-only update not applied: %+v", k)
	}

	// Round 6 — sparse unchanged: a provided field that already matches → no-op.
	res, _ = svc.EnsureSubjectKinds(ctx, nil, site, []EnsureSubjectKindItem{{Key: "forum_reply", CallbackURL: &cb2}})
	assertOutcomes(t, res, EnsureUnchanged)

	// Round 7 — deprecated_skipped: retire forum_topic, then re-declare it with a
	// callback. It must NOT revive and must NOT take the new config.
	dep := getKind(t, site, "forum_topic")
	deprecated := true
	if _, err := svc.PatchSubjectKind(ctx, 1, dep.ID, SubjectKindPatch{IsDeprecated: &deprecated}); err != nil {
		t.Fatalf("deprecate forum_topic: %v", err)
	}
	res, _ = svc.EnsureSubjectKinds(ctx, nil, site, []EnsureSubjectKindItem{{Key: "forum_topic", CallbackURL: &cb1, NotifyOnDismiss: &yes}})
	assertOutcomes(t, res, EnsureDeprecatedSkipped)
	if k := getKind(t, site, "forum_topic"); !k.IsDeprecated || k.CallbackURL != nil || k.NotifyOnDismiss {
		t.Fatalf("deprecated kind was revived/mutated: %+v", k)
	}

	// Round 8 — mixed batch in request order: deprecated / unchanged / created.
	res, _ = svc.EnsureSubjectKinds(ctx, nil, site, []EnsureSubjectKindItem{
		{Key: "forum_topic"},
		{Key: "forum_reply", CallbackURL: &cb2},
		{Key: "resource_post"},
	})
	assertOutcomes(t, res, EnsureDeprecatedSkipped, EnsureUnchanged, EnsureCreated)
	if res[0].Key != "forum_topic" || res[2].Key != "resource_post" {
		t.Fatalf("mixed batch out of order: %+v", res)
	}

	// Empty declaration → empty result (no error).
	empty, err := svc.EnsureSubjectKinds(ctx, nil, site, nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty ensure: results=%v err=%v", empty, err)
	}
}

// TestEnsureSubjectKindsSelfSiteIsolation pins step 06 契约裁决 1/2: a kind is
// always created under the caller's site; the same key in another site is a
// distinct row, and one site's ensure never sees or mutates another site's rows.
func TestEnsureSubjectKindsSelfSiteIsolation(t *testing.T) {
	if testDB == nil {
		t.Skip("TEST_DATABASE_DSN not set")
	}
	cleanTables(t)
	svc := NewRegistryService(testDB)
	ctx := context.Background()

	cbA, cbB := "https://a.example/cb", "https://b.example/cb"

	// site-a declares shared_kind with its own callback.
	res, err := svc.EnsureSubjectKinds(ctx, nil, "site-a", []EnsureSubjectKindItem{{Key: "shared_kind", CallbackURL: &cbA}})
	if err != nil {
		t.Fatalf("site-a ensure: %v", err)
	}
	assertOutcomes(t, res, EnsureCreated)

	// site-b declares the SAME key: a separate row is created (not "unchanged"),
	// carrying site-b's callback — proving keys are tenant-scoped.
	res, err = svc.EnsureSubjectKinds(ctx, nil, "site-b", []EnsureSubjectKindItem{{Key: "shared_kind", CallbackURL: &cbB}})
	if err != nil {
		t.Fatalf("site-b ensure: %v", err)
	}
	assertOutcomes(t, res, EnsureCreated)

	if ka := getKind(t, "site-a", "shared_kind"); derefStr(ka.CallbackURL) != cbA {
		t.Fatalf("site-a callback drifted: %+v", ka)
	}
	if kb := getKind(t, "site-b", "shared_kind"); derefStr(kb.CallbackURL) != cbB {
		t.Fatalf("site-b callback drifted: %+v", kb)
	}

	// Exactly one row per site for the shared key.
	var n int64
	if err := testDB.Model(&model.TrustSubjectKind{}).Where("key = ?", "shared_kind").Count(&n).Error; err != nil {
		t.Fatalf("count shared_kind: %v", err)
	}
	if n != 2 {
		t.Fatalf("shared_kind rows = %d, want 2 (one per site)", n)
	}

	// site-a re-ensures: its own row is unchanged; site-b's is untouched.
	res, _ = svc.EnsureSubjectKinds(ctx, nil, "site-a", []EnsureSubjectKindItem{{Key: "shared_kind", CallbackURL: &cbA}})
	assertOutcomes(t, res, EnsureUnchanged)
	if kb := getKind(t, "site-b", "shared_kind"); derefStr(kb.CallbackURL) != cbB {
		t.Fatalf("site-a ensure leaked into site-b: %+v", kb)
	}
}
