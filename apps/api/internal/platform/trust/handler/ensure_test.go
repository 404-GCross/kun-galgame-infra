package handler

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	siteModel "api/internal/platform/site/model"
	"api/internal/platform/trust/dto"
	"api/internal/platform/trust/service"
)

// s2sCtx builds the S2S request context S2SBridge would produce: an
// authenticated client bound to `site` (its catalog_site). A blank site models
// an unbound client (siteBinding then 403s).
func s2sCtx(site string) context.Context {
	return context.WithValue(context.Background(), ctxKeyClient, &siteModel.OAuthClient{CatalogSite: site})
}

// overCapItems is maxEnsureKinds+1 distinct declared kinds (trips the 422 cap).
func overCapItems() []dto.EnsureSubjectKindItem {
	items := make([]dto.EnsureSubjectKindItem, maxEnsureKinds+1)
	for i := range items {
		items[i] = dto.EnsureSubjectKindItem{Key: fmt.Sprintf("k%d", i)}
	}
	return items
}

// TestEnsureSubjectKindsGuards pins the S2S ensure guards that fire before any DB
// access (step 06 契约裁决 1): unbound client → 403, over-cap → 422, empty → 200
// with an empty result. None of these touch the registry, so a nil DB is safe.
func TestEnsureSubjectKindsGuards(t *testing.T) {
	s := &Server{registry: service.NewRegistryService(testDB)}

	// Unbound client → 403 (site is derived strictly from the binding).
	if got := statusOf(mustErr(s.ensureSubjectKinds(context.Background(), &ensureSubjectKindsInput{}))); got != http.StatusForbidden {
		t.Fatalf("unbound ensure: want 403, got %d", got)
	}

	// Over the 50-kind cap → 422.
	over := &ensureSubjectKindsInput{Body: dto.EnsureSubjectKindsRequest{Kinds: overCapItems()}}
	if got := statusOf(mustErr(s.ensureSubjectKinds(s2sCtx("site-a"), over))); got != http.StatusUnprocessableEntity {
		t.Fatalf("over-cap ensure: want 422, got %d", got)
	}

	// Empty declaration → 200 with an empty result (idempotent no-op).
	out, err := s.ensureSubjectKinds(s2sCtx("site-a"), &ensureSubjectKindsInput{})
	if err != nil {
		t.Fatalf("empty ensure: %v", err)
	}
	if len(out.Body.Data.Results) != 0 {
		t.Fatalf("empty ensure should return no results, got %d", len(out.Body.Data.Results))
	}
}

// TestEnsureSubjectKindsHandler drives the S2S ensure end-to-end against the DB:
// the site is taken from the client binding, results come back in request order,
// a second run is fully idempotent, and the create is audited with a nil (system)
// actor — the S2S contrast to the admin batch.
func TestEnsureSubjectKindsHandler(t *testing.T) {
	if testDB == nil {
		t.Skip("trust test DB unavailable")
	}
	truncateRegistry(t)
	s := &Server{registry: service.NewRegistryService(testDB)}
	ctx := s2sCtx("site-h")

	cb := "https://h.example/cb"
	in := &ensureSubjectKindsInput{Body: dto.EnsureSubjectKindsRequest{Kinds: []dto.EnsureSubjectKindItem{
		{Key: "topic"},
		{Key: "reply", CallbackURL: &cb},
		{Key: "resource"},
	}}}
	out, err := s.ensureSubjectKinds(ctx, in)
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	got := out.Body.Data.Results
	if len(got) != 3 || got[0].Key != "topic" || got[1].Key != "reply" || got[2].Key != "resource" {
		t.Fatalf("results out of request order: %+v", got)
	}
	for i, r := range got {
		if r.Result != "created" {
			t.Fatalf("result[%d] = %q, want created", i, r.Result)
		}
	}
	// The kind landed under the bound site, never a wire-supplied one.
	var site string
	testDB.Raw(`SELECT site FROM trust_subject_kind WHERE key = 'reply'`).Scan(&site)
	if site != "site-h" {
		t.Fatalf("kind landed under %q, want the bound site-h", site)
	}
	// S2S create records a nil (system) audit actor.
	var actor *int64
	testDB.Raw(`SELECT actor_id FROM trust_audit_log WHERE action='subject_kind_created' ORDER BY id DESC LIMIT 1`).Scan(&actor)
	if actor != nil {
		t.Fatalf("S2S ensure should audit a nil actor, got %d", *actor)
	}

	// Second run: identical declaration → all unchanged (idempotent).
	out2, err := s.ensureSubjectKinds(ctx, in)
	if err != nil {
		t.Fatalf("ensure round 2: %v", err)
	}
	for i, r := range out2.Body.Data.Results {
		if r.Result != "unchanged" {
			t.Fatalf("round2 result[%d] = %q, want unchanged", i, r.Result)
		}
	}
}

// TestBatchSubjectKindsPermissionGolden pins the admin batch gate (step 06 契约裁决
// 3): a site-scoped moderator is rejected 403 exactly like the other registry
// surfaces; a missing site and an over-cap list are 422 — all before any DB use.
func TestBatchSubjectKindsPermissionGolden(t *testing.T) {
	s := &AdminServer{
		registry: service.NewRegistryService(testDB),
		clients: &fakeClients{byID: map[string]*siteModel.OAuthClient{
			"site-client": {CatalogSite: "otokun"},
		}},
	}
	scoped := scopedCtx(nil, "site-client", 1)    // site-scoped moderator
	staff := scopedCtx([]string{"admin"}, "", 42) // platform staff

	// Site-scoped moderator → 403 (registries are platform-ops).
	scopedIn := &batchSubjectKindsInput{Body: dto.BatchSubjectKindsRequest{Site: "otokun", Kinds: []dto.EnsureSubjectKindItem{{Key: "x"}}}}
	if got := statusOf(mustErr(s.batchSubjectKinds(scoped, scopedIn))); got != http.StatusForbidden {
		t.Fatalf("site-scoped batch: want 403, got %d", got)
	}

	// Platform staff, but no site → 422.
	if got := statusOf(mustErr(s.batchSubjectKinds(staff, &batchSubjectKindsInput{Body: dto.BatchSubjectKindsRequest{Site: ""}}))); got != http.StatusUnprocessableEntity {
		t.Fatalf("missing-site batch: want 422, got %d", got)
	}

	// Platform staff, over the cap → 422.
	overIn := &batchSubjectKindsInput{Body: dto.BatchSubjectKindsRequest{Site: "otokun", Kinds: overCapItems()}}
	if got := statusOf(mustErr(s.batchSubjectKinds(staff, overIn))); got != http.StatusUnprocessableEntity {
		t.Fatalf("over-cap batch: want 422, got %d", got)
	}
}

// TestBatchSubjectKindsConvergence drives the admin batch against the DB: the
// explicit `site` is honored, the same convergence outcomes are produced, the
// operator is recorded as the audit actor, and a second run is idempotent.
func TestBatchSubjectKindsConvergence(t *testing.T) {
	if testDB == nil {
		t.Skip("trust test DB unavailable")
	}
	truncateRegistry(t)
	s := &AdminServer{registry: service.NewRegistryService(testDB), clients: &fakeClients{}}
	staff := scopedCtx([]string{"admin"}, "", 42)

	cb := "https://x.example/cb"
	in := &batchSubjectKindsInput{Body: dto.BatchSubjectKindsRequest{Site: "explicit-site", Kinds: []dto.EnsureSubjectKindItem{
		{Key: "k1"},
		{Key: "k2", CallbackURL: &cb},
	}}}
	out, err := s.batchSubjectKinds(staff, in)
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	res := out.Body.Data.Results
	if len(res) != 2 || res[0].Result != "created" || res[1].Result != "created" {
		t.Fatalf("batch convergence: %+v", res)
	}

	// The explicit site was honored (not a binding — an admin has none here).
	var n int64
	testDB.Model(&struct{}{}).Table("trust_subject_kind").Where("site = ?", "explicit-site").Count(&n)
	if n != 2 {
		t.Fatalf("explicit-site kinds = %d, want 2", n)
	}
	// The admin batch records the OPERATOR as the audit actor.
	var actor *int64
	testDB.Raw(`SELECT actor_id FROM trust_audit_log WHERE action='subject_kind_created' ORDER BY id DESC LIMIT 1`).Scan(&actor)
	if actor == nil || *actor != 42 {
		t.Fatalf("admin batch must audit the operator (42), got %v", actor)
	}

	// Idempotent second run.
	out2, _ := s.batchSubjectKinds(staff, in)
	for i, r := range out2.Body.Data.Results {
		if r.Result != "unchanged" {
			t.Fatalf("round2 result[%d] = %q, want unchanged", i, r.Result)
		}
	}
}

// truncateRegistry clears the subject-kind registry and the audit chain between
// DB-backed ensure/batch tests.
func truncateRegistry(t *testing.T) {
	t.Helper()
	if err := testDB.Exec("TRUNCATE trust_subject_kind, trust_audit_log RESTART IDENTITY CASCADE").Error; err != nil {
		t.Fatalf("truncate registry: %v", err)
	}
}
