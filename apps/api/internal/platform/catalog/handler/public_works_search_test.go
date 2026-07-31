// public_works_search_test.go — A2-1d wire-level coverage of
// GET /v1/catalog/works/search: the parameter validation posture (which axes
// 400 and which degrade), the envelope shape, and the tags type joining the
// frozen entity-search lane.
//
// The ranking itself is covered at the service layer (which owns the
// Meilisearch round trip); everything here is reachable with no search engine,
// because a 400 must be decided before the query is ever built. The one case
// that needs a corpus says so and skips loudly without one.
package handler

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/catalog/repository"
	"api/internal/platform/catalog/service"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// worksSearchApp mounts the product search bare (no devapi chain), with NO
// indexer wired — every case below must be decided before a query is built.
func worksSearchApp(db *gorm.DB) *fiber.App {
	resolveSvc := service.NewResolveService(repository.NewRedirectRepository(db))
	publicSvc := service.NewPublicService(db, service.NewReadService(db), resolveSvc, "")
	h := NewPublicHandler(publicSvc, resolveSvc, nil, nil)
	app := fiber.New()
	app.Get("/v1/catalog/works/search", h.WorksSearch)
	return app
}

// TestWorksSearchParamValidation pins WHICH axes reject and which degrade —
// the distinction is the contract, not an implementation detail. Our own closed
// vocabularies 400 (a plausible 200 with the wrong ranking or the wrong
// distribution is the worst failure class); open vocabularies and include=
// tokens do not.
func TestWorksSearchParamValidation(t *testing.T) {
	db := openCatalogTestDB(t)
	app := worksSearchApp(db)

	rejected := []struct{ url, msg string }{
		// sort: a closed vocabulary. `view` was the deprecated face's fifth
		// lane and has no catalog counterpart — it must not silently no-op.
		{"/v1/catalog/works/search?sort=view", msgBadSearchSort},
		{"/v1/catalog/works/search?sort=nonsense", msgBadSearchSort},
		// facets: a closed vocabulary, and the INDEX attribute names are not
		// part of it (the wire speaks filter-parameter names).
		{"/v1/catalog/works/search?facets=bogus", msgBadSearchFacet},
		{"/v1/catalog/works/search?facets=content_rating,bogus", msgBadSearchFacet},
		{"/v1/catalog/works/search?facets=tag_ids", msgBadSearchFacet},
		// page / limit: present-but-illegal never degrades to page 1.
		{"/v1/catalog/works/search?page=0", msgBadPage},
		{"/v1/catalog/works/search?page=-3", msgBadPage},
		{"/v1/catalog/works/search?page=abc", msgBadPage},
		{"/v1/catalog/works/search?limit=0", msgBadLimit},
		{"/v1/catalog/works/search?limit=abc", msgBadLimit},
		// id filters: a dropped filter would serve the UNFILTERED first page.
		{"/v1/catalog/works/search?tag_id=0", "tag_id must be up to 10 comma-separated positive integers"},
		{"/v1/catalog/works/search?label_id=-1", "label_id must be a positive integer"},
		{"/v1/catalog/works/search?engine_id=abc", "engine_id must be a positive integer"},
		{"/v1/catalog/works/search?series_id=x", "series_id must be a positive integer"},
		// dates and the two enum-ish axes.
		{"/v1/catalog/works/search?released_after=2024", "released_after must be YYYY-MM-DD"},
		{"/v1/catalog/works/search?released_before=nope", "released_before must be YYYY-MM-DD"},
		{"/v1/catalog/works/search?content_rating=adult", "content_rating must be all_ages|sensitive|r18"},
		{"/v1/catalog/works/search?claimed=maybe", "claimed must be true|false"},
		// claim_state: our own closed vocabulary (A2-R1 区 C). A silently-ignored
		// token here would answer 200 with the very draft works the caller asked
		// to exclude — the production incident this parameter closes.
		{"/v1/catalog/works/search?claim_state=liev", msgBadClaimState},
		{"/v1/catalog/works/search?claim_state=LIVE", msgBadClaimState},
		{"/v1/catalog/works/search?claim_state=published", msgBadClaimState},
		{"/v1/catalog/works/search?claim_state=live,bogus", msgBadClaimState},
		{"/v1/catalog/works/search?claim_state=true", msgBadClaimState},
		// The works-list rule carried over: asking for r18 without opting in.
		{"/v1/catalog/works/search?content_rating=r18", "content_rating=r18 requires nsfw=1"},
	}
	for _, c := range rejected {
		code, body := getJSON(t, app, c.url)
		assert.Equalf(t, fiber.StatusBadRequest, code, "%s must 400", c.url)
		assert.Equalf(t, c.msg, body["message"], "%s message", c.url)
	}
}

// TestWorksSearchOpenVocabulariesDoNotReject is the counterpart: olang and
// include= must NOT 400. They reach the service (which then fails on the
// missing indexer with a 500) — the point is that validation let them past.
func TestWorksSearchOpenVocabulariesDoNotReject(t *testing.T) {
	db := openCatalogTestDB(t)
	app := worksSearchApp(db)

	for _, url := range []string{
		"/v1/catalog/works/search?olang=klingon",
		"/v1/catalog/works/search?olang=all",
		"/v1/catalog/works/search?claim_state=live",
		"/v1/catalog/works/search?claim_state=none,live,draft,hidden",
		"/v1/catalog/works/search?include=names,bogus",
		"/v1/catalog/works/search?sort=relevance&facets=content_rating,olang,claimed,tag_id,label_id,engine_id,series_id,source",
		"/v1/catalog/works/search?page=99999&limit=100",
	} {
		code, _ := getJSON(t, app, url)
		assert.NotEqualf(t, fiber.StatusBadRequest, code, "%s must not 400", url)
	}
}

// TestWorksSearchWithoutIndexerIs500 pins the misconfiguration path: a search
// face with no engine must fail loudly, never answer an empty 200 that reads as
// "nothing matched".
func TestWorksSearchWithoutIndexerIs500(t *testing.T) {
	db := openCatalogTestDB(t)
	code, _ := getJSON(t, worksSearchApp(db), "/v1/catalog/works/search?q=x")
	assert.Equal(t, fiber.StatusInternalServerError, code)
}

// TestPageNumPub unit-pins the page parser (no database needed).
func TestPageNumPub(t *testing.T) {
	n, ok := pageNumPub("")
	assert.True(t, ok)
	assert.Equal(t, 1, n, "absent page means the first page")
	n, ok = pageNumPub("  7 ")
	assert.True(t, ok)
	assert.Equal(t, 7, n)
	for _, bad := range []string{"0", "-1", "abc", "1.5"} {
		_, ok := pageNumPub(bad)
		assert.Falsef(t, ok, "page=%q must be rejected", bad)
	}
	// No upper clamp: a page past the end is an honest empty page, not a 400.
	n, ok = pageNumPub("1000000")
	assert.True(t, ok)
	assert.Equal(t, 1000000, n)
}

// TestWorksSearchOLangDefaultIsUngated pins wave 144's split: ONE parameter,
// two lanes, two different defaults — and nothing else about it moves.
//
// Search is the DISCOVERY surface, so an omitted olang means the whole
// population: once the registry stopped being uniformly 'ja', a family default
// here would have silently deleted every en/ru/ko work the consumer sites can
// find today. The calendar is the CURATED surface (新作月表) and keeps the ja+zh
// family — pinned end to end by TestCalendarBucketsWire, and unit-pinned against
// TestParsePublicOLang's expectations right below.
func TestWorksSearchOLangDefaultIsUngated(t *testing.T) {
	// The default, which is the whole point of the wave.
	assert.Equal(t, service.PublicOLang{All: true}, worksSearchOLang(""),
		"omitted olang on the search lane = no gate")
	assert.Equal(t, service.PublicOLang{All: true}, worksSearchOLang("   "))
	// An all-blank list degrades to the LANE's default, not to the calendar's.
	assert.Equal(t, service.PublicOLang{All: true}, worksSearchOLang(" , , "))
	// …while the calendar keeps the family for exactly the same inputs.
	assert.Equal(t, service.PublicOLang{}, parsePublicOLang(""))
	assert.Equal(t, service.PublicOLang{}, parsePublicOLang(" , , "))

	// Everything a caller states explicitly is identical across the two lanes.
	for _, raw := range []string{"all", "ja", " ja , zh-Hans ", "xx-Nope"} {
		assert.Equalf(t, parsePublicOLang(raw), worksSearchOLang(raw),
			"olang=%q must mean the same thing on both lanes", raw)
	}
	assert.Equal(t, service.PublicOLang{All: true}, worksSearchOLang("all"))
	assert.Equal(t, service.PublicOLang{Values: []string{"en"}}, worksSearchOLang("en"))

	// The gate's ETag discriminator does not collapse: the search lane's new
	// default folds to the SAME key as an explicit olang=all (they are the same
	// population), and still differs from the calendar's family default.
	assert.Equal(t, "all", worksSearchOLang("").Key())
	assert.Equal(t, "jazh", parsePublicOLang("").Key())
	assert.NotEqual(t, parsePublicOLang("").Key(), worksSearchOLang("").Key())
	// The calendar's population key is untouched by this wave.
	assert.Equal(t, "sfw-jazh-all",
		service.CalendarFilter{OLang: parsePublicOLang("")}.PopulationKey())
}

// TestPublicSearchIndexTagsType pins the A2-1b account this wave settles: the
// entity-search `type` vocabulary gains `tags`, and nothing else moves.
func TestPublicSearchIndexTagsType(t *testing.T) {
	for typ, wantEntity := range map[string]string{
		"names": "name", "characters": "character", "labels": "label",
		"works": "work", "tags": "tag",
	} {
		uid, entity, ok := publicSearchIndex(typ)
		require.Truef(t, ok, "type=%s must resolve", typ)
		assert.NotEmpty(t, uid)
		assert.Equalf(t, wantEntity, entity, "type=%s entity_type", typ)
	}
	_, _, ok := publicSearchIndex("engines")
	assert.False(t, ok, "the vocabulary stays closed — engines is not a search type")
	_, _, ok = publicSearchIndex("")
	assert.False(t, ok, "an absent type is still a 400, as before")
}

// TestEntityHitShapeFrozenForNonTagFamilies is the byte-freeze gate of this
// wave's search-lane change: the four pre-existing families must serialize
// EXACTLY as before, i.e. the new tier/kind keys must be absent — not null, not
// empty strings. A missing omitempty on either would show up here.
func TestEntityHitShapeFrozenForNonTagFamilies(t *testing.T) {
	for _, entity := range []string{"name", "character", "label", "work"} {
		hit := dto.PublicEntityHit{
			ID: 7, EntityType: entity, Name: "テスト", Sources: []string{"vndb:v1"},
		}
		if entity == "work" {
			hit.ContentRating = "all_ages"
		}
		raw, err := json.Marshal(hit)
		require.NoError(t, err)
		assert.NotContainsf(t, string(raw), `"tier"`, "%s hit gained a tier key", entity)
		assert.NotContainsf(t, string(raw), `"kind"`, "%s hit gained a kind key", entity)
	}
	// A tag hit carries both.
	raw, err := json.Marshal(dto.PublicEntityHit{
		ID: 3, EntityType: "tag", Name: "純愛", Sources: []string{}, Tier: "core", Kind: "content",
	})
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"tier":"core"`)
	assert.Contains(t, string(raw), `"kind":"content"`)
}

// TestPublicTagKeyProjections pins the handler-side vocabulary mirrors: the
// wire speaks string keys, never the enum ints the index stores.
func TestPublicTagKeyProjections(t *testing.T) {
	assert.Equal(t, "core", publicTagTierKey(0))
	assert.Equal(t, "longtail", publicTagTierKey(1))
	assert.Equal(t, "hidden", publicTagTierKey(2))
	assert.Equal(t, "core", publicTagTierKey(99), "an out-of-vocabulary tier falls back, never blank")
	assert.Equal(t, "content", publicTagKindKey(0))
	assert.Equal(t, "meta", publicTagKindKey(1))
}

// TestWorksSearchEnvelopeShape pins the wire envelope keys. Serialized from the
// DTO rather than from a live query so it holds with no search engine present.
func TestWorksSearchEnvelopeShape(t *testing.T) {
	raw, err := json.Marshal(dto.PublicWorksSearchData{
		Total: 42, Page: 2, Limit: 20, Items: []dto.PublicWorkListItem{},
	})
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	for _, k := range []string{"total", "page", "limit", "items"} {
		assert.Containsf(t, got, k, "envelope must carry %s", k)
	}
	// facets is opt-in: absent, never an empty object.
	assert.NotContains(t, got, "facets")

	withFacets, err := json.Marshal(dto.PublicWorksSearchData{
		Facets: map[string]map[string]int64{"content_rating": {"all_ages": 3}},
	})
	require.NoError(t, err)
	assert.Contains(t, string(withFacets), `"facets":{"content_rating":{"all_ages":3}}`)
}

// TestWorksSearchRouteOrder guards the registration order in cmd/catalog: the
// static /works/search path must win over the /works/:id catch-all, or the
// product search would be parsed as a work id and 400 on a non-numeric id.
func TestWorksSearchRouteOrder(t *testing.T) {
	db := openCatalogTestDB(t)
	resolveSvc := service.NewResolveService(repository.NewRedirectRepository(db))
	publicSvc := service.NewPublicService(db, service.NewReadService(db), resolveSvc, "")
	h := NewPublicHandler(publicSvc, resolveSvc, nil, nil)
	app := fiber.New()
	// Same order as setupPublicCatalog.
	app.Get("/v1/catalog/works", h.WorksList)
	app.Get("/v1/catalog/works/search", h.WorksSearch)
	app.Get("/v1/catalog/works/:id", h.WorkDetail)

	// The search route answers (500 = it reached the indexer-less service);
	// the detail route would have answered 400 "invalid id" instead.
	resp, err := app.Test(httptest.NewRequest("GET", "/v1/catalog/works/search?q=x", nil))
	require.NoError(t, err)
	assert.Equal(t, fiber.StatusInternalServerError, resp.StatusCode,
		"/works/search must not be swallowed by /works/:id")

	// A real numeric id still reaches the detail route.
	code, _ := getJSON(t, app, "/v1/catalog/works/999999999")
	assert.Equal(t, fiber.StatusNotFound, code)
}
