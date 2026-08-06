// cover_vote.go — the best-cover vote (wave 175): the write half (one ballot
// per user per work, moved rather than accumulated) and the read half (the
// batched tally the S2S work detail decorates its cover rows with).
//
// The votes are ADVISORY. Nothing here touches catalog_work_cover: not
// sort_order, not portrait_pinned, not the content flags. An editor's pin
// outranks a popularity contest by construction — because the contest cannot
// write the pin — and every consumer decides on its own face whether the counts
// reorder anything. NSFW trimming likewise stays where it already is, on the
// read side's per-image sexual/violence columns; the write path has no opinion
// about a cover's rating, only about whether the cover belongs to the work.
package service

import (
	"context"
	stderrors "errors"
	"time"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The refusals of the vote face.
var (
	// ErrVoteActorRequired: a vote is somebody's taste, so it is never
	// system-attributed — uid 0 (the claim log's "a projector did it") has no
	// meaning here.
	ErrVoteActorRequired = stderrors.New("catalog: a cover vote requires a voting user")
	// ErrVoteWorkUnavailable: the work is gone, soft-deleted, or not live
	// (a merged-away id is the common case — vote on the survivor instead).
	ErrVoteWorkUnavailable = stderrors.New("catalog: work not available for voting")
	// ErrVoteCoverNotOnWork: the cover exists elsewhere or not at all. Both
	// collapse into one refusal deliberately — the caller's next move is the
	// same either way (re-read the work's covers).
	ErrVoteCoverNotOnWork = stderrors.New("catalog: cover does not belong to this work")
)

// CoverVoteService owns the vote write path.
type CoverVoteService struct{ db *gorm.DB }

func NewCoverVoteService(db *gorm.DB) *CoverVoteService { return &CoverVoteService{db: db} }

// CoverVoteParams is one ballot.
type CoverVoteParams struct {
	WorkID   int64
	CoverID  int64
	ActorUID int64
	// Site is the product site the vote arrived from (provenance only — the
	// ballot is one person's across every site).
	Site string
}

// Vote records the actor's choice of best cover for the work and returns the
// voted cover's new total.
//
// Re-voting MOVES the ballot: the (work_id, actor_uid) unique key is the
// conflict target, so a second call updates the row's cover instead of adding a
// second row. That is the whole reason the key excludes cover_id — a per-cover
// key would let one user stuff every cover on the work.
func (s *CoverVoteService) Vote(ctx context.Context, p CoverVoteParams) (int64, error) {
	if p.ActorUID <= 0 {
		return 0, ErrVoteActorRequired
	}
	var count int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := assertVotableWork(tx, p.WorkID); err != nil {
			return err
		}
		if err := assertCoverOnWork(tx, p.WorkID, p.CoverID); err != nil {
			return err
		}
		vote := model.CatalogCoverVote{
			WorkID: p.WorkID, CoverID: p.CoverID, ActorUID: p.ActorUID, Site: p.Site,
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "work_id"}, {Name: "actor_uid"}},
			DoUpdates: clause.Assignments(map[string]any{"cover_id": p.CoverID, "site": p.Site, "updated_at": time.Now()}),
		}).Create(&vote).Error; err != nil {
			return err
		}
		return tx.Model(&model.CatalogCoverVote{}).Where("cover_id = ?", p.CoverID).Count(&count).Error
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

// Unvote withdraws the actor's ballot on the work. Idempotent: withdrawing a
// ballot nobody cast is a success, because the caller's intent ("this user has
// no vote here") is satisfied either way — a 404 would only invite a client to
// re-read state it already knows.
//
// It deliberately does NOT check the work's availability: a work that stopped
// being live must not trap the votes it collected while it was.
func (s *CoverVoteService) Unvote(ctx context.Context, workID, actorUID int64) error {
	if actorUID <= 0 {
		return ErrVoteActorRequired
	}
	return s.db.WithContext(ctx).
		Where("work_id = ? AND actor_uid = ?", workID, actorUID).
		Delete(&model.CatalogCoverVote{}).Error
}

// CountFor is one cover's advisory total — what a withdrawal answers with, so
// the caller re-renders without a second read.
func (s *CoverVoteService) CountFor(ctx context.Context, coverID int64) (int64, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&model.CatalogCoverVote{}).
		Where("cover_id = ?", coverID).Count(&count).Error
	return count, err
}

// assertVotableWork refuses a missing, soft-deleted or non-live work. Status is
// checked (not claim_state): a merged-away row still exists and still carries
// covers, and a ballot cast on it would be orphaned by the redirect.
func assertVotableWork(tx *gorm.DB, workID int64) error {
	var work model.CatalogWork
	err := tx.Select("status").Where("id = ?", workID).Take(&work).Error
	switch {
	// Take carries GORM's soft-delete scope, so a deleted work is a
	// not-found — the same refusal as a work that never existed.
	case stderrors.Is(err, gorm.ErrRecordNotFound):
		return ErrVoteWorkUnavailable
	case err != nil:
		return err
	case work.Status != model.WorkStatusLive:
		return ErrVoteWorkUnavailable
	}
	return nil
}

// assertCoverOnWork refuses a cover that is not this work's.
func assertCoverOnWork(tx *gorm.DB, workID, coverID int64) error {
	var exists bool
	if err := tx.Raw(
		`SELECT EXISTS (SELECT 1 FROM catalog_work_cover WHERE id = ? AND work_id = ?)`,
		coverID, workID,
	).Scan(&exists).Error; err != nil {
		return err
	}
	if !exists {
		return ErrVoteCoverNotOnWork
	}
	return nil
}

// CoverVoteTally is the advisory projection of one cover's votes: the total, and
// whether the asking viewer is one of them.
type CoverVoteTally struct {
	Count int
	Voted bool
}

// CoverVotes tallies a page of covers in ONE query (§9.1 — never per cover).
//
// viewerUID <= 0 means "nobody is asking on their own behalf": the FILTER then
// matches nothing (uid 0 is never stored), so `voted` comes back false without a
// second branch or a second round-trip. Covers with no vote are absent from the
// map; the caller renders them as zero.
func (s *ReadService) CoverVotes(ctx context.Context, coverIDs []int64, viewerUID int64) (map[int64]CoverVoteTally, error) {
	out := make(map[int64]CoverVoteTally, len(coverIDs))
	if len(coverIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		CoverID int64 `gorm:"column:cover_id"`
		Votes   int   `gorm:"column:votes"`
		Voted   bool  `gorm:"column:voted"`
	}
	if err := s.db.WithContext(ctx).Raw(`
		SELECT cover_id,
		       COUNT(*) AS votes,
		       COUNT(*) FILTER (WHERE actor_uid = ?) > 0 AS voted
		  FROM catalog_cover_vote
		 WHERE cover_id IN ?
		 GROUP BY cover_id`, viewerUID, coverIDs).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.CoverID] = CoverVoteTally{Count: r.Votes, Voted: r.Voted}
	}
	return out, nil
}
