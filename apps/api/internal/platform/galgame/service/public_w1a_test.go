package service

import (
	"context"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/repository"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// W1a route-B endgame: item-level `meta` include + detail-level links /
// screenshots / series / meta + taxonomy rich-ref sub-keys. All add-only over
// the frozen step-02/07 contract — the pure tests pin the include gating +
// byte-identical default; the DB tests pin the batched (non-N+1) meta expansion.

// w1aGalgame is a fully-related galgame for the detail-include tests: series,
// two links, two screenshots (one NSFW), tag with spoiler, official with
// category/lang, engine.
func w1aGalgame() *model.Galgame {
	g := sampleGalgame()
	g.ResourceUpdateTime = model.Timestamp(time.Date(2026, 5, 1, 3, 4, 5, 0, time.UTC))
	g.Created = model.Timestamp(time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC))
	g.ReleasePrecision = "day"
	g.View = 4242
	g.UserID = 88
	g.Series = &model.GalgameSeries{ID: 3, Name: "series-z"}
	g.Link = []model.GalgameLink{
		{ID: 11, Name: "official site", Link: "https://ex.example/", Source: "", SourceKey: "website", UserID: 5},
		{ID: 12, Name: "steam", Link: "https://store.steampowered.com/app/1", Source: "vndb", SourceKey: "steam", UserID: 0},
	}
	g.Screenshot = []model.GalgameScreenshot{
		{ImageHash: "shot1", SortOrder: 0, Width: 1280, Height: 720, Thumbhash: "s1"},
		{ImageHash: "shotN", SortOrder: 1, Width: 1280, Height: 720, Thumbhash: "sN", Sexual: 3},
	}
	g.Tag = []model.GalgameTagRelation{
		{SpoilerLevel: 2, Tag: &model.GalgameTag{ID: 5, Name: "tag-a", Category: "content"}},
	}
	g.Official = []model.GalgameOfficialRelation{
		{Official: &model.GalgameOfficial{ID: 7, Name: "maker-x", Category: "company", Lang: "ja-jp"}},
	}
	g.Engine = []model.GalgameEngineRelation{
		{Engine: &model.GalgameEngine{ID: 9, Name: "engine-y"}},
	}
	return g
}

func TestProjectDetailW1aIncludeGating(t *testing.T) {
	svc := &GalgameService{cdnBase: "https://cdn.example.com/img"}

	// Without any W1a token: none of the new keys appear.
	plain := toMap(t, svc.projectDetail(w1aGalgame(), sampleScoreMeta(), PublicInclude{}, "sfw", 0, false))
	for _, k := range []string{"links", "series", "meta"} {
		if _, ok := plain[k]; ok {
			t.Errorf("%q must be omitted without its include token", k)
		}
	}
	if imgs, ok := plain["images"].(map[string]any); ok {
		if _, ok := imgs["screenshots"]; ok {
			t.Errorf("images.screenshots must be omitted without include=screenshots")
		}
	}

	// All W1a tokens on: every new block present + correctly shaped.
	inc := PublicInclude{
		Taxonomy: true, Links: true, Screenshots: true, Series: true, Meta: true,
		TagRefs: true, OfficialRefs: true, EngineRefs: true,
	}
	rec := svc.projectDetail(w1aGalgame(), sampleScoreMeta(), inc, "sfw", 12, false)
	m := toMap(t, rec)

	// links: curated {id,name,link,source}, source_key/user_id NOT surfaced.
	links, ok := m["links"].([]any)
	if !ok || len(links) != 2 {
		t.Fatalf("links must be a 2-element array, got %v", m["links"])
	}
	l0 := links[0].(map[string]any)
	wantLinkKeys := map[string]bool{"id": true, "name": true, "link": true, "source": true}
	if len(l0) != len(wantLinkKeys) {
		t.Errorf("link keys = %v, want exactly %v", keysOf(l0), wantLinkKeys)
	}
	for k := range l0 {
		if !wantLinkKeys[k] {
			t.Errorf("link carries unexpected key %q (source_key/user_id must not leak)", k)
		}
	}

	// screenshots: under images, NSFW-rated shot dropped on the sfw face.
	imgs := m["images"].(map[string]any)
	shots, ok := imgs["screenshots"].([]any)
	if !ok || len(shots) != 1 {
		t.Fatalf("images.screenshots must drop the NSFW shot on the sfw face (want 1), got %v", imgs["screenshots"])
	}

	// series: {id,name,galgame_count} with the resolved count.
	series := m["series"].(map[string]any)
	if series["id"].(float64) != 3 || series["name"] != "series-z" || series["galgame_count"].(float64) != 12 {
		t.Errorf("series ref wrong: %v", series)
	}

	// taxonomy rich refs.
	tax := m["taxonomy"].(map[string]any)
	tagRefs := tax["tag_refs"].([]any)
	tr0 := tagRefs[0].(map[string]any)
	if tr0["id"].(float64) != 5 || tr0["name"] != "tag-a" || tr0["category"] != "content" || tr0["spoiler_level"].(float64) != 2 {
		t.Errorf("tag_refs wrong: %v", tr0)
	}
	offRefs := tax["official_refs"].([]any)
	or0 := offRefs[0].(map[string]any)
	if or0["id"].(float64) != 7 || or0["category"] != "company" || or0["lang"] != "ja-jp" {
		t.Errorf("official_refs wrong: %v", or0)
	}
	engRefs := tax["engine_refs"].([]any)
	er0 := engRefs[0].(map[string]any)
	if er0["id"].(float64) != 9 || er0["name"] != "engine-y" {
		t.Errorf("engine_refs wrong: %v", er0)
	}
}

// TestTaxonomyAloneByteIdentical proves include=taxonomy WITHOUT the ref tokens
// is byte-for-byte the frozen four-key taxonomy block, and that a ref token
// without taxonomy is a silent no-op (no block emitted).
func TestTaxonomyAloneByteIdentical(t *testing.T) {
	svc := &GalgameService{cdnBase: "https://cdn.example.com/img"}

	frozen := toMap(t, svc.projectDetail(w1aGalgame(), sampleScoreMeta(), PublicInclude{Taxonomy: true}, "sfw", 0, false))
	tax := frozen["taxonomy"].(map[string]any)
	wantKeys := map[string]bool{"tags": true, "officials": true, "engines": true, "series_id": true}
	if len(tax) != len(wantKeys) {
		t.Errorf("include=taxonomy alone must keep exactly 4 keys, got %v", keysOf(tax))
	}
	for k := range tax {
		if !wantKeys[k] {
			t.Errorf("include=taxonomy alone leaked ref key %q", k)
		}
	}

	// tag_refs WITHOUT taxonomy → no taxonomy block at all (silent no-op).
	noTax := toMap(t, svc.projectDetail(w1aGalgame(), sampleScoreMeta(), PublicInclude{TagRefs: true}, "sfw", 0, false))
	if _, ok := noTax["taxonomy"]; ok {
		t.Errorf("a ref token without include=taxonomy must not emit the taxonomy block")
	}
}

// TestPublicMetaValueEquality proves the meta block's scalars equal the internal
// bridge face's values for the same row: vndb_id is the RAW stored id (not the
// empty→null curated discipline), timestamps match model.Timestamp's JSON, and
// series_id/catalog_work_id carry the raw pointer values.
func TestPublicMetaValueEquality(t *testing.T) {
	g := w1aGalgame()
	meta := buildPublicMeta(g)

	if meta.VNDBID != g.VNDBID {
		t.Errorf("meta.vndb_id = %q, want raw bridge value %q", meta.VNDBID, g.VNDBID)
	}
	if meta.OriginalLanguage != g.OriginalLanguage || meta.ContentLimit != g.ContentLimit ||
		meta.ReleasePrecision != g.ReleasePrecision || meta.Status != g.Status ||
		meta.UserID != g.UserID || meta.View != g.View {
		t.Errorf("meta scalar mismatch vs bridge row: %+v", meta)
	}
	if meta.SeriesID == nil || *meta.SeriesID != *g.SeriesID {
		t.Errorf("meta.series_id must equal the row pointer value")
	}
	if meta.CatalogWorkID == nil || *meta.CatalogWorkID != *g.CatalogWorkID {
		t.Errorf("meta.catalog_work_id must equal the row pointer value")
	}
	// Timestamps: equal model.Timestamp's JSON representation (the bridge wire).
	wantRUT := strings.Trim(mustMarshal(t, g.ResourceUpdateTime), `"`)
	if meta.ResourceUpdateTime == nil || *meta.ResourceUpdateTime != wantRUT {
		t.Errorf("meta.resource_update_time = %v, want %q (bridge Timestamp JSON)", meta.ResourceUpdateTime, wantRUT)
	}
	wantCreated := strings.Trim(mustMarshal(t, g.Created), `"`)
	if meta.Created == nil || *meta.Created != wantCreated {
		t.Errorf("meta.created = %v, want %q", meta.Created, wantCreated)
	}

	// Original (no VNDB entry): vndb_id is "" (raw), never null — mirrors bridge.
	g.VNDBID = ""
	if m := buildPublicMeta(g); m.VNDBID != "" {
		t.Errorf("meta.vndb_id for an original must be the raw empty string, got %q", m.VNDBID)
	}
}

// ── DB-backed: item-level meta expansion + non-N+1 batching ──

func seedGameWithMeta(t *testing.T, id int) {
	t.Helper()
	// A series row must exist for the series_id FK.
	if err := testDB.Create(&model.GalgameSeries{ID: id, Name: "series-" + strconv.Itoa(id)}).Error; err != nil {
		t.Fatalf("seed series %d: %v", id, err)
	}
	g := model.Galgame{
		ID: id, NameJaJP: "g" + strconv.Itoa(id), UserID: 70 + id, Status: 0,
		ContentLimit: "sfw", OriginalLanguage: "ja-jp", AgeLimit: "all",
		VNDBID: "v" + strconv.Itoa(1000+id), ReleasePrecision: "day",
		View: id * 100, SeriesID: intptr(id), CatalogWorkID: i64ptr(int64(9000 + id)),
	}
	if err := testDB.Create(&g).Error; err != nil {
		t.Fatalf("seed galgame %d: %v", id, err)
	}
}

func TestPublicListMetaInclude(t *testing.T) {
	cleanTables(t)
	migratePublicMeta(t)
	for i := 1; i <= 3; i++ {
		seedGameWithMeta(t, i)
	}
	ctx := context.Background()

	// Without include=meta: the thin item carries no meta key.
	plain, err := testSvc.PublicList(ctx, "id", "", 10, "sfw", PublicItemInclude{})
	if err != nil {
		t.Fatalf("plain: %v", err)
	}
	for _, it := range plain.Items {
		if it.Meta != nil {
			t.Errorf("thin item must not carry meta without include")
		}
	}

	// With include=meta: the block is present and its scalars equal the row.
	rich, err := testSvc.PublicList(ctx, "id", "", 10, "sfw", PublicItemInclude{Meta: true})
	if err != nil {
		t.Fatalf("rich: %v", err)
	}
	if len(rich.Items) != 3 {
		t.Fatalf("want 3 items, got %d", len(rich.Items))
	}
	for _, it := range rich.Items {
		if it.Meta == nil {
			t.Fatalf("item %d: meta missing under include=meta", it.ID)
		}
		if it.Meta.VNDBID != "v"+strconv.Itoa(1000+it.ID) ||
			it.Meta.View != it.ID*100 ||
			it.Meta.UserID != 70+it.ID ||
			it.Meta.SeriesID == nil || *it.Meta.SeriesID != it.ID ||
			it.Meta.CatalogWorkID == nil || *it.Meta.CatalogWorkID != int64(9000+it.ID) {
			t.Errorf("item %d: meta scalars wrong: %+v", it.ID, it.Meta)
		}
	}
}

func TestPublicListMetaNonN1(t *testing.T) {
	cleanTables(t)
	migratePublicMeta(t)
	for i := 1; i <= 6; i++ {
		seedGameWithMeta(t, i)
	}
	ctx := context.Background()

	count := func(limit int) int64 {
		var n int64
		sess := testDB.Session(&gorm.Session{Logger: countingLogger{Interface: logger.Default.LogMode(logger.Silent), n: &n}})
		svc := NewGalgameService(
			repository.NewGalgameRepository(sess),
			repository.NewRevisionRepository(sess),
			repository.NewPRRepository(sess),
			repository.NewUserReadonlyRepository(sess),
		).WithCDNBase("https://cdn.example.com/img")
		data, err := svc.PublicList(ctx, "id", "", limit, "sfw", PublicItemInclude{Meta: true})
		if err != nil {
			t.Fatalf("list(limit=%d): %v", limit, err)
		}
		if len(data.Items) != limit {
			t.Fatalf("want %d items, got %d", limit, len(data.Items))
		}
		return atomic.LoadInt64(&n)
	}

	small, large := count(2), count(6)
	if small != large {
		t.Errorf("query count must be constant across page size (non-N+1): 2-item=%d 6-item=%d", small, large)
	}
	// list + 2 pin lookups + 1 meta IN = 4. Guard against a per-item regression.
	if large > 5 {
		t.Errorf("expected a small constant query count (~4), got %d — possible N+1", large)
	}
}
