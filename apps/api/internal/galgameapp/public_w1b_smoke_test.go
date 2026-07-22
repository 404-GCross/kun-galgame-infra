package galgameapp

import (
	"context"
	"net/http"
	"os"
	"strconv"
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
	"api/pkg/config"
	"api/pkg/oidctoken"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/require"
)

// End-to-end (in-process app.Test) smoke for the W1b /v1/galgame curated taxonomy
// four-family public faces (G6): every one of the 14 operations exercised over
// the REAL mountPublic wiring — the shared devapi chain, the taxonomy projection
// service, and Meilisearch (tag/official search skip if MS is unreachable). Seeds
// are scoped to a PRIVATE id band (960001-960004, distinct from W1a's 950001-
// 950003) + the w1bsmoke_ prefix and cleaned up, so the suite never touches the
// shared galgame / dev-key tables wholesale.

const (
	w1bGID       = 960001 // published sfw galgame carrying all four taxonomy families
	w1bNSFWGID   = 960002 // published nsfw galgame in the same series
	w1bSeriesID  = 960001
	w1bNamePfx   = "w1bsmoke"
	w1bIdxPrefix = "w1bsmoke_"
)

func w1bCleanup(t *testing.T) {
	t.Helper()
	ids := []int{w1bGID, w1bNSFWGID}
	for _, tbl := range []string{
		"galgame_tag_relation", "galgame_official_relation", "galgame_engine_relation",
	} {
		testDB.Exec("DELETE FROM "+tbl+" WHERE galgame_id IN ?", ids)
	}
	testDB.Exec("DELETE FROM galgame WHERE id IN ?", ids)
	testDB.Exec("DELETE FROM galgame_series WHERE id = ?", w1bSeriesID)
	testDB.Exec("DELETE FROM galgame_tag_alias WHERE galgame_tag_id IN (SELECT id FROM galgame_tag WHERE name LIKE 'w1bsmoke%')")
	testDB.Exec("DELETE FROM galgame_official_alias WHERE galgame_official_id IN (SELECT id FROM galgame_official WHERE name LIKE 'w1bsmoke%')")
	testDB.Exec("DELETE FROM galgame_tag WHERE name LIKE 'w1bsmoke%'")
	testDB.Exec("DELETE FROM galgame_official WHERE name LIKE 'w1bsmoke%'")
	testDB.Exec("DELETE FROM galgame_engine WHERE name LIKE 'w1bsmoke%'")
	testDB.Exec("DELETE FROM developer_api_keys WHERE client_id LIKE 'w1bsmoke_%'")
	testDB.Exec("DELETE FROM oauth_clients WHERE id LIKE 'w1bsmoke_%'")
}

// w1bSeed returns the auto-generated ids for the entities the by-id ops address.
func w1bSeed(t *testing.T) (tagA, tagB, official, engine int) {
	t.Helper()
	require.NoError(t, testDB.Create(&model.GalgameSeries{ID: w1bSeriesID, Name: w1bNamePfx + "series"}).Error)

	ta := &model.GalgameTag{Name: w1bNamePfx + "tag", Category: "content", Description: "tag description"}
	require.NoError(t, testDB.Create(ta).Error)
	require.NoError(t, testDB.Create(&model.GalgameTagAlias{GalgameTagID: ta.ID, Name: w1bNamePfx + "alias"}).Error)
	tb := &model.GalgameTag{Name: w1bNamePfx + "tagtwo", Category: "content"}
	require.NoError(t, testDB.Create(tb).Error)
	// A sexual-category tag exercises the sfw list gate.
	require.NoError(t, testDB.Create(&model.GalgameTag{Name: w1bNamePfx + "sexual", Category: "sexual"}).Error)

	off := &model.GalgameOfficial{
		Name: w1bNamePfx + "maker", Category: "company",
		Original: "オリジナル社", Link: "https://maker.example/", Lang: "ja-jp", Description: "maker description",
	}
	require.NoError(t, testDB.Create(off).Error)
	require.NoError(t, testDB.Create(&model.GalgameOfficialAlias{GalgameOfficialID: off.ID, Name: w1bNamePfx + "makeralias"}).Error)

	eng := &model.GalgameEngine{Name: w1bNamePfx + "engine", Description: "engine description", Alias: []byte(`["ew1b"]`)}
	require.NoError(t, testDB.Create(eng).Error)

	require.NoError(t, testDB.Create(&model.Galgame{
		ID: w1bGID, NameJaJP: w1bNamePfx + "作品", UserID: 1, Status: 0, ContentLimit: "sfw",
		OriginalLanguage: "ja-jp", AgeLimit: "all", ReleasePrecision: "day", SeriesID: intPtr(w1bSeriesID),
	}).Error)
	require.NoError(t, testDB.Create(&model.Galgame{
		ID: w1bNSFWGID, NameJaJP: w1bNamePfx + "成年", UserID: 1, Status: 0, ContentLimit: "nsfw",
		OriginalLanguage: "ja-jp", AgeLimit: "r18", ReleasePrecision: "day", SeriesID: intPtr(w1bSeriesID),
	}).Error)

	require.NoError(t, testDB.Create(&model.GalgameTagRelation{GalgameID: w1bGID, TagID: ta.ID}).Error)
	require.NoError(t, testDB.Create(&model.GalgameTagRelation{GalgameID: w1bGID, TagID: tb.ID}).Error)
	require.NoError(t, testDB.Create(&model.GalgameOfficialRelation{GalgameID: w1bGID, OfficialID: off.ID}).Error)
	require.NoError(t, testDB.Create(&model.GalgameEngineRelation{GalgameID: w1bGID, EngineID: eng.ID}).Error)

	return ta.ID, tb.ID, off.ID, eng.ID
}

func TestPublicW1bSmoke(t *testing.T) {
	if testDB == nil {
		t.Skip("no test DB")
	}
	// Reuse the W1a migration (all galgame + taxonomy tables).
	w1aMigrate(t)
	w1bCleanup(t)
	t.Cleanup(func() { w1bCleanup(t) })
	tagA, tagB, official, engine := w1bSeed(t)

	// Meilisearch (tag/official search sub-cases skip if unreachable).
	msHost := os.Getenv("MEILISEARCH_TEST_HOST")
	if msHost == "" {
		msHost = "http://127.0.0.1:7700"
	}
	msClient, msErr := searchInfra.NewClient(config.MeilisearchConfig{
		Host:        msHost,
		APIKey:      os.Getenv("MEILISEARCH_TEST_API_KEY"),
		IndexPrefix: w1bIdxPrefix,
	})
	msUp := msErr == nil && msClient.Health() == nil

	repo := galgameRepo.NewGalgameRepository(testDB)
	svc := galgameService.NewGalgameService(
		repo, galgameRepo.NewRevisionRepository(testDB),
		galgameRepo.NewPRRepository(testDB), galgameRepo.NewUserReadonlyRepository(testDB),
	).WithCDNBase("https://cdn.example.com/img")

	tagRepo := galgameRepo.NewTagRepository(testDB)
	officialRepo := galgameRepo.NewOfficialRepository(testDB)
	engineRepo := galgameRepo.NewEngineRepository(testDB)
	seriesRepo := galgameRepo.NewSeriesRepository(testDB)
	publicTaxSvc := galgameService.NewPublicTaxonomyService(tagRepo, officialRepo, engineRepo, seriesRepo, svc)

	var searchSvc *galgameSearch.Service
	if msUp {
		require.NoError(t, galgameSearch.EnsureIndexes(msClient))
		t.Cleanup(func() {
			for _, uid := range []string{galgameSearch.IndexGalgames, galgameSearch.IndexTags, galgameSearch.IndexOfficials} {
				_, _ = msClient.Svc().DeleteIndex(msClient.IndexUID(uid))
			}
		})
		searchSvc = galgameSearch.NewService(msClient)
		idxer := galgameSearch.NewIndexer(msClient)
		// Index the tag + official with their real published counts.
		ta, _ := tagRepo.FindByID(context.Background(), tagA)
		require.NoError(t, idxer.UpsertTag(context.Background(), ta))
		off, _ := officialRepo.FindByID(context.Background(), official)
		require.NoError(t, idxer.UpsertOfficial(context.Background(), off))
		require.Eventually(t, func() bool {
			tr, e1 := searchSvc.SearchTags(context.Background(), &galgameSearch.TagSearchRequest{Query: w1bNamePfx + "tag", Limit: 10})
			or, e2 := searchSvc.SearchOfficials(context.Background(), &galgameSearch.OfficialSearchRequest{Query: w1bNamePfx + "maker", Limit: 10})
			return e1 == nil && e2 == nil && tr.Total >= 1 && or.Total >= 1
		}, 15*time.Second, 300*time.Millisecond, "meili did not index the taxonomy seeds in time")
	} else {
		t.Logf("meilisearch unreachable (%v) — tag/official search sub-cases skipped", msErr)
	}

	galgameH := galgameHandler.NewGalgameHandler(svc, nil, nil)
	entityH := galgameHandler.NewEntityGalgamesHandler(officialRepo, tagRepo, svc)

	application := &app.App{Fiber: fiber.New()}
	face := devapiFace{
		mw:       devapi.NewMiddleware(devapi.NewRepository(testDB), devapi.NewRedisStore(nil)),
		usageRec: devapi.NewUsageRecorder(devapi.NewRepository(testDB), devapi.NewRedisStore(nil)),
	}
	optionalJWT := middleware.OptionalJWT(oidctoken.NewVerifierWithJWKS(testJWTSecret, ""))
	mountPublic(application, face, svc, searchSvc, galgameH, entityH, publicTaxSvc, optionalJWT)
	f := application.Fiber

	readKey := w1bMintKey(t, "w1bsmoke_read", devapi.TierInternal, []string{devapi.ScopeGalgameRead}, false)
	nsfwKey := w1bMintKey(t, "w1bsmoke_nsfw", devapi.TierInternal, []string{devapi.ScopeGalgameRead, devapi.ScopeGalgameNSFW}, true)

	pathID := func(base string, id int) string { return base + "/" + strconv.Itoa(id) }

	// ── 1. tags list: sfw hides the sexual category ──
	_, env := doReq(t, f, "/v1/galgame/tags?limit=100", readKey, "", nil)
	tagList := dataOf(t, env)
	require.GreaterOrEqual(t, tagList["total"].(float64), float64(1))
	require.True(t, listHasName(tagList["items"], w1bNamePfx+"tag"), "sfw tag list must contain the content tag")
	require.False(t, listHasName(tagList["items"], w1bNamePfx+"sexual"), "sfw tag list must hide the sexual category")

	// ── 2. tag detail by id: curated fields ──
	_, env = doReq(t, f, pathID("/v1/galgame/tags", tagA), readKey, "", nil)
	td := dataOf(t, env)
	require.Equal(t, w1bNamePfx+"tag", td["name"])
	require.Equal(t, "tag description", td["description"])
	require.Equal(t, float64(1), td["galgame_count"])
	require.Equal(t, []any{w1bNamePfx + "alias"}, td["aliases"])
	require.NotEmpty(t, td["created"])
	require.NotEmpty(t, td["updated"])

	// ── 3. tag by-id 404 on a missing id ──
	resp404, _ := doReq(t, f, "/v1/galgame/tags/999999", readKey, "", nil)
	require.Equal(t, http.StatusNotFound, resp404.StatusCode)

	// ── 4. tags/multi: galgames carrying ALL given tag ids ──
	_, env = doReq(t, f, "/v1/galgame/tags/multi?ids="+strconv.Itoa(tagA)+","+strconv.Itoa(tagB), readKey, "", nil)
	multi := dataOf(t, env)
	require.Equal(t, float64(1), multi["total"])
	multiItems := multi["items"].([]any)
	require.Len(t, multiItems, 1)
	require.Equal(t, float64(w1bGID), multiItems[0].(map[string]any)["id"])

	// ── 5. tag galgame-ids ──
	_, env = doReq(t, f, pathID("/v1/galgame/tags", tagA)+"/galgame-ids", readKey, "", nil)
	require.Equal(t, []any{float64(w1bGID)}, dataOf(t, env)["ids"])

	// ── 6. officials list + detail + galgame-ids ──
	_, env = doReq(t, f, "/v1/galgame/officials?limit=100", readKey, "", nil)
	require.True(t, listHasName(dataOf(t, env)["items"], w1bNamePfx+"maker"))
	_, env = doReq(t, f, pathID("/v1/galgame/officials", official), readKey, "", nil)
	od := dataOf(t, env)
	require.Equal(t, "オリジナル社", od["original"])
	require.Equal(t, "https://maker.example/", od["link"])
	require.Equal(t, "ja-jp", od["lang"])
	require.Equal(t, "maker description", od["description"])
	require.Equal(t, []any{w1bNamePfx + "makeralias"}, od["aliases"])
	require.Equal(t, float64(1), od["galgame_count"])
	_, env = doReq(t, f, pathID("/v1/galgame/officials", official)+"/galgame-ids", readKey, "", nil)
	require.Equal(t, []any{float64(w1bGID)}, dataOf(t, env)["ids"])

	// ── 7. engines list + detail + galgame-ids ──
	_, env = doReq(t, f, "/v1/galgame/engines?limit=100", readKey, "", nil)
	require.True(t, listHasName(dataOf(t, env)["items"], w1bNamePfx+"engine"))
	_, env = doReq(t, f, pathID("/v1/galgame/engines", engine), readKey, "", nil)
	ed := dataOf(t, env)
	require.Equal(t, "engine description", ed["description"])
	require.Equal(t, []any{"ew1b"}, ed["alias"])
	require.Equal(t, float64(1), ed["galgame_count"])
	_, env = doReq(t, f, pathID("/v1/galgame/engines", engine)+"/galgame-ids", readKey, "", nil)
	require.Equal(t, []any{float64(w1bGID)}, dataOf(t, env)["ids"])

	// ── 8. series list: preview is sfw-gated (nsfw member dropped) ──
	_, env = doReq(t, f, "/v1/galgame/series?limit=50", readKey, "", nil)
	serList := dataOf(t, env)
	var serItem map[string]any
	for _, it := range serList["items"].([]any) {
		m := it.(map[string]any)
		if int(m["id"].(float64)) == w1bSeriesID {
			serItem = m
		}
	}
	require.NotNil(t, serItem, "seeded series missing from the list")
	require.Equal(t, float64(1), serItem["galgame_count"], "sfw series count must reflect the gated set")
	prev := serItem["galgames"].([]any)
	require.Len(t, prev, 1, "sfw series preview must drop the nsfw member")
	require.Equal(t, float64(w1bGID), prev[0].(map[string]any)["id"])

	// ── 9. series detail: sfw member only ──
	_, env = doReq(t, f, pathID("/v1/galgame/series", w1bSeriesID), readKey, "", nil)
	sd := dataOf(t, env)
	require.Equal(t, float64(1), sd["galgame_count"])
	require.Len(t, sd["galgames"].([]any), 1)

	// ── 10. series detail with an nsfw-scope key + content_limit=all: both members ──
	_, env = doReq(t, f, pathID("/v1/galgame/series", w1bSeriesID)+"?content_limit=all", nsfwKey, "", nil)
	sdAll := dataOf(t, env)
	require.Equal(t, float64(2), sdAll["galgame_count"], "nsfw-scope key content_limit=all must include both members")
	require.Len(t, sdAll["galgames"].([]any), 2)

	if !msUp {
		return
	}

	// ── 11. tags/search (Meilisearch) ──
	_, env = doReq(t, f, "/v1/galgame/tags/search?q="+w1bNamePfx+"tag", readKey, "", nil)
	ts := dataOf(t, env)
	require.GreaterOrEqual(t, ts["total"].(float64), float64(1))
	require.Contains(t, ts, "processing_time_ms")
	require.True(t, searchHasID(ts["items"], tagA), "tag search must surface the seeded tag")

	// ── 12. officials/search (Meilisearch) ──
	_, env = doReq(t, f, "/v1/galgame/officials/search?q="+w1bNamePfx+"maker", readKey, "", nil)
	osr := dataOf(t, env)
	require.GreaterOrEqual(t, osr["total"].(float64), float64(1))
	require.True(t, searchHasID(osr["items"], official), "official search must surface the seeded maker")
	// original / lang are curated onto the maker search item.
	for _, it := range osr["items"].([]any) {
		if int(it.(map[string]any)["id"].(float64)) == official {
			require.Equal(t, "ja-jp", it.(map[string]any)["lang"])
		}
	}
}

// ── smoke helpers ──

// w1bMintKey provisions a w1bsmoke_ client + one key (mirrors w1aMintKey).
func w1bMintKey(t *testing.T, clientID, tier string, scopes []string, nsfw bool) string {
	return w1aMintKey(t, clientID, tier, scopes, nsfw)
}

func listHasName(items any, name string) bool {
	arr, ok := items.([]any)
	if !ok {
		return false
	}
	for _, it := range arr {
		if m, ok := it.(map[string]any); ok && m["name"] == name {
			return true
		}
	}
	return false
}

func searchHasID(items any, id int) bool {
	arr, ok := items.([]any)
	if !ok {
		return false
	}
	for _, it := range arr {
		if m, ok := it.(map[string]any); ok {
			if n, ok := m["id"].(float64); ok && int(n) == id {
				return true
			}
		}
	}
	return false
}
