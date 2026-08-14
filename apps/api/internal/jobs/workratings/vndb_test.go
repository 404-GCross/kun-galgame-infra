package workratings

import (
	"context"
	"testing"
	"time"

	"api/internal/platform/catalog/model"
	srcv "api/internal/platform/catalog/srcvndb"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

// rating/average arrive on the dump's ×100 scale (787 = 7.87), the same scale
// src_vndb.vn stages them on.
func mkVN(t *testing.T, id string, rating, average *float64, votes int) {
	t.Helper()
	require.NoError(t, testDB.Create(&srcv.VN{
		ID: id, OLang: "ja", CVotecount: votes,
		CRating: rating, CAverage: average, IngestedAt: time.Now(),
	}).Error)
}

func mkVNVotes(t *testing.T, id, dist string, total int) {
	t.Helper()
	require.NoError(t, testDB.Create(&srcv.VNVoteStats{
		ID: id, Distribution: datatypes.JSON(dist), Total: total,
	}).Error)
}

func TestVndbLane(t *testing.T) {
	clean(t)
	ctx := context.Background()
	reg, err := resolveRegistry(ctx, testDB)
	require.NoError(t, err)

	wFull := mkWork(t, reg.galgameMedium, "vndb-full", nil)
	mkVN(t, "v97", pf(787), pf(790), 23633)
	mkVNVotes(t, "v97", `{"7":10,"8":20,"10":5}`, 35)
	mkAnchor(t, wFull, "v97", reg.vndbSource, model.LinkKindExact, "rule:test")

	wNoVotes := mkWork(t, reg.galgameMedium, "vndb-no-votes", nil)
	mkVN(t, "v11", pf(862), nil, 19037)
	mkAnchor(t, wNoVotes, "v11", reg.vndbSource, model.LinkKindExact, "rule:test")

	wMissing := mkWork(t, reg.galgameMedium, "vndb-missing", nil)
	mkAnchor(t, wMissing, "v999", reg.vndbSource, model.LinkKindExact, "rule:test")

	wUnrated := mkWork(t, reg.galgameMedium, "vndb-unrated", nil)
	mkVN(t, "v500", nil, nil, 0)
	mkAnchor(t, wUnrated, "v500", reg.vndbSource, model.LinkKindExact, "rule:test")

	wZeroVotes := mkWork(t, reg.galgameMedium, "vndb-zero-votes", nil)
	mkVN(t, "v501", pf(700), pf(700), 0)
	mkAnchor(t, wZeroVotes, "v501", reg.vndbSource, model.LinkKindExact, "rule:test")

	wMulti := mkWork(t, reg.galgameMedium, "vndb-multianchor", nil)
	mkVN(t, "v600", pf(500), pf(500), 10)
	mkVN(t, "v601", pf(900), pf(900), 99)
	mkAnchor(t, wMulti, "v600", reg.vndbSource, model.LinkKindExact, "rule:test")
	mkAnchor(t, wMulti, "v601", reg.vndbSource, model.LinkKindExact, "rule:test")

	wProbable := mkWork(t, reg.galgameMedium, "vndb-probable", nil)
	mkVN(t, "v700", pf(800), pf(800), 50)
	mkAnchor(t, wProbable, "v700", reg.vndbSource, model.LinkKindProbable, "rule:test")

	st, err := Run(ctx, runOpts(true))
	require.NoError(t, err)
	assert.Equal(t, 6, st.VndbCandidates, "probable anchors stay out of the lane")
	assert.Equal(t, 1, st.VndbMultiAnchor)
	assert.Equal(t, 1, st.VndbMissingMirror)
	assert.Equal(t, 2, st.VndbNoScore, "a null rating and a zero-vote row both skip")
	assert.Equal(t, 3, st.VndbPlanned)
	assert.Equal(t, 3, st.VndbWritten)
	assert.Equal(t, 1, st.VndbDistribution)
	assert.Equal(t, 2, st.VndbNoVotes, "v11 and v601 have no staged vote stats")
	assert.Equal(t, 2, st.VndbStats, "v11 publishes no c_average, so it gets no stats blob")
	assert.Zero(t, st.Errors)
	assert.EqualValues(t, 0, ratingCount(t, "WHERE work_id = ?", wProbable))

	var r model.CatalogWorkRating
	require.NoError(t, testDB.Where("work_id = ? AND source_id = ?", wFull, reg.vndbSource).First(&r).Error)
	assert.InDelta(t, 7.87, r.Score, 1e-9, "score is c_rating, VNDB's headline number")
	assert.Equal(t, 23633, r.VoteCount)
	assert.Nil(t, r.Rank)
	assert.JSONEq(t, `{"7":10,"8":20,"10":5}`, string(r.Distribution))
	assert.JSONEq(t, `{"average":7.9}`, string(r.Stats), "c_average travels beside c_rating, not instead of it")

	var rNoVotes model.CatalogWorkRating
	require.NoError(t, testDB.Where("work_id = ?", wNoVotes).First(&rNoVotes).Error)
	assert.Nil(t, rNoVotes.Distribution, "a vn absent from the votes dump stores NULL, not an empty object")
	assert.Nil(t, rNoVotes.Stats)

	var rMulti model.CatalogWorkRating
	require.NoError(t, testDB.Where("work_id = ?", wMulti).First(&rMulti).Error)
	assert.InDelta(t, 9.0, rMulti.Score, 1e-9, "the better-voted anchor wins")
	assert.Equal(t, 99, rMulti.VoteCount)

	st, err = Run(ctx, runOpts(true))
	require.NoError(t, err)
	assert.Equal(t, 3, st.VndbUnchanged, "second pass rewrites nothing")
	assert.Zero(t, st.VndbWritten+st.Errors)

	require.NoError(t, testDB.Exec(`UPDATE src_vndb.vn SET c_rating = 800 WHERE id = 'v97'`).Error)
	st, err = Run(ctx, runOpts(true))
	require.NoError(t, err)
	assert.Equal(t, 1, st.VndbWritten, "a moved rating updates in place")
	assert.EqualValues(t, 3, ratingCount(t, ""), "no row growth")
}

func TestVndbLaneWithoutStagedVotes(t *testing.T) {
	clean(t)
	ctx := context.Background()
	reg, err := resolveRegistry(ctx, testDB)
	require.NoError(t, err)

	w := mkWork(t, reg.galgameMedium, "vndb-votes-unstaged", nil)
	mkVN(t, "v97", pf(787), pf(787), 23633)
	mkAnchor(t, w, "v97", reg.vndbSource, model.LinkKindExact, "rule:test")

	st, err := Run(ctx, runOpts(true))
	require.NoError(t, err)
	assert.Equal(t, 1, st.VndbPlanned, "an empty vn_vote_stats still refreshes the score")
	assert.Equal(t, 1, st.VndbWritten)
	assert.Equal(t, 0, st.VndbDistribution)
	assert.Equal(t, 1, st.VndbNoVotes)
	assert.Zero(t, st.Errors)

	var r model.CatalogWorkRating
	require.NoError(t, testDB.Where("work_id = ?", w).First(&r).Error)
	assert.InDelta(t, 7.87, r.Score, 1e-9)
	assert.Nil(t, r.Distribution)
}
