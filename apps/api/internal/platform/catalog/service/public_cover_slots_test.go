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
		{"exactly 4:3 qualifies", "main", 1024, 768, true},
		{"just short of 4:3 is not hero art", "main", 1000, 768, false},
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

// Width is a tier, not a veto. The wiki bridge left a large population of
// 256px-wide thumbnails behind: correct shape, useless as a hero, and they used
// to win the slot outright by sorting first. A real banner now outranks them —
// but when the thumbnail is all the work owns it still fills the slot, because
// the alternative is center-cropping a portrait into a 16:9 frame.
func TestBannerPrefersTheWiderCandidate(t *testing.T) {
	svc := slotSvc()
	thumb, real := slotRow("7b17", "", false), slotRow("b16b", "dig", false)
	meta := map[string]ImageMeta{
		thumb.ImageHash: {Width: 256, Height: 144},
		real.ImageHash:  {Width: 1920, Height: 1080},
	}

	// The thumbnail sorts first and loses anyway.
	slots := svc.pickCoverSlots([]WorkCoverRow{thumb, real}, meta, false)
	if slots.Banner == nil || slots.Banner.URL != svc.imageURL(real.ImageHash) {
		t.Fatalf("banner = %+v, want the full-size art over the 256px thumbnail", slots.Banner)
	}

	// Alone, it is still better than nothing.
	slots = svc.pickCoverSlots([]WorkCoverRow{thumb}, meta, false)
	if slots.Banner == nil || slots.Banner.URL != svc.imageURL(thumb.ImageHash) {
		t.Fatalf("banner = %+v, want the thumbnail as the last resort", slots.Banner)
	}
}

// A work that declares its art display-safe must not be REPRESENTED by sexual
// art. Production had 223 such works whose hero was sexual-flagged, because the
// only gate was the caller's age switch — which every consumer holds open just
// to see that r18 works exist.
func TestSexualCoversNeedTheWorksOwnPermission(t *testing.T) {
	svc := slotSvc()
	sexy, safe := slotRow("5e59", "dig", false), slotRow("5afe", "dig", false)
	sexy.Sexual = 2
	meta := map[string]ImageMeta{
		sexy.ImageHash: {Width: 1920, Height: 740},
		safe.ImageHash: {Width: 1529, Height: 1080},
	}

	// Display-safe work: the sexual banner sorts first and is skipped entirely.
	slots := svc.pickCoverSlots([]WorkCoverRow{sexy, safe}, meta, false)
	if slots.Banner == nil || slots.Banner.URL != svc.imageURL(safe.ImageHash) {
		t.Fatalf("banner = %+v, want the display-safe art", slots.Banner)
	}

	// Its only WIDE art being sexual leaves that slot empty rather than filled:
	// the consumer falls back to the portrait, which is display-safe.
	tall := slotRow("7a11", "dig", false)
	meta[tall.ImageHash] = ImageMeta{Width: 600, Height: 900}
	slots = svc.pickCoverSlots([]WorkCoverRow{sexy, tall}, meta, false)
	if slots.Banner != nil {
		t.Fatalf("banner = %+v, want null for a display-safe work with only sexual wide art", slots.Banner)
	}
	if slots.Portrait == nil || slots.Portrait.URL != svc.imageURL(tall.ImageHash) {
		t.Fatalf("portrait = %+v, want the display-safe portrait", slots.Portrait)
	}

	// A work whose own display flag is set may use it — and a display-safe
	// candidate still wins its tier when one exists.
	slots = svc.pickCoverSlots([]WorkCoverRow{sexy}, meta, true)
	if slots.Banner == nil || slots.Banner.URL != svc.imageURL(sexy.ImageHash) {
		t.Fatalf("banner = %+v, want the sexual art once the work permits it", slots.Banner)
	}
	slots = svc.pickCoverSlots([]WorkCoverRow{sexy, safe}, meta, true)
	if slots.Banner == nil || slots.Banner.URL != svc.imageURL(safe.ImageHash) {
		t.Fatalf("banner = %+v, want the display-safe art to still win its tier", slots.Banner)
	}
}

// The safe-first preference reaches the portrait slot too, and it outranks the
// editorial pin: 1,413 display-safe works carried a sexual-flagged pin, which is
// the cover every card on the site renders.
func TestPortraitPrefersDisplaySafeOverAPin(t *testing.T) {
	svc := slotSvc()
	pinned, safe := slotRow("9d1a", "dig", true), slotRow("5afe", "dig", false)
	pinned.Sexual = 1
	meta := map[string]ImageMeta{
		pinned.ImageHash: {Width: 720, Height: 1080},
		safe.ImageHash:   {Width: 600, Height: 900},
	}

	slots := svc.pickCoverSlots([]WorkCoverRow{pinned, safe}, meta, false)
	if slots.Portrait == nil || slots.Portrait.URL != svc.imageURL(safe.ImageHash) {
		t.Fatalf("portrait = %+v, want the display-safe cover over a sexual pin", slots.Portrait)
	}
	slots = svc.pickCoverSlots([]WorkCoverRow{pinned, safe}, meta, true)
	if slots.Portrait == nil || slots.Portrait.URL != svc.imageURL(pinned.ImageHash) {
		t.Fatalf("portrait = %+v, want the pin honoured once the work permits it", slots.Portrait)
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
