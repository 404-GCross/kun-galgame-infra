package model

import "testing"

func TestPopulateEffectiveBanner(t *testing.T) {
	hashOf := func(g *Galgame) string {
		if g.EffectiveBannerHash == nil {
			return ""
		}
		return *g.EffectiveBannerHash
	}

	t.Run("pinned cover (sort_order=0) wins even when listed after others", func(t *testing.T) {
		g := &Galgame{Cover: []GalgameCover{
			{ImageHash: "h2", SortOrder: 2},
			{ImageHash: "h0", SortOrder: 0},
			{ImageHash: "h1", SortOrder: 1},
		}}
		PopulateEffectiveBanner(g)
		if got := hashOf(g); got != "h0" {
			t.Fatalf("want pinned h0, got %q", got)
		}
	})

	t.Run("falls back to lowest sort_order when no pinned cover (the v66155/61351 case)", func(t *testing.T) {
		g := &Galgame{Cover: []GalgameCover{{ImageHash: "only", SortOrder: 1}}}
		PopulateEffectiveBanner(g)
		if got := hashOf(g); got != "only" {
			t.Fatalf("want fallback to the lone cover, got %q", got)
		}
	})

	t.Run("picks the minimum sort_order among several non-zero covers", func(t *testing.T) {
		g := &Galgame{Cover: []GalgameCover{
			{ImageHash: "h3", SortOrder: 3},
			{ImageHash: "h1", SortOrder: 1},
			{ImageHash: "h2", SortOrder: 2},
		}}
		PopulateEffectiveBanner(g)
		if got := hashOf(g); got != "h1" {
			t.Fatalf("want lowest sort_order h1, got %q", got)
		}
	})

	t.Run("no covers leaves nil", func(t *testing.T) {
		g := &Galgame{}
		PopulateEffectiveBanner(g)
		if g.EffectiveBannerHash != nil {
			t.Fatalf("want nil banner for zero covers, got %q", *g.EffectiveBannerHash)
		}
	})

	t.Run("nil galgame is a no-op", func(t *testing.T) {
		PopulateEffectiveBanner(nil) // must not panic
	})
}
