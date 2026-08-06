package service

import (
	"context"
	"strings"
	"testing"

	"api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The vote service's own refusals and its batched tally (wave 175) — the
// invariants that hold whether or not an HTTP schema gate runs in front.

func seedVoteWork(t *testing.T) (workID, coverID int64) {
	t.Helper()
	for _, tbl := range []string{"catalog_cover_vote", "catalog_work_cover", "catalog_work"} {
		require.NoError(t, testDB.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	w := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "投票サービス", Status: model.WorkStatusLive}
	require.NoError(t, testDB.Create(&w).Error)
	c := model.CatalogWorkCover{WorkID: w.ID, ImageHash: strings.Repeat("e", 64), SourceID: 2}
	require.NoError(t, testDB.Create(&c).Error)
	return w.ID, c.ID
}

// TestCoverVote_ActorRequired: a vote is somebody's taste — the uid 0 the claim
// log accepts as "the system did it" has no meaning here, on either op.
func TestCoverVote_ActorRequired(t *testing.T) {
	workID, coverID := seedVoteWork(t)
	svc := NewCoverVoteService(testDB)
	ctx := context.Background()

	for _, uid := range []int64{0, -1} {
		_, err := svc.Vote(ctx, CoverVoteParams{WorkID: workID, CoverID: coverID, ActorUID: uid, Site: "kungal"})
		assert.ErrorIsf(t, err, ErrVoteActorRequired, "uid %d must not cast a ballot", uid)
		assert.ErrorIs(t, svc.Unvote(ctx, workID, uid), ErrVoteActorRequired)
	}

	var rows int64
	require.NoError(t, testDB.Raw(`SELECT count(*) FROM catalog_cover_vote`).Scan(&rows).Error)
	assert.EqualValues(t, 0, rows)
}

// TestCoverVote_TallyIsBatchedAndViewerScoped: one query answers the page, the
// count is everybody's and the flag is only the asker's.
func TestCoverVote_TallyIsBatchedAndViewerScoped(t *testing.T) {
	workID, coverID := seedVoteWork(t)
	second := model.CatalogWorkCover{WorkID: workID, ImageHash: strings.Repeat("f", 64), SortOrder: 1, SourceID: 2}
	require.NoError(t, testDB.Create(&second).Error)

	svc := NewCoverVoteService(testDB)
	read := NewReadService(testDB)
	ctx := context.Background()
	for _, uid := range []int64{11, 12} {
		count, err := svc.Vote(ctx, CoverVoteParams{WorkID: workID, CoverID: coverID, ActorUID: uid, Site: "kungal"})
		require.NoError(t, err)
		assert.EqualValues(t, uid-10, count, "the write answers with the cover's running total")
	}

	tally, err := read.CoverVotes(ctx, []int64{coverID, second.ID}, 12)
	require.NoError(t, err)
	assert.Equal(t, CoverVoteTally{Count: 2, Voted: true}, tally[coverID])
	assert.NotContains(t, tally, second.ID, "an unvoted cover is absent; the caller renders zero")

	anon, err := read.CoverVotes(ctx, []int64{coverID}, 0)
	require.NoError(t, err)
	assert.Equal(t, CoverVoteTally{Count: 2, Voted: false}, anon[coverID],
		"viewer 0 is nobody asking — the count still answers, the flag does not")

	empty, err := read.CoverVotes(ctx, nil, 12)
	require.NoError(t, err)
	assert.Empty(t, empty, "no covers, no query")
}
