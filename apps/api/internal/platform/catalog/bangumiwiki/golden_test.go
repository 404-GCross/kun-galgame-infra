package bangumiwiki

import (
	"bufio"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type goldenRow struct {
	File   string   `json:"file"`
	ID     int64    `json:"id"`
	Input  string   `json:"input"`
	OK     bool     `json:"ok"`
	Output *Infobox `json:"output,omitempty"`
}

func TestGoldenDumpCases(t *testing.T) {
	f, err := os.Open("testdata/golden/golden.jsonl")
	require.NoError(t, err)
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 32*1024*1024)
	var total, okCases, errCases int
	for sc.Scan() {
		var g goldenRow
		require.NoError(t, json.Unmarshal(sc.Bytes(), &g))
		total++

		got, err := Parse(g.Input)
		if !g.OK {
			errCases++
			assert.Errorf(t, err, "%s id=%d must fail to parse", g.File, g.ID)
			continue
		}
		okCases++
		require.NoErrorf(t, err, "%s id=%d must parse", g.File, g.ID)
		require.NotNil(t, g.Output)
		assert.Equalf(t, *g.Output, got, "%s id=%d output drifted from the reference", g.File, g.ID)
	}
	require.NoError(t, sc.Err())
	assert.Greater(t, total, 200, "golden corpus unexpectedly small")
	assert.Positive(t, errCases)
	assert.Positive(t, okCases)
}
