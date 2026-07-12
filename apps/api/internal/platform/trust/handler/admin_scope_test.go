package handler

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"

	siteModel "api/internal/platform/site/model"
	"api/internal/platform/trust/dbtest"
	"api/internal/platform/trust/dto"
	"api/internal/platform/trust/migrate"
	"api/internal/platform/trust/model"
	"api/internal/platform/trust/service"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// testDB is the shared trust database for the DB-backed admin scope tests. It is
// nil when no database is reachable — the spec-export smokes (handler_test.go)
// must still run, so TestMain never exits early; the DB-backed tests t.Skip.
var testDB *gorm.DB

func TestMain(m *testing.M) {
	release := func() {}
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		dsn = "host=localhost port=5432 user=postgres password=postgres dbname=kun_trust_test sslmode=disable"
	}
	if db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}); err == nil {
		sqlDB, _ := db.DB()
		release = dbtest.AcquireSuiteLock(sqlDB)
		if merr := migrate.Run(db); merr != nil {
			fmt.Fprintf(os.Stderr, "SKIP DB tests: trust migration failed: %v\n", merr)
		} else {
			testDB = db
		}
	} else {
		fmt.Fprintf(os.Stderr, "SKIP DB tests: cannot connect to test database: %v\n", err)
	}
	code := m.Run()
	release()
	os.Exit(code)
}

// fakeClients is an in-memory clientSiteLookup for the scope resolver.
type fakeClients struct {
	byID map[string]*siteModel.OAuthClient
}

func (f *fakeClients) FindByClientID(_ context.Context, id string) (*siteModel.OAuthClient, error) {
	if c, ok := f.byID[id]; ok {
		return c, nil
	}
	return nil, nil // not found → resolver fails closed (403)
}

// scopedCtx builds the request context AdminBridge would produce from the JWT
// locals: the caller's GLOBAL roles, its token client id, and the admin user id.
func scopedCtx(globalRoles []string, clientID string, adminID int64) context.Context {
	ctx := context.Background()
	ctx = context.WithValue(ctx, ctxKeyGlobalRoles, globalRoles)
	ctx = context.WithValue(ctx, ctxKeyClientID, clientID)
	ctx = context.WithValue(ctx, ctxKeyAdminID, adminID)
	return ctx
}

func statusOf(err error) int {
	if he, ok := err.(*houseError); ok {
		return he.GetStatus()
	}
	return 0
}

// TestAdminResolveScope pins the two-tier discriminator (章程 04 ruling 1/3):
// global roles that pass trust.queue_access ⇒ unrestricted; otherwise site-scoped
// to the token client's catalog_site; unbound/unknown/missing client ⇒ 403.
func TestAdminResolveScope(t *testing.T) {
	s := &AdminServer{clients: &fakeClients{byID: map[string]*siteModel.OAuthClient{
		"site-client":    {CatalogSite: "otokun"},
		"unbound-client": {}, // authenticated, but no catalog_site binding
	}}}

	// global [moderator] / [admin] alone pass the resolver ⇒ unrestricted.
	for _, roles := range [][]string{{"moderator"}, {"admin"}, {"ren"}} {
		sc, he := s.resolveScope(scopedCtx(roles, "site-client", 1))
		if he != nil || sc.siteScoped() {
			t.Fatalf("global %v must be unrestricted: scope=%+v err=%v", roles, sc, he)
		}
	}

	// global [] + a site-bound client ⇒ scoped to its catalog_site.
	sc, he := s.resolveScope(scopedCtx(nil, "site-client", 1))
	if he != nil || sc.site != "otokun" {
		t.Fatalf("site-only caller must be scoped to otokun: scope=%+v err=%v", sc, he)
	}

	// Fail-closed 403s: unbound client, unknown client, missing client id.
	for _, clientID := range []string{"unbound-client", "ghost", ""} {
		if _, he := s.resolveScope(scopedCtx(nil, clientID, 1)); statusOf(he) != http.StatusForbidden {
			t.Fatalf("client %q must be 403, got %v", clientID, he)
		}
	}
}

// TestAdminRegistriesPlatformOnly pins that the registry + dead-letter surfaces
// reject a site-scoped caller with 403 (章程 04 ruling 2), before any service
// call (so nil services are safe here).
func TestAdminRegistriesPlatformOnly(t *testing.T) {
	s := &AdminServer{clients: &fakeClients{byID: map[string]*siteModel.OAuthClient{
		"site-client": {CatalogSite: "otokun"},
	}}}
	ctx := scopedCtx(nil, "site-client", 1) // site-scoped moderator

	checks := map[string]func() error{
		"listSubjectKinds":  func() error { _, e := s.listSubjectKinds(ctx, &listSubjectKindsAdminInput{}); return e },
		"createSubjectKind": func() error { _, e := s.createSubjectKind(ctx, &createSubjectKindInput{}); return e },
		"patchSubjectKind":  func() error { _, e := s.patchSubjectKind(ctx, &patchSubjectKindInput{}); return e },
		"listReasons":       func() error { _, e := s.listReasons(ctx, &listReasonsInput{}); return e },
		"createReason":      func() error { _, e := s.createReason(ctx, &createReasonInput{}); return e },
		"patchReason":       func() error { _, e := s.patchReason(ctx, &patchReasonInput{}); return e },
		"listDispositions":  func() error { _, e := s.listDispositions(ctx, &listDispositionsInput{}); return e },
		"redeliver":         func() error { _, e := s.redeliverDisposition(ctx, &dispositionIDInput{ID: 1}); return e },
	}
	for name, call := range checks {
		if got := statusOf(call()); got != http.StatusForbidden {
			t.Errorf("%s site-scoped: want 403, got status %d", name, got)
		}
	}
}

// --- DB-backed review-item scoping ---

func truncateReviewTables(t *testing.T) {
	t.Helper()
	for _, tbl := range []string{"trust_disposition", "trust_report", "trust_review_item"} {
		if err := testDB.Exec("TRUNCATE " + tbl + " RESTART IDENTITY CASCADE").Error; err != nil {
			t.Fatalf("truncate %s: %v", tbl, err)
		}
	}
}

func seedItem(t *testing.T, site, subject string) int64 {
	t.Helper()
	it := model.TrustReviewItem{
		Site: site, SubjectKind: "forum_topic", SubjectID: subject,
		Source: model.ReviewSourceReports, Priority: 2, Status: model.ReviewStatusPending,
	}
	if err := testDB.Create(&it).Error; err != nil {
		t.Fatalf("seed item in %s: %v", site, err)
	}
	return it.ID
}

// TestAdminReviewItemsSiteScoped drives the review-item ops end-to-end: a
// site-scoped caller's List is forced to their own site, and Get/Claim/Decide on
// a foreign-site item return 404 (existence not leaked); platform staff are
// unrestricted. (章程 04 ruling 2.)
func TestAdminReviewItemsSiteScoped(t *testing.T) {
	if testDB == nil {
		t.Skip("trust test DB unavailable")
	}
	truncateReviewTables(t)
	ownID := seedItem(t, "kungal", "own")
	foreignID := seedItem(t, "otokun", "foreign")

	s := &AdminServer{
		review:  service.NewReviewService(testDB),
		clients: &fakeClients{byID: map[string]*siteModel.OAuthClient{"kungal-client": {CatalogSite: "kungal"}}},
	}
	scoped := scopedCtx(nil, "kungal-client", 7) // site-scoped to kungal
	staff := scopedCtx([]string{"admin"}, "", 7) // unrestricted

	// List: a site-scoped caller asking for otokun still sees only kungal.
	out, err := s.listReviewItems(scoped, &listReviewItemsInput{Site: "otokun", Status: -1, Source: -1})
	if err != nil {
		t.Fatalf("scoped list: %v", err)
	}
	if len(out.Body.Data.Items) == 0 {
		t.Fatal("scoped list returned nothing for own site")
	}
	for _, it := range out.Body.Data.Items {
		if it.Site != "kungal" {
			t.Fatalf("scoped list leaked site %q", it.Site)
		}
	}

	// Staff sees both sites.
	staffOut, err := s.listReviewItems(staff, &listReviewItemsInput{Status: -1, Source: -1})
	if err != nil {
		t.Fatalf("staff list: %v", err)
	}
	if len(staffOut.Body.Data.Items) < 2 {
		t.Fatalf("staff must see both sites, got %d", len(staffOut.Body.Data.Items))
	}

	// Get: own ok, foreign 404.
	if _, err := s.getReviewItem(scoped, &reviewItemIDInput{ID: ownID}); err != nil {
		t.Fatalf("get own-site scoped: %v", err)
	}
	if got := statusOf(mustErr(s.getReviewItem(scoped, &reviewItemIDInput{ID: foreignID}))); got != http.StatusNotFound {
		t.Fatalf("get foreign-site scoped: want 404, got %d", got)
	}

	// Claim + Decide: foreign 404.
	if got := statusOf(mustErr(s.claimReviewItem(scoped, &reviewItemIDInput{ID: foreignID}))); got != http.StatusNotFound {
		t.Fatalf("claim foreign-site scoped: want 404, got %d", got)
	}
	if got := statusOf(mustErr(s.decideReviewItem(scoped, &decideInput{ID: foreignID, Body: dto.DecideRequest{Decision: "dismissed"}}))); got != http.StatusNotFound {
		t.Fatalf("decide foreign-site scoped: want 404, got %d", got)
	}

	// Own-site claim then decide succeed.
	if _, err := s.claimReviewItem(scoped, &reviewItemIDInput{ID: ownID}); err != nil {
		t.Fatalf("claim own-site scoped: %v", err)
	}
	if _, err := s.decideReviewItem(scoped, &decideInput{ID: ownID, Body: dto.DecideRequest{Decision: "dismissed"}}); err != nil {
		t.Fatalf("decide own-site scoped: %v", err)
	}
}

// mustErr extracts the error from a (result, error) pair, discarding the result,
// so a 404-status assertion reads in one line.
func mustErr[T any](_ T, err error) error { return err }
