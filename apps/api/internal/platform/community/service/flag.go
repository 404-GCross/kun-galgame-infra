package service

import (
	"context"

	"api/internal/platform/community/model"
	"api/internal/platform/community/repository"

	"gorm.io/gorm"
)

// Reputation-weighted reporting (doc 11 §6 layer 4). A report's weight is the
// reporter's TL base value scaled by their historical accuracy; a post whose
// accumulated PENDING weight crosses the threshold auto-hides and enters the
// review queue. A TL3+ reporter against a TL0 author hides on a single vote.
const (
	// flagHideThreshold ≈ "3 ordinary reports" (a fresh TL0 reporter weighs 1.0).
	flagHideThreshold float32 = 3.0
)

// flagBaseByLevel is the per-TL base report weight (index = trust level). TL3's
// base (2.0) is deliberately below the threshold so a single TL3 report does not
// auto-hide on weight alone — the TL3-vs-TL0 single-vote rule is the ONLY
// single-report hide path, and it is scoped to TL0 authors.
var flagBaseByLevel = [...]float32{1.0, 1.2, 1.5, 2.0, 2.5}

// FlagService owns report submission and the auto-hide/enqueue decision. Queue
// adjudication (approve/reject + accuracy backfill) is ReviewService.
type FlagService struct {
	db   *gorm.DB
	sink EventSink
}

func NewFlagService(db *gorm.DB, sink EventSink) *FlagService {
	return &FlagService{db: db, sink: sink}
}

// Submit records a weighted report and, when the post is still visible, applies
// the auto-hide decision (threshold OR the TL3-vs-TL0 single vote). Idempotent
// per (post, flagger). Reporting an already-hidden/tombstoned post only records
// the flag — it is never re-enqueued.
func (s *FlagService) Submit(ctx context.Context, postID, flaggerID int64, reason *int16, note *string) error {
	var crossed bool
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		pc, found, err := repository.PostContextTx(tx, postID)
		if err != nil {
			return err
		}
		if !found {
			return ErrPostNotFound
		}
		reporter, err := repository.GetOrCreateTrustTx(tx, flaggerID)
		if err != nil {
			return err
		}
		weight := flagBase(reporter.Level) * accuracyFactor(reporter)
		inserted, err := repository.InsertFlagTx(tx, &model.CommunityFlag{
			PostID: postID, FlaggerID: flaggerID, Reason: reason, Note: note,
			Weight: weight, Status: model.FlagStatusPending,
		})
		if err != nil {
			return err
		}
		if !inserted || pc.Status != model.PostStatusVisible {
			return nil // already flagged, or post not visible → record only
		}

		hide, err := s.shouldHide(tx, reporter, pc.AuthorID, postID)
		if err != nil {
			return err
		}
		if !hide {
			return nil
		}
		if err := repository.SetPostStatusTx(tx, postID, model.PostStatusHidden); err != nil {
			return err
		}
		if _, err := repository.EnqueueReviewIfAbsentTx(tx, pc.Site, postID, model.ReviewSourceFlags); err != nil {
			return err
		}
		crossed = true
		return nil
	})
	if err != nil {
		return err
	}
	if crossed {
		s.sink.Emit(Event{Kind: EventFlagThreshold, PostID: postID})
	}
	return nil
}

// shouldHide decides whether this report tips the post into hidden: a TL3+
// reporter against a TL0 author (single vote), or the accumulated pending weight
// reaching the threshold.
func (s *FlagService) shouldHide(tx *gorm.DB, reporter *model.CommunityTrust, authorID, postID int64) (bool, error) {
	if reporter.Level >= model.TrustLevelRegular {
		authorLevel := model.TrustLevelNew
		if at, err := repository.GetTrustTx(tx, authorID); err != nil {
			return false, err
		} else if at != nil {
			authorLevel = at.Level
		}
		if authorLevel == model.TrustLevelNew {
			return true, nil
		}
	}
	sum, err := repository.SumPendingFlagWeightTx(tx, postID)
	if err != nil {
		return false, err
	}
	return sum >= flagHideThreshold, nil
}

func flagBase(level int16) float32 {
	if int(level) >= 0 && int(level) < len(flagBaseByLevel) {
		return flagBaseByLevel[level]
	}
	return flagBaseByLevel[len(flagBaseByLevel)-1]
}

// accuracyFactor is agreed/(agreed+disagreed); a reporter with no decided
// history factors to 1.0 (doc 11: initial 1). A consistently-wrong reporter
// tends to 0, neutralizing their influence.
func accuracyFactor(t *model.CommunityTrust) float32 {
	agreed, disagreed := nz(t.FlagsAgreed), nz(t.FlagsDisagreed)
	denom := agreed + disagreed
	if denom == 0 {
		return 1.0
	}
	return float32(agreed) / float32(denom)
}
