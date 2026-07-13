package portraitfill

import "testing"

func TestIsPortrait(t *testing.T) {
	cases := []struct {
		w, h int64
		want bool
	}{
		{790, 560, false}, // landscape main cover (VNDB box)
		{560, 790, true},  // portrait
		{500, 500, false}, // square is not portrait
		{100, 105, false}, // exactly 1.05 -> strict, not portrait
		{100, 106, true},  // just over 1.05
		{2560, 1809, false},
		{0, 0, false},
	}
	for _, c := range cases {
		if got := isPortrait(c.w, c.h); got != c.want {
			t.Errorf("isPortrait(%d,%d)=%v want %v", c.w, c.h, got, c.want)
		}
	}
}

func TestLongEdgeBucket(t *testing.T) {
	cases := []struct {
		v    int
		want string
	}{
		{100, "<512"}, {511, "<512"}, {512, "512-767"}, {767, "512-767"},
		{768, "768-1079"}, {1079, "768-1079"}, {1080, ">=1080"}, {4000, ">=1080"},
	}
	for _, c := range cases {
		if got := longEdgeBucket(c.v); got != c.want {
			t.Errorf("longEdgeBucket(%d)=%q want %q", c.v, got, c.want)
		}
	}
}

func TestRating(t *testing.T) {
	cases := []struct {
		in   int
		want int16
	}{
		{0, 0}, {49, 0}, {50, 1}, {149, 1}, {150, 2}, {200, 2}, {250, 2}, {-10, 0},
	}
	for _, c := range cases {
		if got := rating(c.in); got != c.want {
			t.Errorf("rating(%d)=%d want %d", c.in, got, c.want)
		}
	}
	if got := ratingFloat(1.5); got != 2 {
		t.Errorf("ratingFloat(1.5)=%d want 2", got)
	}
	if got := ratingFloat(0.4); got != 0 {
		t.Errorf("ratingFloat(0.4)=%d want 0", got)
	}
}

func TestGroupNoPortrait(t *testing.T) {
	// hashes: L=landscape, P=portrait, U=unknown (absent from dims)
	dims := map[string][2]int64{
		"L": {800, 600},
		"P": {600, 800},
	}
	// bid comes from the catalog exact anchor keyed by catalog_work_id.
	w1, w3 := int64(1001), int64(1003)
	bidByWork := map[int64]int{w1: 10, w3: 33}
	rows := []coverRow{
		{ID: 1, VndbID: "v1", CatalogWorkID: &w1, ImageHash: "L"}, // game 1: only landscape -> candidate (bid 10)
		{ID: 2, VndbID: "v2", ImageHash: "L"},                     // game 2: landscape + portrait -> NOT candidate
		{ID: 2, VndbID: "v2", ImageHash: "P"},
		{ID: 3, VndbID: "v3", CatalogWorkID: &w3, ImageHash: "U"}, // game 3: unknown dims -> candidate (bid 33)
		{ID: 4, VndbID: "v4", ImageHash: "P"},                     // game 4: portrait -> NOT candidate
	}
	got := groupNoPortrait(rows, dims, bidByWork)
	want := []int{1, 3}
	if len(got) != len(want) {
		t.Fatalf("got %d candidates %+v, want ids %v", len(got), got, want)
	}
	for i, c := range got {
		if c.ID != want[i] {
			t.Errorf("candidate[%d].ID=%d want %d", i, c.ID, want[i])
		}
	}
	// carried fields survive grouping; bid resolved from the anchor map
	if got[0].VNDBID != "v1" || got[0].BID != 10 {
		t.Errorf("candidate 1 fields = %+v", got[0])
	}
	if got[1].BID != 33 {
		t.Errorf("candidate 3 bid = %d, want 33 (from anchor)", got[1].BID)
	}
}
