package getchumedia

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A work's Getchu editions publish the same CG set, so the same bytes arrive
// twice. The (work_id, image_hash) key would collapse them, but only after
// paying for the upload — 2,556 of 16,199 across the backfill. The mirror
// recorded each file's sha256, so the duplicate is free to spot here.
func TestFillSkipsBytesAlreadySentForTheSameWork(t *testing.T) {
	staged := map[string][]stagedImage{
		"100": {
			{GetchuID: "100", File: "a.jpg", SHA256: "aaa"},
			{GetchuID: "100", File: "b.jpg", SHA256: "bbb"},
		},
		// The DL edition: same two CG, different filenames.
		"200": {
			{GetchuID: "200", File: "c.jpg", SHA256: "aaa"},
			{GetchuID: "200", File: "d.jpg", SHA256: "bbb"},
			{GetchuID: "200", File: "e.jpg", SHA256: "ccc"},
		},
	}
	r := &runner{stats: &Stats{}, have: map[int64]map[string]bool{}}
	// Dry run: no uploads, but the decision is the same one apply makes.
	r.fill(t.Context(), "/nowhere", candidate{WorkID: 1, GetchuIDs: []string{"100", "200"}}, staged, false)

	assert.Equal(t, 2, r.stats.Dedup, "the DL edition's two repeats are skipped before any upload")
	// The three distinct images are all attempted; the mirror dir is empty in
	// this test, so they land in `missing` rather than `planned`.
	assert.Equal(t, 3, r.stats.Missing)
	assert.Equal(t, 0, r.stats.Planned)
}

// A file the mirror recorded no hash for is never skipped — the unique key is
// still the backstop, and dropping an image on a missing hash would lose it.
func TestFillKeepsImagesWithNoRecordedHash(t *testing.T) {
	staged := map[string][]stagedImage{
		"100": {
			{GetchuID: "100", File: "a.jpg"},
			{GetchuID: "100", File: "b.jpg"},
		},
	}
	r := &runner{stats: &Stats{}, have: map[int64]map[string]bool{}}
	r.fill(t.Context(), "/nowhere", candidate{WorkID: 1, GetchuIDs: []string{"100"}}, staged, false)
	assert.Equal(t, 0, r.stats.Dedup)
	assert.Equal(t, 2, r.stats.Missing)
}
