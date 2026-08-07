// public_moderation_test.go — the works-list review-queue gate (wave 186a).
//
// Every row here is a different way of not being this tenant's moderator, and
// none of them may receive a queue page. The one thing the suite refuses to
// tolerate is a QUIET refusal: a 200 carrying the live set instead of the queue
// would read, to the moderator on the other end, as "there is nothing to
// review".
package handler

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"api/internal/middleware"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/repository"
	"api/internal/platform/catalog/service"
	siteModel "api/internal/platform/site/model"
	"api/pkg/oidctoken"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// queueApp mounts GET /v1/catalog/works exactly as cmd/catalog does for the
// dual-credential transport: OptionalJWT in front (never blocking), the client
// registry installed on the handler. The devapi key chain is a separate concern
// and is deliberately absent — it would only add 401s to what these cases
// assert.
func queueApp(db *gorm.DB, clients fakeClientLookup) *fiber.App {
	resolveSvc := service.NewResolveService(repository.NewRedirectRepository(db))
	publicSvc := service.NewPublicService(db, service.NewReadService(db), resolveSvc, "")
	h := NewPublicHandler(publicSvc, resolveSvc, nil, nil).WithModeration(clients)
	app := fiber.New()
	app.Get("/v1/catalog/works",
		middleware.OptionalJWT(oidctoken.NewVerifier(userTestSecret, nil)), h.WorksList)
	return app
}

// queueGet issues a works-list call with an optional end-user token in the
// Authorization slot — the second credential.
func queueGet(t *testing.T, app *fiber.App, url, token string) (int, []int64) {
	t.Helper()
	req := httptest.NewRequest("GET", url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := app.Test(req)
	require.NoError(t, err)
	var env struct {
		Data struct {
			Items []struct {
				ID int64 `json:"id"`
			} `json:"items"`
		} `json:"data"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&env)
	ids := make([]int64, 0, len(env.Data.Items))
	for _, it := range env.Data.Items {
		ids = append(ids, it.ID)
	}
	return resp.StatusCode, ids
}

type queueFixture struct {
	minePending  int64
	theirPending int64
	mineLive     int64
}

func seedQueueWorks(t *testing.T, db *gorm.DB) queueFixture {
	t.Helper()
	for _, tbl := range []string{
		"catalog_credit", "catalog_work_character", "catalog_work_label", "catalog_external_ref",
		"catalog_work_title", "catalog_release", "catalog_work",
	} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	mk := func(name, site string, productWorkID int64, state int16) int64 {
		w := model.CatalogWork{
			MediumID: 1, OLang: "ja", DisplayName: name,
			ContentRating: model.ContentRatingAllAges, Status: model.WorkStatusLive,
			Site: &site, ProductWorkID: &productWorkID, ClaimState: &state,
		}
		require.NoError(t, db.Create(&w).Error)
		return w.ID
	}
	return queueFixture{
		minePending:  mk("うちの投稿", "kungal", 96001, model.ClaimStatePending),
		theirPending: mk("よその投稿", "moyu", 96002, model.ClaimStatePending),
		mineLive:     mk("うちの公開済み", "kungal", 96003, model.ClaimStateLive),
	}
}

func queueClients() fakeClientLookup {
	thirdPartyOwner := uint(77)
	return fakeClientLookup{
		"kungal-client": {ID: "kungal-client", CatalogSite: "kungal"},
		"moyu-client":   {ID: "moyu-client", CatalogSite: "moyu"},
		"no-site":       {ID: "no-site"},
		"third-party":   {ID: "third-party", CatalogSite: "kungal", OwnerUserID: &thirdPartyOwner},
	}
}

// TestWorksListStatusDefaultUnchanged: absent and live are the same query, and
// neither reads the Authorization header at all — the pre-186 wire, byte for
// byte, with or without a token present.
func TestWorksListStatusDefaultUnchanged(t *testing.T) {
	db := openCatalogTestDB(t)
	f := seedQueueWorks(t, db)
	app := queueApp(db, queueClients())
	moderator := userTokenRoles(t, 700, ScopeCatalogEdit, "kungal-client", "user", "moderator")

	for _, url := range []string{"/v1/catalog/works", "/v1/catalog/works?status=live"} {
		for _, tok := range []string{"", moderator} {
			status, ids := queueGet(t, app, url, tok)
			require.Equalf(t, fiber.StatusOK, status, "%s must stay a plain 200", url)
			assert.ElementsMatchf(t, []int64{f.minePending, f.theirPending, f.mineLive}, ids,
				"%s must return the whole LIVE set regardless of any token", url)
		}
	}
	// A token in the LEGACY single-credential transport (the API key itself in
	// the Bearer slot) must not disturb the lane: OptionalJWT cannot parse it
	// and falls through instead of 401ing.
	status, ids := queueGet(t, app, "/v1/catalog/works", "nm_live_notajwtatall")
	assert.Equal(t, fiber.StatusOK, status)
	assert.Len(t, ids, 3)
}

// TestWorksListPendingRefusals walks the gate from the outside in. Each row is
// a different missing half of "this person, through this product, may review".
func TestWorksListPendingRefusals(t *testing.T) {
	db := openCatalogTestDB(t)
	seedQueueWorks(t, db)
	app := queueApp(db, queueClients())

	for _, tc := range []struct {
		name  string
		token string
		want  int
	}{
		{"no second credential at all", "", fiber.StatusForbidden},
		{"an unparseable Authorization value", "nm_live_notajwtatall", fiber.StatusForbidden},
		{"a token bound to no client", userTokenRoles(t, 701, ScopeCatalogEdit, "", "user", "moderator"), fiber.StatusForbidden},
		{"a token whose client is not registered", userTokenRoles(t, 702, ScopeCatalogEdit, "nobody-client", "user", "moderator"), fiber.StatusForbidden},
		{"a client bound to no catalog site", userTokenRoles(t, 703, ScopeCatalogEdit, "no-site", "user", "moderator"), fiber.StatusForbidden},
		{"a THIRD-PARTY application, moderator behind it or not", userTokenRoles(t, 704, ScopeCatalogEdit, "third-party", "user", "moderator"), fiber.StatusForbidden},
		{"a person without catalog.claim.review", userTokenRoles(t, 705, ScopeCatalogEdit, "kungal-client", "user", "creator"), fiber.StatusForbidden},
	} {
		status, ids := queueGet(t, app, "/v1/catalog/works?status=pending", tc.token)
		assert.Equalf(t, tc.want, status, "%s must be refused", tc.name)
		assert.Emptyf(t, ids, "%s must receive no rows at all — a downgraded live page reads as an empty queue", tc.name)
	}
}

// TestWorksListPendingServesThePinnedTenant: the happy path, plus the pin. The
// moderator sees their OWN tenant's submissions and nobody else's, and asking
// for someone else's queue is refused rather than silently re-pointed.
func TestWorksListPendingServesThePinnedTenant(t *testing.T) {
	db := openCatalogTestDB(t)
	f := seedQueueWorks(t, db)
	app := queueApp(db, queueClients())
	kungalMod := userTokenRoles(t, 710, ScopeCatalogEdit, "kungal-client", "user", "moderator")
	moyuMod := userTokenRoles(t, 711, ScopeCatalogEdit, "moyu-client", "user", "admin")

	status, ids := queueGet(t, app, "/v1/catalog/works?status=pending", kungalMod)
	require.Equal(t, fiber.StatusOK, status)
	assert.Equal(t, []int64{f.minePending}, ids,
		"the queue is this tenant's pending claims only — not another tenant's, not its own live ones")

	// Each tenant's moderator sees their own queue through the same URL.
	status, ids = queueGet(t, app, "/v1/catalog/works?status=pending", moyuMod)
	require.Equal(t, fiber.StatusOK, status)
	assert.Equal(t, []int64{f.theirPending}, ids)

	// Naming one's own site is redundant but legal; naming another's is a 403.
	status, ids = queueGet(t, app, "/v1/catalog/works?status=pending&site=kungal", kungalMod)
	require.Equal(t, fiber.StatusOK, status)
	assert.Equal(t, []int64{f.minePending}, ids)

	status, ids = queueGet(t, app, "/v1/catalog/works?status=pending&site=moyu", kungalMod)
	assert.Equal(t, fiber.StatusForbidden, status, "a cross-tenant queue request is refused, never re-pointed")
	assert.Empty(t, ids)
}

// TestWorksListStatusVocabulary: the parameter is a CLOSED vocabulary, and it
// cannot be combined with the claim_state gate it already is.
func TestWorksListStatusVocabulary(t *testing.T) {
	db := openCatalogTestDB(t)
	seedQueueWorks(t, db)
	app := queueApp(db, queueClients())
	mod := userTokenRoles(t, 720, ScopeCatalogEdit, "kungal-client", "user", "moderator")

	for _, tok := range []string{"draft", "banned", "merged", "stub", "LIVE", "PENDING", "true"} {
		status, _ := queueGet(t, app, "/v1/catalog/works?status="+tok, mod)
		assert.Equalf(t, fiber.StatusBadRequest, status, "status=%s is outside the vocabulary", tok)
	}
	status, _ := queueGet(t, app, "/v1/catalog/works?status=pending&claim_state=live", mod)
	assert.Equal(t, fiber.StatusBadRequest, status, "status=pending IS the claim gate; asking for both is a caller mistake")
}

// TestPendingQueueSite covers the gate as pure logic, so the ordering of its
// four doors is pinned without a database or an HTTP round trip.
func TestPendingQueueSite(t *testing.T) {
	owner := uint(9)
	clients := fakeClientLookup{
		"first-party": {ID: "first-party", CatalogSite: "kungal"},
		"unbound":     {ID: "unbound"},
		"third-party": {ID: "third-party", CatalogSite: "kungal", OwnerUserID: &owner},
	}
	mod := []string{"user", "moderator"}

	site, refusal := pendingQueueSite(t.Context(), clients, 5, mod, "first-party")
	require.Nil(t, refusal)
	assert.Equal(t, "kungal", site)

	for _, tc := range []struct {
		name     string
		uid      uint
		roles    []string
		clientID string
		lookup   OAuthClientLookup
	}{
		{"no user identity", 0, mod, "first-party", clients},
		{"no client registry installed", 5, mod, "first-party", nil},
		{"token not client-bound", 5, mod, "", clients},
		{"client not registered", 5, mod, "ghost", clients},
		{"third-party application", 5, mod, "third-party", clients},
		{"client bound to no site", 5, mod, "unbound", clients},
		{"roles without claim review", 5, []string{"user", "creator"}, "first-party", clients},
	} {
		site, refusal := pendingQueueSite(t.Context(), tc.lookup, tc.uid, tc.roles, tc.clientID)
		assert.NotNilf(t, refusal, "%s must be refused", tc.name)
		assert.Emptyf(t, site, "%s must yield no tenant to serve", tc.name)
	}

	// isThirdPartyClient is the wave-186b cap's single reading of the column.
	assert.False(t, isThirdPartyClient(nil))
	assert.False(t, isThirdPartyClient(&siteModel.OAuthClient{ID: "first-party"}))
	assert.True(t, isThirdPartyClient(&siteModel.OAuthClient{ID: "app", OwnerUserID: &owner}))
}
