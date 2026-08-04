package getchumedia

import (
	"strconv"
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
	res := r.fill(t.Context(), "/nowhere", candidate{WorkID: 1, GetchuIDs: []string{"100", "200"}}, staged, false)

	assert.Equal(t, 2, res.dedup, "the DL edition's two repeats are skipped before any upload")
	// The three distinct images are all attempted; the mirror dir is empty in
	// this test, so they land in `missing` rather than `planned`.
	assert.Equal(t, 3, res.missing)
	assert.Equal(t, 0, res.planned)
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
	res := r.fill(t.Context(), "/nowhere", candidate{WorkID: 1, GetchuIDs: []string{"100"}}, staged, false)
	assert.Equal(t, 0, res.dedup)
	assert.Equal(t, 2, res.missing)
}

// The pool must produce the same totals as the serial path, and must not race.
// Run under -race: workers share the runner (its `have` map, the staged map,
// the image client) and only the driver goroutine may touch stats/touched.
func TestRunPoolMatchesSerialAndDoesNotRace(t *testing.T) {
	staged := map[string][]stagedImage{}
	cands := make([]candidate, 0, 50)
	for i := 1; i <= 50; i++ {
		gid := strconv.Itoa(i)
		staged[gid] = []stagedImage{
			{GetchuID: gid, File: "a.jpg", SHA256: "h" + gid + "a"},
			{GetchuID: gid, File: "b.jpg", SHA256: "h" + gid + "b"},
			{GetchuID: gid, File: "c.jpg", SHA256: "h" + gid + "a"}, // duplicate byte
		}
		cands = append(cands, candidate{WorkID: int64(i), GetchuIDs: []string{gid}})
	}

	run := func(workers int) Stats {
		r := &runner{stats: &Stats{Works: len(cands)}, have: map[int64]map[string]bool{}}
		// Dry mode: no image service, no DB — this exercises the pool, the
		// per-work dedup and the merge, which is where the shared state is.
		r.run(t.Context(), Opts{MirrorDir: "/nowhere", Workers: workers}, cands, staged)
		return *r.stats
	}

	serial, pooled := run(1), run(8)
	assert.Equal(t, serial, pooled, "the pool must decide exactly what the serial path decides")
	assert.Equal(t, 50, serial.Dedup, "one duplicate byte per work, caught before any upload")
	assert.Equal(t, 100, serial.Missing, "two distinct images per work, none on disk here")
}

// A work with nothing staged is counted once, whichever path ran it.
func TestRunPoolCountsNoStagedOnce(t *testing.T) {
	cands := []candidate{{WorkID: 1, GetchuIDs: []string{"absent"}}, {WorkID: 2, GetchuIDs: []string{"absent"}}}
	for _, workers := range []int{1, 4} {
		r := &runner{stats: &Stats{Works: len(cands)}, have: map[int64]map[string]bool{}}
		r.run(t.Context(), Opts{MirrorDir: "/nowhere", Workers: workers}, cands, map[string][]stagedImage{})
		assert.Equal(t, 2, r.stats.NoStaged, "workers=%d", workers)
	}
}
