package srcvndb

import (
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var votesFixture = []string{
	"1 2 30 2007-09-22",
	"97 5 10 2008-03-24",
	"1 13 100 2007-10-20",
	"97 6 55 2011-01-01",
	"1 14 99 2010-01-01",
	"2 1 120 2020-01-01",
	"",
	"garbage",
	"v3 1 50 2020-01-01",
	"5 1 notanumber 2020-01-01",
	"1 15 5 2020-01-01",
}

func TestVotesAggregator(t *testing.T) {
	agg := newVotesAgg()
	for _, line := range votesFixture {
		agg.addLine(line)
	}

	assert.EqualValues(t, 6, agg.counted)
	assert.EqualValues(t, 4, agg.skipped, "blank lines are ignored, malformed ones are counted")

	rows := agg.rows()
	require.Len(t, rows, 3)
	assert.Equal(t, []string{"v1", "v2", "v97"}, []string{rows[0].ID, rows[1].ID, rows[2].ID},
		"rows come out in numeric vn order, not map order")

	assert.JSONEq(t, `{"3":1,"9":1,"10":1}`, string(rows[0].Distribution))
	assert.Equal(t, 3, rows[0].Total, "the 5-point vote is below VNDB's 10-100 scale and never lands")
	assert.JSONEq(t, `{"10":1}`, string(rows[1].Distribution), "a vote past 100 clamps into the top bucket")
	assert.JSONEq(t, `{"1":1,"5":1}`, string(rows[2].Distribution))
	assert.Equal(t, 2, rows[2].Total)
}

func writeVotesGz(t *testing.T, lines []string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "votes.gz")
	f, err := os.Create(path)
	require.NoError(t, err)
	gz := gzip.NewWriter(f)
	_, err = gz.Write([]byte(strings.Join(lines, "\n") + "\n"))
	require.NoError(t, err)
	require.NoError(t, gz.Close())
	require.NoError(t, f.Close())
	return path
}

func TestRunVotes(t *testing.T) {
	report, err := RunVotes(testDB, writeVotesGz(t, votesFixture))
	require.NoError(t, err)
	assert.Equal(t, 3, report.VNs)
	assert.EqualValues(t, 6, report.Votes)
	assert.EqualValues(t, 4, report.Skipped)

	var rows []VNVoteStats
	require.NoError(t, testDB.Order("total DESC, id").Find(&rows).Error)
	require.Len(t, rows, 3)
	assert.Equal(t, "v1", rows[0].ID)
	assert.Equal(t, 3, rows[0].Total)

	report, err = RunVotes(testDB, writeVotesGz(t, []string{"97 5 80 2020-01-01"}))
	require.NoError(t, err)
	assert.Equal(t, 1, report.VNs)
	var n int64
	require.NoError(t, testDB.Model(&VNVoteStats{}).Count(&n).Error)
	assert.EqualValues(t, 1, n, "whole-table replacement — the previous staging is gone, not merged")

	_, err = RunVotes(testDB, writeVotesGz(t, []string{"garbage", "also garbage"}))
	require.Error(t, err, "a dump that parses to nothing must fail loudly, not empty the table")

	require.NoError(t, testDB.Exec(`TRUNCATE src_vndb.vn_vote_stats`).Error)
}
