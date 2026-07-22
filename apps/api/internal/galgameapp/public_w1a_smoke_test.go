package galgameapp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"api/internal/app"
	searchInfra "api/internal/infrastructure/search"
	"api/internal/middleware"
	"api/internal/platform/devapi"
	galgameHandler "api/internal/platform/galgame/handler"
	"api/internal/platform/galgame/model"
	galgameRepo "api/internal/platform/galgame/repository"
	galgameSearch "api/internal/platform/galgame/search"
	galgameService "api/internal/platform/galgame/service"
	siteModel "api/internal/platform/site/model"
	"api/pkg/config"
	"api/pkg/oidctoken"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

// End-to-end (in-process app.Test) smoke for the W1a /v1/galgame enrichments
// (G6): every new include token, endpoint, and param exercised over the REAL
// mountPublic wiring — the shared devapi chain, sfw gate, the galgame service,
// and Meilisearch (skips if MS is unreachable). Seeds are scoped to a private id
// band (950001-950003) + the w1asmoke_ client/index prefixes and cleaned up, so
// the suite never touches the shared galgame or dev-key tables wholesale.

const (
	w1aGID       = 950001 // published sfw galgame, all relations
	w1aNSFWGID   = 950002 // published nsfw galgame
	w1aPendGID   = 950003 // pending (status=3) draft owned by w1aPendUID
	w1aSeriesID  = 950001
	w1aPendUID   = 777
	w1aOwnerUID  = 555
	w1aIdxPrefix = "w1asmoke_"
)

func w1aMigrate(t *testing.T) {
	t.Helper()
	require.NoError(t, testDB.AutoMigrate(
		&model.GalgameSeries{}, &model.GalgameTag{}, &model.GalgameTagAlias{},
		&model.GalgameOfficial{}, &model.GalgameOfficialAlias{}, &model.GalgameEngine{},
		&model.Galgame{}, &model.GalgameAlias{}, &model.GalgameTagRelation{},
		&model.GalgameOfficialRelation{}, &model.GalgameEngineRelation{},
		&model.GalgameLink{}, &model.GalgameCover{}, &model.GalgameScreenshot{},
		// Contributor is preloaded by FindByID — a fresh CI database needs the
		// table even though the smoke never seeds rows into it.
		&model.GalgameContributor{},
		&model.GalgameVNDBMeta{}, &model.GalgameBangumiMeta{}, &model.GalgameEGMeta{},
		&model.GalgameStats{},
	))
}

func w1aCleanup(t *testing.T) {
	t.Helper()
	ids := []int{w1aGID, w1aNSFWGID, w1aPendGID}
	for _, tbl := range []string{
		"galgame_tag_relation", "galgame_official_relation", "galgame_engine_relation",
		"galgame_link", "galgame_screenshot", "galgame_cover",
		"galgame_vndb_meta", "galgame_bangumi_meta", "galgame_eg_meta",
	} {
		testDB.Exec("DELETE FROM "+tbl+" WHERE galgame_id IN ?", ids)
	}
	testDB.Exec("DELETE FROM galgame WHERE id IN ?", ids)
	testDB.Exec("DELETE FROM galgame_series WHERE id = ?", w1aSeriesID)
	testDB.Exec("DELETE FROM galgame_tag WHERE name = ?", "smoke-tag")
	testDB.Exec("DELETE FROM galgame_official WHERE name = ?", "smoke-maker")
	testDB.Exec("DELETE FROM galgame_engine WHERE name = ?", "smoke-engine")
	testDB.Exec("DELETE FROM galgame_stats WHERE key = ?", "coverage")
	testDB.Exec("DELETE FROM developer_api_keys WHERE client_id LIKE 'w1asmoke_%'")
	testDB.Exec("DELETE FROM oauth_clients WHERE id LIKE 'w1asmoke_%'")
}

func w1aSeed(t *testing.T) {
	t.Helper()
	require.NoError(t, testDB.Create(&model.GalgameSeries{ID: w1aSeriesID, Name: "smoke-series"}).Error)
	tag := &model.GalgameTag{Name: "smoke-tag", Category: "content"}
	require.NoError(t, testDB.Create(tag).Error)
	off := &model.GalgameOfficial{Name: "smoke-maker", Category: "company", Lang: "ja-jp"}
	require.NoError(t, testDB.Create(off).Error)
	eng := &model.GalgameEngine{Name: "smoke-engine", Alias: datatypes.JSON([]byte("[]"))}
	require.NoError(t, testDB.Create(eng).Error)

	g1 := &model.Galgame{
		ID: w1aGID, NameJaJP: "スモークテスト作品", NameEnUS: "Smoke Test Work",
		VNDBID: "v950001", UserID: w1aOwnerUID, Status: 0, ContentLimit: "sfw",
		OriginalLanguage: "ja-jp", AgeLimit: "all", ReleasePrecision: "day",
		View: 10, SeriesID: intPtr(w1aSeriesID), CatalogWorkID: int64Ptr(950001),
	}
	require.NoError(t, testDB.Create(g1).Error)
	require.NoError(t, testDB.Create(&model.GalgameTagRelation{GalgameID: w1aGID, TagID: tag.ID, SpoilerLevel: 2}).Error)
	require.NoError(t, testDB.Create(&model.GalgameOfficialRelation{GalgameID: w1aGID, OfficialID: off.ID}).Error)
	require.NoError(t, testDB.Create(&model.GalgameEngineRelation{GalgameID: w1aGID, EngineID: eng.ID}).Error)
	require.NoError(t, testDB.Create(&model.GalgameLink{ID: 950011, GalgameID: w1aGID, Name: "official", Link: "https://s.example/", Source: "", UserID: w1aOwnerUID}).Error)
	require.NoError(t, testDB.Create(&model.GalgameCover{GalgameID: w1aGID, ImageHash: strings.Repeat("a", 64), SortOrder: 0}).Error)
	require.NoError(t, testDB.Create(&model.GalgameScreenshot{GalgameID: w1aGID, ImageHash: strings.Repeat("b", 64), SortOrder: 0}).Error)
	require.NoError(t, testDB.Create(&model.GalgameScreenshot{GalgameID: w1aGID, ImageHash: strings.Repeat("c", 64), SortOrder: 1, Sexual: 3}).Error)
	rating := 80.0
	require.NoError(t, testDB.Create(&model.GalgameVNDBMeta{GalgameID: w1aGID, VNDBID: "v950001", Rating: &rating, VoteCount: 100, SyncedAt: time.Now()}).Error)

	require.NoError(t, testDB.Create(&model.Galgame{
		ID: w1aNSFWGID, NameJaJP: "スモークテスト成年", VNDBID: "v950002", UserID: w1aOwnerUID,
		Status: 0, ContentLimit: "nsfw", OriginalLanguage: "ja-jp", AgeLimit: "r18", ReleasePrecision: "day",
	}).Error)
	require.NoError(t, testDB.Create(&model.Galgame{
		ID: w1aPendGID, NameJaJP: "スモークテスト下書き", VNDBID: "v950003", UserID: w1aPendUID,
		Status: 3, ContentLimit: "sfw", OriginalLanguage: "ja-jp", AgeLimit: "all", ReleasePrecision: "day",
	}).Error)

	payload := datatypes.JSON([]byte(`{"registered":1}`))
	require.NoError(t, testDB.Create(&model.GalgameStats{Key: "coverage", Payload: payload, BuiltAt: time.Date(2026, 7, 20, 5, 45, 0, 0, time.UTC)}).Error)
}

func intPtr(i int) *int       { return &i }
func int64Ptr(i int64) *int64 { return &i }

// w1aMintKey provisions a w1asmoke_ client + one key. nsfw sets both the client
// dev_nsfw_allowed and the key nsfw_allowed (MintKey forces the key flag off).
func w1aMintKey(t *testing.T, clientID, tier string, scopes []string, nsfw bool) string {
	t.Helper()
	client := &siteModel.OAuthClient{
		ID: clientID, Name: "w1a smoke", Secret: "x",
		RedirectURIs: datatypes.JSON([]byte("[]")), Grants: datatypes.JSON([]byte("[]")),
		AllowedScopes: datatypes.JSON([]byte("[]")), DevEnabled: true, DevTier: tier,
	}
	require.NoError(t, testDB.Create(client).Error)
	if nsfw {
		require.NoError(t, testDB.Exec("UPDATE oauth_clients SET dev_nsfw_allowed = true WHERE id = ?", clientID).Error)
	}
	svc := devapi.NewAdminService(devapi.NewRepository(testDB), devapi.NewRedisStore(nil))
	_, plaintext, err := svc.MintKey(context.Background(), clientID, devapi.MintKeyInput{Name: "k", Scopes: scopes}, 1)
	require.NoError(t, err)
	if nsfw {
		require.NoError(t, testDB.Exec("UPDATE developer_api_keys SET nsfw_allowed = true WHERE client_id = ?", clientID).Error)
	}
	return plaintext
}

// doReq drives one request, returning the full response + body so callers can
// read headers (ETag/304). extra headers (e.g. If-None-Match) ride in hdr.
func doReq(t *testing.T, f *fiber.App, path, apiKey, bearer string, hdr map[string]string) (*http.Response, map[string]any) {
	t.Helper()
	r := httptest.NewRequest("GET", path, nil)
	if apiKey != "" {
		r.Header.Set("X-API-Key", apiKey)
	}
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	resp, err := f.Test(r, fiber.TestConfig{Timeout: 10 * time.Second})
	require.NoError(t, err)
	raw, _ := io.ReadAll(resp.Body)
	var env map[string]any
	_ = json.Unmarshal(raw, &env)
	return resp, env
}

func dataOf(t *testing.T, env map[string]any) map[string]any {
	t.Helper()
	d, ok := env["data"].(map[string]any)
	require.True(t, ok, "envelope has no object data: %v", env)
	return d
}

func TestPublicW1aSmoke(t *testing.T) {
	if testDB == nil {
		t.Skip("no test DB")
	}
	w1aMigrate(t)
	w1aCleanup(t)
	t.Cleanup(func() { w1aCleanup(t) })
	w1aSeed(t)

	// Meilisearch (skips search sub-cases if unreachable). Host and key follow
	// the search suite's env convention — CI's instance requires the master key.
	msHost := os.Getenv("MEILISEARCH_TEST_HOST")
	if msHost == "" {
		msHost = "http://127.0.0.1:7700"
	}
	msClient, msErr := searchInfra.NewClient(config.MeilisearchConfig{
		Host:        msHost,
		APIKey:      os.Getenv("MEILISEARCH_TEST_API_KEY"),
		IndexPrefix: w1aIdxPrefix,
	})
	msUp := msErr == nil && msClient.Health() == nil
	if msUp {
		require.NoError(t, galgameSearch.EnsureIndexes(msClient))
		t.Cleanup(func() {
			for _, uid := range []string{galgameSearch.IndexGalgames, galgameSearch.IndexTags, galgameSearch.IndexOfficials} {
				_, _ = msClient.Svc().DeleteIndex(msClient.IndexUID(uid))
			}
		})
	} else {
		t.Logf("meilisearch unreachable (%v) — search sub-cases skipped", msErr)
	}

	// Build the public face over the test DB.
	repo := galgameRepo.NewGalgameRepository(testDB)
	svc := galgameService.NewGalgameService(
		repo, galgameRepo.NewRevisionRepository(testDB),
		galgameRepo.NewPRRepository(testDB), galgameRepo.NewUserReadonlyRepository(testDB),
	).WithCDNBase("https://cdn.example.com/img")
	var searchSvc *galgameSearch.Service
	if msUp {
		searchSvc = galgameSearch.NewService(msClient)
		idxer := galgameSearch.NewIndexer(msClient)
		for _, id := range []int{w1aGID, w1aPendGID} {
			var g model.Galgame
			require.NoError(t, testDB.Preload("Tag.Tag").Preload("Official.Official").
				Preload("Engine.Engine").Preload("Alias").Preload("Cover").First(&g, id).Error)
			require.NoError(t, idxer.UpsertGalgame(context.Background(), &g))
		}
		// Wait until the published seed is searchable.
		require.Eventually(t, func() bool {
			resp, err := searchSvc.SearchGalgames(context.Background(), &galgameSearch.GalgameSearchRequest{
				Query: "スモークテスト", Statuses: []int{0}, Page: 1, Limit: 10,
			})
			return err == nil && resp.Total >= 1
		}, 15*time.Second, 300*time.Millisecond, "meili did not index the seed in time")
	}
	galgameH := galgameHandler.NewGalgameHandler(svc, nil, nil)
	entityH := galgameHandler.NewEntityGalgamesHandler(
		galgameRepo.NewOfficialRepository(testDB), galgameRepo.NewTagRepository(testDB), svc)

	application := &app.App{Fiber: fiber.New()}
	face := devapiFace{
		mw:       devapi.NewMiddleware(devapi.NewRepository(testDB), devapi.NewRedisStore(nil)),
		usageRec: devapi.NewUsageRecorder(devapi.NewRepository(testDB), devapi.NewRedisStore(nil)),
	}
	optionalJWT := middleware.OptionalJWT(oidctoken.NewVerifierWithJWKS(testJWTSecret, ""))
	mountPublic(application, face, svc, searchSvc, galgameH, entityH, optionalJWT)
	f := application.Fiber

	readKey := w1aMintKey(t, "w1asmoke_read", devapi.TierInternal, []string{devapi.ScopeGalgameRead}, false)
	freeKey := w1aMintKey(t, "w1asmoke_free", devapi.TierFree, []string{devapi.ScopeGalgameRead}, false)
	nsfwKey := w1aMintKey(t, "w1asmoke_nsfw", devapi.TierInternal, []string{devapi.ScopeGalgameRead, devapi.ScopeGalgameNSFW}, true)
	jwt := signJWT(t, w1aPendUID)

	// ── 1. list default: no meta key ──
	_, env := doReq(t, f, "/v1/galgame?limit=50", readKey, "", nil)
	items := dataOf(t, env)["items"].([]any)
	require.NotEmpty(t, items)
	if _, ok := items[0].(map[string]any)["meta"]; ok {
		t.Errorf("list item must not carry meta without include")
	}

	// ── 2. list include=meta ──
	_, env = doReq(t, f, "/v1/galgame?include=meta&limit=50", readKey, "", nil)
	var g1item map[string]any
	for _, it := range dataOf(t, env)["items"].([]any) {
		m := it.(map[string]any)
		if int(m["id"].(float64)) == w1aGID {
			g1item = m
		}
	}
	require.NotNil(t, g1item, "seeded galgame missing from sfw list")
	meta := g1item["meta"].(map[string]any)
	require.Equal(t, "v950001", meta["vndb_id"])
	require.Equal(t, float64(10), meta["view"])
	require.Equal(t, float64(w1aOwnerUID), meta["user_id"])
	require.Equal(t, float64(w1aSeriesID), meta["series_id"])
	require.Equal(t, float64(950001), meta["catalog_work_id"])

	// ── 3. detail default: none of the new keys ──
	_, env = doReq(t, f, "/v1/galgame/950001", readKey, "", nil)
	d := dataOf(t, env)
	for _, k := range []string{"links", "series", "meta"} {
		if _, ok := d[k]; ok {
			t.Errorf("detail default leaked %q", k)
		}
	}
	if _, ok := d["images"].(map[string]any)["screenshots"]; ok {
		t.Errorf("detail default leaked images.screenshots")
	}

	// ── 4. detail all W1a includes ──
	_, env = doReq(t, f, "/v1/galgame/950001?include=links,screenshots,series,meta,taxonomy,tag_refs,official_refs,engine_refs", readKey, "", nil)
	d = dataOf(t, env)
	require.Len(t, d["links"].([]any), 1)
	require.Len(t, d["images"].(map[string]any)["screenshots"].([]any), 1, "nsfw screenshot must be dropped on the sfw face")
	series := d["series"].(map[string]any)
	require.Equal(t, "smoke-series", series["name"])
	require.GreaterOrEqual(t, series["galgame_count"].(float64), float64(1))
	require.NotNil(t, d["meta"])
	tax := d["taxonomy"].(map[string]any)
	require.Equal(t, float64(2), tax["tag_refs"].([]any)[0].(map[string]any)["spoiler_level"])
	require.Equal(t, "company", tax["official_refs"].([]any)[0].(map[string]any)["category"])
	require.Equal(t, "smoke-engine", tax["engine_refs"].([]any)[0].(map[string]any)["name"])

	// ── 5. include=taxonomy alone: exactly 4 keys (no refs) ──
	_, env = doReq(t, f, "/v1/galgame/950001?include=taxonomy", readKey, "", nil)
	tax = dataOf(t, env)["taxonomy"].(map[string]any)
	require.Len(t, tax, 4, "include=taxonomy alone must stay the frozen 4-key block: %v", tax)

	// ── 6a. track_view free tier: no bump ──
	viewNow := func() int {
		var g model.Galgame
		require.NoError(t, testDB.Select("view").First(&g, w1aGID).Error)
		return g.View
	}
	require.Equal(t, 10, viewNow())
	doReq(t, f, "/v1/galgame/950001?track_view=1", freeKey, "", nil)
	time.Sleep(400 * time.Millisecond)
	require.Equal(t, 10, viewNow(), "free-tier track_view must NOT bump the view counter")

	// ── 6b. track_view internal tier: bumps ──
	doReq(t, f, "/v1/galgame/950001?track_view=1", readKey, "", nil)
	require.Eventually(t, func() bool { return viewNow() == 11 }, 3*time.Second, 100*time.Millisecond,
		"internal-tier track_view must bump the view counter")

	// ── 7. stats: 200 + ETag, then 304 ──
	resp, env := doReq(t, f, "/v1/galgame/stats", readKey, "", nil)
	require.Equal(t, 200, resp.StatusCode)
	etag := resp.Header.Get("ETag")
	require.NotEmpty(t, etag)
	require.Contains(t, dataOf(t, env), "coverage")
	resp304, _ := doReq(t, f, "/v1/galgame/stats", readKey, "", map[string]string{"If-None-Match": etag})
	require.Equal(t, 304, resp304.StatusCode, "matching If-None-Match must 304")

	// ── 8. lookup ──
	_, env = doReq(t, f, "/v1/galgame/lookup?vndb_id=v950001", readKey, "", nil)
	d = dataOf(t, env)
	require.Equal(t, true, d["exists"])
	require.Equal(t, float64(w1aGID), d["id"])
	_, env = doReq(t, f, "/v1/galgame/lookup?vndb_id=v000000", readKey, "", nil)
	d = dataOf(t, env)
	require.Equal(t, false, d["exists"])
	if _, ok := d["id"]; ok {
		t.Errorf("lookup miss must omit id")
	}

	// ── 9. content_limit no-scope invariant: all/nsfw coerced to sfw = byte-identical ──
	_, envDefault := doReq(t, f, "/v1/galgame?limit=50", readKey, "", nil)
	_, envAll := doReq(t, f, "/v1/galgame?content_limit=all&limit=50", readKey, "", nil)
	_, envNSFW := doReq(t, f, "/v1/galgame?content_limit=nsfw&limit=50", readKey, "", nil)
	dj := func(m map[string]any) string { b, _ := json.Marshal(m["data"]); return string(b) }
	require.Equal(t, dj(envDefault), dj(envAll), "no-scope key: content_limit=all must be byte-identical to default (sfw)")
	require.Equal(t, dj(envDefault), dj(envNSFW), "no-scope key: content_limit=nsfw must be byte-identical to default (sfw)")
	// The nsfw galgame must NOT be present for the no-scope key.
	require.NotContains(t, dj(envDefault), `"v950002"`, "sfw list must not include the nsfw galgame")

	// ── 10. content_limit nsfw-scope positive ──
	_, envAllNSFW := doReq(t, f, "/v1/galgame?content_limit=all&limit=50&include=meta", nsfwKey, "", nil)
	require.Contains(t, dj(envAllNSFW), `"v950002"`, "nsfw-scope key with content_limit=all must include the nsfw galgame")

	if !msUp {
		return
	}

	// ── 11. search facets + highlight ──
	_, env = doReq(t, f, "/v1/galgame/search?q=スモークテスト&facets=1&highlight=1", readKey, "", nil)
	d = dataOf(t, env)
	require.Contains(t, d, "facets", "facets=1 must add a facets block")
	require.Contains(t, d, "highlight", "highlight=1 must add a highlight block")
	// Read the decoded value directly (re-marshaling would HTML-escape the < >).
	hlNames := d["highlight"].([]any)[0].(map[string]any)["names"].(map[string]any)
	require.Contains(t, hlNames["ja-jp"].(string), "<mark>", "highlight names must carry <mark> markup")
	// default search (no facets/highlight) must not carry the keys.
	_, envPlain := doReq(t, f, "/v1/galgame/search?q=スモークテスト", readKey, "", nil)
	dp := dataOf(t, envPlain)
	require.NotContains(t, dp, "facets")
	require.NotContains(t, dp, "highlight")
	require.NotContains(t, dp, "pending")

	// ── 12. include_pending dual-credential ──
	_, envPend := doReq(t, f, "/v1/galgame/search?q=スモークテスト&include_pending=1", readKey, jwt, nil)
	dPend := dataOf(t, envPend)
	require.Contains(t, dPend, "pending", "include_pending with a valid JWT must add the pending block")
	pendItems := dPend["pending"].([]any)
	require.NotEmpty(t, pendItems, "pending must contain the caller's own draft")
	foundDraft := false
	for _, it := range pendItems {
		if int(it.(map[string]any)["id"].(float64)) == w1aPendGID {
			foundDraft = true
		}
	}
	require.True(t, foundDraft, "pending must contain the caller's own draft (id %d)", w1aPendGID)
	// Without the JWT → no pending key.
	_, envNoJWT := doReq(t, f, "/v1/galgame/search?q=スモークテスト&include_pending=1", readKey, "", nil)
	require.NotContains(t, dataOf(t, envNoJWT), "pending", "include_pending without a JWT must be silently dropped")
}
