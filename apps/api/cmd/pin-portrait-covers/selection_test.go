package main

import "testing"

func p(w, h int) (int, int, bool) { return w, h, true }

func TestSelectBest_LargestLongEdgeThenKindThenHash(t *testing.T) {
	w1, h1, k1 := p(600, 900)
	w2, h2, k2 := p(700, 1200) // tallest
	w3, h3, k3 := p(700, 1200) // tie on height with #2
	covers := []coverRow{
		{Hash: "b", Kind: "pkgfront", Width: w1, Height: h1, DimsKnown: k1},
		{Hash: "z", Kind: "pkgfront", Width: w2, Height: h2, DimsKnown: k2},
		{Hash: "a", Kind: "main", Width: w3, Height: h3, DimsKnown: k3},          // wins tie: main > pkgfront
		{Hash: "land", Kind: "main", Width: 1920, Height: 1000, DimsKnown: true}, // landscape excluded
		{Hash: "nodims", Kind: "main", DimsKnown: false},                         // unknown dims excluded
	}
	best := selectBest(covers)
	if best == nil || best.Hash != "a" {
		t.Fatalf("want best hash 'a' (main, 1200 tall), got %+v", best)
	}
}

func TestSelectBest_NoPortrait(t *testing.T) {
	covers := []coverRow{
		{Hash: "l1", Width: 1920, Height: 1080, DimsKnown: true},
		{Hash: "sq", Width: 500, Height: 510, DimsKnown: true}, // 510 <= 500*1.05 → not portrait
		{Hash: "u", DimsKnown: false},
	}
	if best := selectBest(covers); best != nil {
		t.Fatalf("want nil (no portrait), got %+v", best)
	}
}

func TestClassify_ThreeStatesPlusGuards(t *testing.T) {
	cases := []struct {
		name   string
		covers []coverRow
		want   state
	}{
		{"no portrait", []coverRow{{Hash: "l", Width: 1920, Height: 1080, DimsKnown: true}}, stateNoPortrait},
		{"direct pin >=1080", []coverRow{{Hash: "p", Width: 800, Height: 1200, DimsKnown: true}}, stateDirectPin},
		{"need upscale <1080", []coverRow{{Hash: "p", Width: 600, Height: 900, DimsKnown: true}}, stateNeedUpscale},
		{"nsfw deferred", []coverRow{{Hash: "p", Width: 800, Height: 1200, Sexual: 2, DimsKnown: true}}, stateNSFWDeferred},
		{"already pinned wins over nsfw", []coverRow{{Hash: "p", Width: 800, Height: 1200, Sexual: 2, PortraitPinned: true, DimsKnown: true}}, stateAlreadyPinned},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classify(1, c.covers)
			if got.State != c.want {
				t.Fatalf("classify %s = %v, want %v", c.name, got.State, c.want)
			}
		})
	}
}

func TestClassify_HasUpscaleFlag(t *testing.T) {
	covers := []coverRow{
		{Hash: "orig", Width: 600, Height: 900, DimsKnown: true},
		{Hash: "up", Source: "upscale", Width: 720, Height: 1080, DimsKnown: true},
	}
	sel := classify(1, covers)
	if !sel.HasUpscale {
		t.Fatal("want HasUpscale true")
	}
	// The upscale row (1080) is the largest portrait → direct-pin candidate.
	if sel.State != stateDirectPin || sel.Best == nil || sel.Best.Hash != "up" {
		t.Fatalf("want direct_pin on the upscale row, got state=%v best=%+v", sel.State, sel.Best)
	}
}
