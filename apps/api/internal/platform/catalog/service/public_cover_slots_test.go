// public_cover_slots_test.go — the slot picker on its own, with no database in
// the way: pickCoverSlots only needs a CDN base, so every case here is a pure
// function of the rows and their dimensions.
package service

import "testing"

// slotSvc is the picker's whole world: a CDN base (so imageURL is non-empty)
// and nothing else. The source map stays nil — an unknown source is "".
func slotSvc() *PublicService { return &PublicService{cdnBase: testCDNBase} }

func slotRow(seed, kind string, pinned bool) WorkCoverRow {
	return WorkCoverRow{ImageHash: hash64(seed), Kind: kind, PortraitPinned: pinned}
}

// TestDiscFaceIsNeverABanner is the kungal.com/galgame/51 bug, pinned: a DVD
// disc face scans at 1084x1080, which clears any "not portrait" test and then
// wins the hero slot because it happens to sort first. Near-square is not wide,
// and packaging is not artwork — it fails on both counts.
func TestDiscFaceIsNeverABanner(t *testing.T) {
	svc := slotSvc()
	disc, art := slotRow("d15c", "pkgmed", false), slotRow("a217", "dig", true)
	meta := map[string]ImageMeta{
		disc.ImageHash: {Width: 1084, Height: 1080},
		art.ImageHash:  {Width: 720, Height: 1080},
	}

	slots := svc.pickCoverSlots([]WorkCoverRow{disc, art}, meta, false)
	if slots == nil {
		t.Fatal("slots = nil, want the work's covers resolved")
	}
	if slots.Banner != nil {
		t.Fatalf("banner = %+v, want null: the work has no wide artwork at all", slots.Banner)
	}
	if slots.Portrait == nil || slots.Portrait.URL != svc.imageURL(art.ImageHash) {
		t.Fatalf("portrait = %+v, want the pinned digital cover", slots.Portrait)
	}
}

func TestBannerNeedsWidthAndArtwork(t *testing.T) {
	cases := []struct {
		name       string
		kind       string
		w, h       int
		wantBanner bool
	}{
		{"wide artwork", "dig", 1920, 1080, true},
		{"exactly 3:2 qualifies", "main", 1200, 800, true},
		{"4:3 is not hero art", "main", 1024, 768, false},
		{"near-square is not hero art", "main", 1084, 1080, false},
		{"wide but a photo of the box back", "pkgback", 1920, 1080, false},
		{"wide but a booklet page", "pkgcontent", 1920, 1080, false},
		{"wide but a spine", "pkgside", 1920, 1080, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc := slotSvc()
			row := slotRow("bb01", c.kind, false)
			slots := svc.pickCoverSlots([]WorkCoverRow{row},
				map[string]ImageMeta{row.ImageHash: {Width: c.w, Height: c.h}}, false)
			if got := slots.Banner != nil; got != c.wantBanner {
				t.Fatalf("banner filled = %v, want %v (%dx%d %q)", got, c.wantBanner, c.w, c.h, c.kind)
			}
		})
	}
}

// The portrait chain prefers artwork but never leaves a card blank: a work
// whose only portrait image is a box back still renders it.
func TestPortraitPrefersArtworkButFallsBack(t *testing.T) {
	svc := slotSvc()
	back, front := slotRow("0bac", "pkgback", false), slotRow("0f20", "pkgfront", false)
	meta := map[string]ImageMeta{
		back.ImageHash:  {Width: 800, Height: 1200},
		front.ImageHash: {Width: 700, Height: 1000},
	}

	// The box back sorts first, but the front is the artwork.
	slots := svc.pickCoverSlots([]WorkCoverRow{back, front}, meta, false)
	if slots.Portrait == nil || slots.Portrait.URL != svc.imageURL(front.ImageHash) {
		t.Fatalf("portrait = %+v, want the package front", slots.Portrait)
	}

	// Alone, the box back is all there is — better than an empty card.
	slots = svc.pickCoverSlots([]WorkCoverRow{back}, meta, false)
	if slots.Portrait == nil || slots.Portrait.URL != svc.imageURL(back.ImageHash) {
		t.Fatalf("portrait = %+v, want the box back as the last resort", slots.Portrait)
	}
}

// An editorial pin outranks the kind ladder: whoever set portrait_pinned looked
// at the picture, and the read face does not second-guess that.
func TestPinnedRowWinsWhateverItsKind(t *testing.T) {
	svc := slotSvc()
	pinned, art := slotRow("9911", "pkgmed", true), slotRow("22aa", "dig", false)
	meta := map[string]ImageMeta{
		pinned.ImageHash: {Width: 900, Height: 1200},
		art.ImageHash:    {Width: 720, Height: 1080},
	}
	slots := svc.pickCoverSlots([]WorkCoverRow{art, pinned}, meta, false)
	if slots.Portrait == nil || slots.Portrait.URL != svc.imageURL(pinned.ImageHash) {
		t.Fatalf("portrait = %+v, want the pinned row", slots.Portrait)
	}
}
