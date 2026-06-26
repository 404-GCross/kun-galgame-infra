package service

import (
	"context"
	"testing"

	"api/internal/platform/galgame/model"
)

// TestResolveImageMetaCachesAndDedups verifies cache-first behaviour: a hash is
// fetched once, duplicates/empties are dropped, and a second call only fetches
// the new (uncached) hashes.
func TestResolveImageMetaCachesAndDedups(t *testing.T) {
	calls := 0
	var gotHashes []string
	svc := &GalgameService{metaCache: newImageMetaCache(100)}
	svc.WithImageMeta(func(_ context.Context, hashes []string) (map[string]ImageMeta, error) {
		calls++
		gotHashes = append(gotHashes, hashes...)
		out := map[string]ImageMeta{}
		for _, h := range hashes {
			out[h] = ImageMeta{Width: 100, Height: 200, Thumbhash: "x"}
		}
		return out, nil
	})

	ctx := context.Background()
	got := svc.resolveImageMeta(ctx, []string{"a", "b", "a", ""})
	if len(got) != 2 {
		t.Fatalf("want 2 metas, got %d", len(got))
	}
	if calls != 1 {
		t.Fatalf("want 1 fetch, got %d", calls)
	}
	if len(gotHashes) != 2 {
		t.Fatalf("fetch should dedup to 2 hashes, got %v", gotHashes)
	}

	calls = 0
	gotHashes = nil
	got = svc.resolveImageMeta(ctx, []string{"a", "c"})
	if len(got) != 2 {
		t.Fatalf("want 2 metas (1 cached + 1 fetched), got %d", len(got))
	}
	if calls != 1 || len(gotHashes) != 1 || gotHashes[0] != "c" {
		t.Fatalf("only 'c' should be fetched; calls=%d hashes=%v", calls, gotHashes)
	}
}

// TestResolveImageMetaNilFuncSafe verifies enrichment is a safe no-op when no
// image-meta func is wired (returns only cache hits, i.e. none here).
func TestResolveImageMetaNilFuncSafe(t *testing.T) {
	svc := &GalgameService{metaCache: newImageMetaCache(10)}
	got := svc.resolveImageMeta(context.Background(), []string{"a"})
	if len(got) != 0 {
		t.Fatalf("nil imageMeta should yield no metas, got %d", len(got))
	}
}

// TestEnrichGalgameImagesFillsTransientFields verifies covers/screenshots get
// their transient width/height/thumbhash, and that an unknown hash stays zero.
func TestEnrichGalgameImagesFillsTransientFields(t *testing.T) {
	svc := &GalgameService{metaCache: newImageMetaCache(10)}
	svc.WithImageMeta(func(_ context.Context, _ []string) (map[string]ImageMeta, error) {
		return map[string]ImageMeta{
			"cover1": {Width: 300, Height: 400, Thumbhash: "tc"},
			"shot1":  {Width: 1920, Height: 1080, Thumbhash: "ts"},
		}, nil
	})
	g := &model.Galgame{
		Cover:      []model.GalgameCover{{ImageHash: "cover1"}, {ImageHash: "missing"}},
		Screenshot: []model.GalgameScreenshot{{ImageHash: "shot1"}},
	}
	svc.enrichGalgameImages(context.Background(), g)

	if g.Cover[0].Width != 300 || g.Cover[0].Height != 400 || g.Cover[0].Thumbhash != "tc" {
		t.Fatalf("cover1 not enriched: %+v", g.Cover[0])
	}
	if g.Cover[1].Width != 0 || g.Cover[1].Thumbhash != "" {
		t.Fatalf("missing cover should stay zero: %+v", g.Cover[1])
	}
	if g.Screenshot[0].Width != 1920 || g.Screenshot[0].Thumbhash != "ts" {
		t.Fatalf("shot1 not enriched: %+v", g.Screenshot[0])
	}
}

// TestChunkStrings covers the batching helper edge cases.
func TestChunkStrings(t *testing.T) {
	if got := chunkStrings([]string{"a", "b", "c"}, 2); len(got) != 2 ||
		len(got[0]) != 2 || len(got[1]) != 1 {
		t.Fatalf("want [[a b][c]], got %v", got)
	}
	if got := chunkStrings([]string{"a"}, 10); len(got) != 1 || len(got[0]) != 1 {
		t.Fatalf("single chunk expected, got %v", got)
	}
}
