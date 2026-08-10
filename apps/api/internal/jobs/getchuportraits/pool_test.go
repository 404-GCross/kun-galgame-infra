package getchuportraits

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mkCands(t *testing.T, n int) (string, []candidate) {
	t.Helper()
	dir := t.TempDir()
	var out []candidate
	for i := 0; i < n; i++ {
		gid := fmt.Sprintf("%d", 1000+i)
		file := fmt.Sprintf("c%schara1.jpg", gid)
		if i%2 == 0 {
			require.NoError(t, os.MkdirAll(filepath.Join(dir, gid), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, gid, file), []byte("bytes"), 0o644))
		}
		out = append(out, candidate{CharacterID: int64(i + 1), GetchuID: gid, File: file})
	}
	return dir, out
}

func TestPoolWidthDoesNotChangeTheResult(t *testing.T) {
	dir, cands := mkCands(t, 60)
	var want Stats
	for _, workers := range []int{1, 2, 8, 64} {
		st := &Stats{}
		r := &runner{stats: st}
		r.run(context.Background(), Opts{MirrorDir: dir, Workers: workers}, cands)
		if workers == 1 {
			want = *st
			assert.Equal(t, 30, st.Missing, "half the fixtures have no bytes on disk")
			continue
		}
		assert.Equal(t, want, *st, "workers=%d disagreed with the serial run", workers)
	}
}

func TestAbsorbFoldsResultsAndCollectsHashes(t *testing.T) {
	st := &Stats{}
	r := &runner{stats: st}
	r.absorb(charResult{uploaded: 1, hash: "aaa"})
	r.absorb(charResult{missing: 1})
	r.absorb(charResult{rejected: 1, hash: "bbb"})
	r.absorb(charResult{errors: 1, quota: true})

	assert.Equal(t, 1, st.Uploaded)
	assert.Equal(t, 1, st.Missing)
	assert.Equal(t, 1, st.Rejected)
	assert.Equal(t, 1, st.Errors)
	assert.True(t, st.Quota)
	assert.Equal(t, []string{"aaa", "bbb"}, r.pingHashes)
}

func TestPoolStopsOnCancel(t *testing.T) {
	dir, cands := mkCands(t, 40)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	st := &Stats{}
	r := &runner{stats: st}
	r.run(ctx, Opts{MirrorDir: dir, Workers: 4}, cands)
	assert.Less(t, st.Missing, 20, "a cancelled run should not walk the whole list")
}
