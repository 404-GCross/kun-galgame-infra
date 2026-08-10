package getchumedia

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFillSkipsBytesAlreadySentForTheSameWork(t *testing.T) {
	staged := map[string][]stagedImage{
		"100": {
			{GetchuID: "100", File: "a.jpg", SHA256: "aaa"},
			{GetchuID: "100", File: "b.jpg", SHA256: "bbb"},
		},
		"200": {
			{GetchuID: "200", File: "c.jpg", SHA256: "aaa"},
			{GetchuID: "200", File: "d.jpg", SHA256: "bbb"},
			{GetchuID: "200", File: "e.jpg", SHA256: "ccc"},
		},
	}
	r := &runner{stats: &Stats{}, have: map[int64]map[string]bool{}}
	res := r.fill(t.Context(), "/nowhere", candidate{WorkID: 1, GetchuIDs: []string{"100", "200"}}, nil, staged, false)

	assert.Equal(t, 2, res.dedup, "the DL edition's two repeats are skipped before any upload")
	assert.Equal(t, 3, res.missing)
	assert.Equal(t, 0, res.planned)
}

func TestFillKeepsImagesWithNoRecordedHash(t *testing.T) {
	staged := map[string][]stagedImage{
		"100": {
			{GetchuID: "100", File: "a.jpg"},
			{GetchuID: "100", File: "b.jpg"},
		},
	}
	r := &runner{stats: &Stats{}, have: map[int64]map[string]bool{}}
	res := r.fill(t.Context(), "/nowhere", candidate{WorkID: 1, GetchuIDs: []string{"100"}}, nil, staged, false)
	assert.Equal(t, 0, res.dedup)
	assert.Equal(t, 2, res.missing)
}

func TestRunPoolMatchesSerialAndDoesNotRace(t *testing.T) {
	staged := map[string][]stagedImage{}
	cands := make([]candidate, 0, 50)
	for i := 1; i <= 50; i++ {
		gid := strconv.Itoa(i)
		staged[gid] = []stagedImage{
			{GetchuID: gid, File: "a.jpg", SHA256: "h" + gid + "a"},
			{GetchuID: gid, File: "b.jpg", SHA256: "h" + gid + "b"},
			{GetchuID: gid, File: "c.jpg", SHA256: "h" + gid + "a"},
		}
		cands = append(cands, candidate{WorkID: int64(i), GetchuIDs: []string{gid}})
	}

	run := func(workers int) Stats {
		r := &runner{stats: &Stats{Works: len(cands)}, have: map[int64]map[string]bool{}}
		r.run(t.Context(), Opts{MirrorDir: "/nowhere", Workers: workers}, cands, staged)
		return *r.stats
	}

	serial, pooled := run(1), run(8)
	assert.Equal(t, serial, pooled, "the pool must decide exactly what the serial path decides")
	assert.Equal(t, 50, serial.Dedup, "one duplicate byte per work, caught before any upload")
	assert.Equal(t, 100, serial.Missing, "two distinct images per work, none on disk here")
}

func TestRunPoolCountsNoStagedOnce(t *testing.T) {
	cands := []candidate{{WorkID: 1, GetchuIDs: []string{"absent"}}, {WorkID: 2, GetchuIDs: []string{"absent"}}}
	for _, workers := range []int{1, 4} {
		r := &runner{stats: &Stats{Works: len(cands)}, have: map[int64]map[string]bool{}}
		r.run(t.Context(), Opts{MirrorDir: "/nowhere", Workers: workers}, cands, map[string][]stagedImage{})
		assert.Equal(t, 2, r.stats.NoStaged, "workers=%d", workers)
	}
}

func TestAbsorbLeavesHaveUntouched(t *testing.T) {
	r := &runner{stats: &Stats{}, have: map[int64]map[string]bool{7: {"old": true}}}
	r.absorb(7, workResult{uploaded: 2, hashes: []string{"new1", "new2"}, touched: true})
	r.absorb(9, workResult{uploaded: 1, hashes: []string{"other"}, touched: true})

	assert.Equal(t, map[int64]map[string]bool{7: {"old": true}}, r.have,
		"no key added, no set grown — workers range over this map")
	assert.Equal(t, 3, r.stats.Uploaded)
	assert.Equal(t, []string{"new1", "new2", "other"}, r.pingHashes)
	assert.Equal(t, []int64{7, 9}, r.touched)
}

func TestRunPoolReadsExistingHashes(t *testing.T) {
	staged := map[string][]stagedImage{"1": {{GetchuID: "1", File: "a.jpg", SHA256: "x"}}}
	cands := []candidate{{WorkID: 1, GetchuIDs: []string{"1"}}}
	for _, workers := range []int{1, 4} {
		r := &runner{stats: &Stats{}, have: map[int64]map[string]bool{1: {"already": true}}}
		r.run(t.Context(), Opts{MirrorDir: "/nowhere", Workers: workers}, cands, staged)
		assert.Equal(t, 1, r.stats.Missing, "workers=%d", workers)
		assert.Len(t, r.have[1], 1, "the seed set is not grown by the run (workers=%d)", workers)
	}
}
