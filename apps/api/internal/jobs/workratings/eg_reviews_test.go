package workratings

import (
	"context"
	"testing"

	"api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mkEGReview(t *testing.T, game int, tokuten *int) {
	t.Helper()
	require.NoError(t, testDB.Exec(
		`INSERT INTO workratings_eg.reviews (game, tokuten) VALUES (?, ?)`, game, tokuten).Error)
}

func TestEGReviewHistogram(t *testing.T) {
	clean(t)
	ctx := context.Background()
	reg, err := resolveRegistry(ctx, testDB)
	require.NoError(t, err)

	wScored := mkWork(t, reg.galgameMedium, "eg-histogram", nil)
	mkEGGame(t, 2001, p(78), 40)
	mkAnchor(t, wScored, "2001", reg.egSource, model.LinkKindExact, "rule:test")
	for _, tk := range []*int{p(0), p(5), p(9), p(10), p(19), p(78), p(95), p(100), nil, nil} {
		mkEGReview(t, 2001, tk)
	}

	wSilent := mkWork(t, reg.galgameMedium, "eg-no-reviews", nil)
	mkEGGame(t, 2002, p(60), 3)
	mkAnchor(t, wSilent, "2002", reg.egSource, model.LinkKindExact, "rule:test")
	mkEGReview(t, 2002, nil)

	st, err := Run(ctx, runOpts(true))
	require.NoError(t, err)
	assert.Equal(t, 2, st.EgPlanned)
	assert.Equal(t, 1, st.EgDistribution)
	assert.Equal(t, 1, st.EgNoReviews, "a game whose every review is comment-only stores no histogram")
	assert.Zero(t, st.Errors)

	var r model.CatalogWorkRating
	require.NoError(t, testDB.Where("work_id = ? AND source_id = ?", wScored, reg.egSource).First(&r).Error)
	assert.JSONEq(t, `{"0":3,"10":2,"70":1,"90":1,"100":1}`, string(r.Distribution),
		"scores fold to their decile floor; 0 and 100 are real buckets, not edges to be swallowed")
	assert.Equal(t, 40, r.VoteCount, "the bars sum to 8 and the vote_count stays 40 — different mirrors, different cursors")

	var rSilent model.CatalogWorkRating
	require.NoError(t, testDB.Where("work_id = ? AND source_id = ?", wSilent, reg.egSource).First(&rSilent).Error)
	assert.Nil(t, rSilent.Distribution)

	st, err = Run(ctx, runOpts(true))
	require.NoError(t, err)
	assert.Equal(t, 2, st.EgUnchanged, "second pass rewrites nothing")
	assert.Zero(t, st.EgWritten+st.Errors)
}
