package service

import (
	"context"
	"sync/atomic"
	"testing"

	"api/internal/platform/galgame/repository"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestW1dDetailRefsAndContributors covers W1d rulings ①②⑦: the detail
// taxonomy refs gain galgame_count (all three) + official link/aliases, and the
// new include=contributors block carries the contributor user ids. All are
// projection-only (the FindByID preload already loads cnt / official alias /
// contributor) and stay behind their include tokens (byte-frozen default).
func TestW1dDetailRefsAndContributors(t *testing.T) {
	svc := &GalgameService{cdnBase: "https://cdn.example.com/img"}

	inc := PublicInclude{Taxonomy: true, TagRefs: true, OfficialRefs: true, EngineRefs: true, Contributors: true}
	m := toMap(t, svc.projectDetail(w1aGalgame(), sampleScoreMeta(), inc, "sfw", 0, false))

	tax, ok := m["taxonomy"].(map[string]any)
	if !ok {
		t.Fatalf("taxonomy block missing")
	}

	// ① tag_refs galgame_count
	tr0 := tax["tag_refs"].([]any)[0].(map[string]any)
	if tr0["galgame_count"] != float64(15) {
		t.Errorf("tag_ref galgame_count = %v, want 15", tr0["galgame_count"])
	}

	// ② official_refs link + aliases + galgame_count
	or0 := tax["official_refs"].([]any)[0].(map[string]any)
	if or0["link"] != "https://maker.example/" {
		t.Errorf("official_ref link = %v, want https://maker.example/", or0["link"])
	}
	if or0["galgame_count"] != float64(8) {
		t.Errorf("official_ref galgame_count = %v, want 8", or0["galgame_count"])
	}
	aliases, _ := or0["aliases"].([]any)
	if len(aliases) != 1 || aliases[0] != "maker-alias" {
		t.Errorf("official_ref aliases = %v, want [maker-alias]", or0["aliases"])
	}

	// ① engine_refs galgame_count
	er0 := tax["engine_refs"].([]any)[0].(map[string]any)
	if er0["galgame_count"] != float64(3) {
		t.Errorf("engine_ref galgame_count = %v, want 3", er0["galgame_count"])
	}

	// ⑦ contributors block = curated [{user_id}]
	contribs, ok := m["contributors"].([]any)
	if !ok || len(contribs) != 2 {
		t.Fatalf("contributors must be a 2-element array, got %v", m["contributors"])
	}
	c0 := contribs[0].(map[string]any)
	if len(c0) != 1 || c0["user_id"] != float64(88) {
		t.Errorf("contributor[0] = %v, want exactly {user_id:88}", c0)
	}

	// Default (no tokens): contributors absent (byte-frozen); taxonomy-alone keeps
	// its four frozen keys without the ref sub-keys.
	plain := toMap(t, svc.projectDetail(w1aGalgame(), sampleScoreMeta(), PublicInclude{}, "sfw", 0, false))
	if _, ok := plain["contributors"]; ok {
		t.Errorf("contributors must be omitted without include=contributors")
	}
}

// TestW1dIntroItemInclude covers ⑤: the item-level include=intro block is absent
// without the token and present (per-item) with it — on the aggregate list (also
// the batch/search/series/multi path via thinItems).
func TestW1dIntroItemInclude(t *testing.T) {
	cleanTables(t)
	migratePublicMeta(t)
	for i := 1; i <= 3; i++ {
		seedGameWithMeta(t, i)
	}
	ctx := context.Background()

	plain, err := testSvc.PublicList(ctx, "id", "", 10, "sfw", PublicItemInclude{})
	if err != nil {
		t.Fatalf("plain: %v", err)
	}
	for _, it := range plain.Items {
		if it.Intro != nil {
			t.Errorf("thin item must not carry intro without include")
		}
	}

	rich, err := testSvc.PublicList(ctx, "id", "", 10, "sfw", PublicItemInclude{Intro: true})
	if err != nil {
		t.Fatalf("rich: %v", err)
	}
	if len(rich.Items) != 3 {
		t.Fatalf("want 3 items, got %d", len(rich.Items))
	}
	for _, it := range rich.Items {
		if it.Intro == nil {
			t.Errorf("item %d: intro missing under include=intro", it.ID)
		}
	}
}

// TestW1dIntroNonN1 covers ⑤'s batched-loader discipline: the include=intro
// expansion is ONE IN across the whole page (query count constant across page
// size), mirroring the meta non-N+1 guard.
func TestW1dIntroNonN1(t *testing.T) {
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
		data, err := svc.PublicList(ctx, "id", "", limit, "sfw", PublicItemInclude{Intro: true})
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
}
