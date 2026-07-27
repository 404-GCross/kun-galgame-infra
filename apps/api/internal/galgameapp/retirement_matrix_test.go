package galgameapp

import (
	"bytes"
	"testing"

	"api/internal/app"
	searchInfra "api/internal/infrastructure/search"
	"api/internal/middleware"
	"api/internal/platform/devapi"
	"api/internal/platform/editing"
	"api/internal/platform/galgame/editquery"
	galgameHandler "api/internal/platform/galgame/handler"
	"api/internal/platform/galgame/model"
	galgameRepo "api/internal/platform/galgame/repository"
	galgameSearch "api/internal/platform/galgame/search"
	galgameService "api/internal/platform/galgame/service"
	siteRepo "api/internal/platform/site/repository"
	"api/pkg/config"
	"api/pkg/oidctoken"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/require"
)

// W5 (09-open-api-phase2 route-B endgame) retirement matrix.
//
// Proves the surgery that split the /internal galgame bridge read face into the
// retired public-data reads (now served by /v1) and the surviving platform
// workflow face (workflowroutes.go):
//   - every RETIRED A/C-bucket path is an UNMATCHED route (Fiber's plain 404),
//   - every SURVIVING workflow route is still registered (non-404, or a handler
//     404 that carries the {code} envelope — never Fiber's routing 404),
//   - the sibling write face (06a) and proposal face (06b) still respond, proving
//     the surgery touched ONLY the read registrations.
//
// The whole matrix runs against one app that mounts the three /internal faces in
// the SAME order as Mount (writes → propose → reads), so the group-Use fence that
// keeps the read chain from blanketing the per-route write/propose chains is
// exercised too. Suite discipline: it seeds no galgame rows (empty migrated
// tables are enough to prove registration) and mints one "w5ret_" client it
// cleans up, so it never touches shared data.

// getRouteAbsent reports whether the GET route is NOT registered. Fiber answers
// an unregistered GET one of two plain-text ways, neither carrying the {code}
// envelope a handler/middleware emits:
//   - 404 "Cannot GET ..."   — the path is gone entirely.
//   - 405 "Method Not Allowed" — the path survives for a write method (the write
//     face's POST / or PUT|PATCH|DELETE /:gid still own it) but its GET read was
//     retired. This is exactly the outcome we want for a retired read whose path
//     overlaps a surviving write route.
//
// A registered GET route always reaches a handler or a devapi/jwt middleware, so
// it returns the {code} envelope or a non-404/405 status — an exact discriminator
// between "read route retired" and "handler returned a semantic 404".
func getRouteAbsent(status int, body []byte) bool {
	return (status == fiber.StatusNotFound || status == fiber.StatusMethodNotAllowed) &&
		!bytes.Contains(body, []byte(`"code"`))
}

// w5Migrate provisions the galgame content + revision tables the surviving
// workflow handlers query. Idempotent; the propose harness already migrated the
// developer-platform + editing tables into testDB.
func w5Migrate(t *testing.T) {
	t.Helper()
	require.NoError(t, testDB.AutoMigrate(
		&model.GalgameSeries{}, &model.GalgameTag{}, &model.GalgameTagAlias{},
		&model.GalgameTagEdge{},
		&model.GalgameOfficial{}, &model.GalgameOfficialAlias{}, &model.GalgameEngine{},
		&model.Galgame{}, &model.GalgameAlias{}, &model.GalgameTagRelation{},
		&model.GalgameOfficialRelation{}, &model.GalgameEngineRelation{},
		&model.GalgameLink{}, &model.GalgameCover{}, &model.GalgameScreenshot{},
		&model.GalgameContributor{}, &model.GalgameVNDBMeta{}, &model.GalgameBangumiMeta{},
		&model.GalgameEGMeta{}, &model.GalgameDlsiteMeta{}, &model.GalgameStats{},
		&model.TaxonomyRevision{}, &model.GalgameMessage{}, &model.GalgamePR{}, &model.GalgameRevision{},
	))
}

// buildW5App wires the three /internal faces over testDB exactly as Mount does
// (writes → propose → reads), and returns the app + the plaintext key.
func buildW5App(t *testing.T) (*fiber.App, string) {
	t.Helper()

	// Repositories + services + handlers for the workflow (read) face.
	repo := galgameRepo.NewGalgameRepository(testDB)
	userRepo := galgameRepo.NewUserReadonlyRepository(testDB)
	messageRepo := galgameRepo.NewMessageRepository(testDB)
	tagRepo := galgameRepo.NewTagRepository(testDB)
	officialRepo := galgameRepo.NewOfficialRepository(testDB)
	engineRepo := galgameRepo.NewEngineRepository(testDB)
	seriesRepo := galgameRepo.NewSeriesRepository(testDB)
	taxRevRepo := galgameRepo.NewTaxonomyRevisionRepository(testDB)

	// Editing engine + native edit-log querier, wired into the services exactly as
	// Mount does — GetUserStats / contributed reads dereference the querier, so a
	// bare service nil-panics.
	reg := editing.NewRegistry()
	registerTestSpec(t, reg)
	engine := editing.NewEngine(testDB, reg)
	editq := editquery.New(testDB)

	svc := galgameService.NewGalgameService(
		repo, galgameRepo.NewRevisionRepository(testDB),
		galgameRepo.NewPRRepository(testDB), userRepo).WithEditing(engine, editq)
	submissionSvc := galgameService.NewSubmissionService(repo, messageRepo).WithEditing(engine)
	messageSvc := galgameService.NewMessageService(messageRepo, repo, userRepo)
	taxSvc := galgameService.NewTaxonomyService(tagRepo, officialRepo, engineRepo, seriesRepo, taxRevRepo, repo)

	// A Meilisearch service so /search's handler is non-nil; the /search probe
	// below 400s on a bad param BEFORE any Meili call, so reachability is moot.
	msClient, err := searchInfra.NewClient(config.MeilisearchConfig{Host: "http://127.0.0.1:7700"})
	require.NoError(t, err)
	searchSvc := galgameSearch.NewService(msClient)

	galgameH := galgameHandler.NewGalgameHandler(svc, nil, nil)
	searchH := galgameHandler.NewSearchHandler(searchSvc)
	submissionH := galgameHandler.NewSubmissionHandler(submissionSvc, nil, nil)
	messageH := galgameHandler.NewMessageHandler(messageSvc)
	taxRevH := galgameHandler.NewTaxonomyRevisionHandler(taxSvc)
	revisionH := galgameHandler.NewRevisionHandler(svc)
	linkH := galgameHandler.NewLinkHandler(svc, repo)
	contributorH := galgameHandler.NewContributorHandler(repo, userRepo)

	jwtAuth := middleware.JWTAuth(oidctoken.NewVerifierWithJWKS(testJWTSecret, ""))
	optionalJWT := middleware.OptionalJWT(oidctoken.NewVerifierWithJWKS(testJWTSecret, ""))

	application := &app.App{Fiber: fiber.New()}
	devRepo := devapi.NewRepository(testDB)
	store := devapi.NewRedisStore(nil) // nil cache → rate-limit/quota fail open
	face := devapiFace{mw: devapi.NewMiddleware(devRepo, store), usageRec: devapi.NewUsageRecorder(devRepo, store)}

	// Propose face reuses the same engine (its synthetic spec is enough).
	proposeH := newProposeHandler(engine, siteRepo.NewOAuthClientRepository(testDB))

	writes := writeRoutes{galgameH: galgameH, linkH: linkH, contributorH: contributorH, submissionH: submissionH}
	workflow := workflowRoutes{
		galgameH: galgameH, searchH: searchH, submissionH: submissionH,
		messageH: messageH, taxRevH: taxRevH, optionalJWT: optionalJWT, jwtAuth: jwtAuth,
	}

	// SAME order as Mount: writes + propose (per-route chains) BEFORE the read
	// group Use, so the read chain never blankets them.
	mountInternalWrites(application, face, writes, jwtAuth)
	mountInternalPropose(application, face, proposeH, jwtAuth)
	mountInternal(application, face, workflow, messageH, revisionH)

	testDB.Exec("DELETE FROM developer_api_keys WHERE client_id = 'w5ret_key'")
	testDB.Exec("DELETE FROM oauth_clients WHERE id = 'w5ret_key'")
	key := mintKey(t, "w5ret_key", "", devapi.TierInternal,
		[]string{devapi.ScopeGalgameRead, devapi.ScopeGalgameWrite, devapi.ScopeGalgamePropose})
	t.Cleanup(func() {
		testDB.Exec("DELETE FROM developer_api_keys WHERE client_id = 'w5ret_key'")
		testDB.Exec("DELETE FROM oauth_clients WHERE id = 'w5ret_key'")
	})
	return application.Fiber, key
}

func TestW5RetirementMatrix(t *testing.T) {
	if testDB == nil {
		t.Skip("no test DB")
	}
	w5Migrate(t)
	f, key := buildW5App(t)

	// ── Retired A/C-bucket reads (29): every one is now an unmatched route ──
	retired := []string{
		// galgame group (14)
		"/internal/galgame",
		"/internal/galgame/batch?ids=1",
		"/internal/galgame/check?vndb_id=v1",
		"/internal/galgame/calendar",
		"/internal/galgame/calendar/pending",
		"/internal/galgame/calendar/tba",
		"/internal/galgame/stats",
		"/internal/galgame/officials/1/galgames",
		"/internal/galgame/tags/1/galgames",
		"/internal/galgame/123",
		"/internal/galgame/123/links",
		"/internal/galgame/123/aliases",
		"/internal/galgame/123/contributors",
		"/internal/galgame/123/scores",
		// tag group (5)
		"/internal/tag",
		"/internal/tag/search",
		"/internal/tag/multi",
		"/internal/tag/somename",
		"/internal/tag/1/galgame-ids",
		// official group (4)
		"/internal/official",
		"/internal/official/search",
		"/internal/official/somename",
		"/internal/official/1/galgame-ids",
		// engine group (3)
		"/internal/engine",
		"/internal/engine/somename",
		"/internal/engine/1/galgame-ids",
		// series group (3)
		"/internal/series",
		"/internal/series/search",
		"/internal/series/1",
	}
	require.Len(t, retired, 29, "the retired census is 29 A/C-bucket routes")
	for _, path := range retired {
		status, body := req(t, f, "GET", path, key, "", "")
		require.Truef(t, getRouteAbsent(status, body),
			"retired path GET %s must be an unregistered read route (Fiber 404/405), got %d: %s", path, status, body)
	}

	// ── Surviving platform-workflow routes (15): all still registered ──
	// The /search probe uses a bad released_from so it 400s BEFORE any Meili call.
	survivors := []string{
		// galgame workflow (7)
		"/internal/galgame/mine",
		"/internal/galgame/messages/mine",
		"/internal/galgame/search?released_from=not-a-date",
		"/internal/galgame/drafts",
		"/internal/galgame/user/1/stats",
		"/internal/galgame/user/1/galgames",
		"/internal/galgame/user/1/contributed",
		// taxonomy revisions (8)
		"/internal/tag/1/revisions",
		"/internal/tag/1/revisions/1",
		"/internal/official/1/revisions",
		"/internal/official/1/revisions/1",
		"/internal/engine/1/revisions",
		"/internal/engine/1/revisions/1",
		"/internal/series/1/revisions",
		"/internal/series/1/revisions/1",
	}
	require.Len(t, survivors, 15, "the surviving census is 15 platform-workflow routes")
	for _, path := range survivors {
		status, body := req(t, f, "GET", path, key, "", "")
		require.Falsef(t, getRouteAbsent(status, body),
			"surviving workflow route GET %s must be registered (not a Fiber 404/405), got %d: %s", path, status, body)
	}

	// The two JWT-personal routes must specifically enforce auth (401), proving the
	// route ran its jwtAuth rather than merely not-404'ing by accident.
	for _, path := range []string{"/internal/galgame/mine", "/internal/galgame/messages/mine"} {
		status, _ := req(t, f, "GET", path, key, "", "")
		require.Equalf(t, fiber.StatusUnauthorized, status, "GET %s must 401 without a Bearer JWT", path)
	}

	// ── Sibling faces untouched: the write (06a) + proposal (06b) faces respond ──
	// Both terminate in jwtAuth, so without a Bearer they 401 — non-404, proving
	// the surgery removed only read registrations.
	spotChecks := []struct{ method, path string }{
		{"POST", "/internal/galgame/submit"}, // write face
		{"PUT", "/internal/galgame/123"},     // write face /:gid
		{"GET", "/internal/edit/proposals"},  // proposal face
		{"POST", "/internal/edit/proposals"}, // proposal face
	}
	for _, sc := range spotChecks {
		status, body := req(t, f, sc.method, sc.path, key, "", "")
		require.Falsef(t, getRouteAbsent(status, body),
			"%s %s (sibling face) must still be registered, got %d: %s", sc.method, sc.path, status, body)
	}
}
