package service

import (
	"testing"

	"api/internal/platform/galgame/model"
)

// w1cGalgame is a fixture with a pinned cover + a caption-bearing screenshot,
// both carrying non-trivial + zero ratings, to exercise the W1c rating gate.
func w1cGalgame() *model.Galgame {
	g := w1aGalgame()
	bannerHash := "cov1"
	g.EffectiveBannerHash = &bannerHash
	g.Cover = []model.GalgameCover{
		{ImageHash: "cov1", SortOrder: 0, Width: 800, Height: 600, Thumbhash: "c1", Kind: "main", Sexual: 1, Violence: 0},
	}
	g.Screenshot = []model.GalgameScreenshot{
		{ImageHash: "shot1", SortOrder: 0, Width: 1280, Height: 720, Thumbhash: "s1", Caption: "opening", Sexual: 0, Violence: 2},
	}
	return g
}

// TestPublicImageRatingGating_W1c is the G1-style invariant for W1c: a scope-less
// (withRatings=false) response carries NO per-image sexual/violence keys — the
// covers/screenshots stay byte-frozen (only the ungated screenshot caption is
// added, which is on a W1a-new block with no legacy consumers). A scope-capable
// (withRatings=true) response carries sexual/violence on covers[]/screenshots[]
// INCLUDING the meaningful 0. The banner/portrait pins never carry ratings.
func TestPublicImageRatingGating_W1c(t *testing.T) {
	svc := &GalgameService{cdnBase: "https://cdn.example.com/img"}
	inc := PublicInclude{Covers: true, Screenshots: true}

	imgKeys := func(rec map[string]any, set string) []map[string]any {
		imgs := rec["images"].(map[string]any)
		arr, _ := imgs[set].([]any)
		out := make([]map[string]any, 0, len(arr))
		for _, e := range arr {
			out = append(out, e.(map[string]any))
		}
		return out
	}
	hasRating := func(m map[string]any) bool {
		_, s := m["sexual"]
		_, v := m["violence"]
		return s || v
	}

	// ── No scope: byte-frozen (no sexual/violence on any cover/screenshot). ──
	no := toMap(t, svc.projectDetail(w1cGalgame(), sampleScoreMeta(), inc, "all", 0, false))
	for _, c := range imgKeys(no, "covers") {
		if hasRating(c) {
			t.Errorf("no-scope cover must not carry sexual/violence: %v", c)
		}
	}
	for _, s := range imgKeys(no, "screenshots") {
		if hasRating(s) {
			t.Errorf("no-scope screenshot must not carry sexual/violence: %v", s)
		}
		if s["caption"] != "opening" {
			t.Errorf("caption is ungated (must be present): %v", s["caption"])
		}
	}
	// banner pin: never carries ratings even in the no-scope response.
	if b, ok := no["images"].(map[string]any)["banner"].(map[string]any); ok && hasRating(b) {
		t.Errorf("banner pin must never carry ratings: %v", b)
	}

	// ── With scope: sexual/violence present, INCLUDING the 0 value. ──
	yes := toMap(t, svc.projectDetail(w1cGalgame(), sampleScoreMeta(), inc, "all", 0, true))
	covers := imgKeys(yes, "covers")
	if len(covers) != 1 || covers[0]["sexual"].(float64) != 1 || covers[0]["violence"].(float64) != 0 {
		t.Errorf("scope cover ratings wrong (sexual=1,violence=0 incl 0): %v", covers)
	}
	shots := imgKeys(yes, "screenshots")
	if len(shots) != 1 || shots[0]["sexual"].(float64) != 0 || shots[0]["violence"].(float64) != 2 {
		t.Errorf("scope screenshot ratings wrong (sexual=0 incl 0,violence=2): %v", shots)
	}
	if shots[0]["caption"] != "opening" {
		t.Errorf("scope screenshot caption wrong: %v", shots[0]["caption"])
	}
	// banner pin still never carries ratings.
	if b, ok := yes["images"].(map[string]any)["banner"].(map[string]any); ok && hasRating(b) {
		t.Errorf("banner pin must never carry ratings even with scope: %v", b)
	}
}
