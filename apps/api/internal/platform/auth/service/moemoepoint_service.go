package service

import (
	"context"
	stderrors "errors"

	"api/internal/platform/auth/model"
	"api/internal/platform/auth/repository"
	"api/pkg/errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MoemoepointService owns every change to a user's moemoepoint balance. It is
// the single mutation path (see docs/integration/oauth/06-moemoepoint.md):
// the mutable balance on users.moemoepoint and an append-only audit row in
// moemoepoint_log are written in ONE transaction, guarded by an idempotency
// key so retries / cron replays never double-apply.
//
// Balance may go negative (no non-negative enforcement) so reversals always
// succeed — for soft karma that's the lean, correct choice.
type MoemoepointService struct {
	db       *gorm.DB
	userRepo *repository.UserRepository
}

// NewMoemoepointService creates a MoemoepointService.
func NewMoemoepointService(db *gorm.DB, userRepo *repository.UserRepository) *MoemoepointService {
	return &MoemoepointService{db: db, userRepo: userRepo}
}

// AdjustParams is one moemoepoint change.
type AdjustParams struct {
	UserID         uint
	Delta          int
	Reason         string
	SourceApp      string
	Ref            string
	ActorUserID    uint
	IdempotencyKey string
	Note           string
}

// AdjustResult is what Adjust returns.
type AdjustResult struct {
	Balance int  `json:"balance"`
	Applied bool `json:"applied"` // false = idempotency-key hit, no-op
	LogID   int64 `json:"-"`
}

// maxAbsDelta caps a single adjustment. Generous for karma — its purpose is
// to stop a buggy downstream from setting an absurd balance in one call, not
// to model any real economy limit.
const maxAbsDelta = 1_000_000

// Adjust applies a signed delta to a user's balance, atomically writing the
// audit row. Idempotent on IdempotencyKey.
func (s *MoemoepointService) Adjust(ctx context.Context, p AdjustParams) (*AdjustResult, error) {
	if p.Delta == 0 || p.Delta > maxAbsDelta || p.Delta < -maxAbsDelta {
		return nil, errors.NewWithCode(errors.ErrMoemoepointInvalidDelta)
	}
	if !model.IsValidMoemoepointReason(p.Reason) {
		return nil, errors.NewWithCode(errors.ErrMoemoepointInvalidReason)
	}
	if p.IdempotencyKey == "" {
		return nil, errors.NewWithCode(errors.ErrMissingParam)
	}

	var result AdjustResult
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Idempotency short-circuit: same key already applied?
		var existing model.MoemoepointLog
		e := tx.Where("idempotency_key = ?", p.IdempotencyKey).First(&existing).Error
		if e == nil {
			// Same key MUST describe the same change, else it's a caller bug.
			if existing.UserID != p.UserID || existing.Delta != p.Delta || existing.Reason != p.Reason {
				return errors.NewWithCode(errors.ErrMoemoepointIdemConflict)
			}
			var u model.User
			if err := tx.Select("moemoepoint").First(&u, p.UserID).Error; err != nil {
				return mapUserErr(err)
			}
			result = AdjustResult{Balance: u.Moemoepoint, Applied: false, LogID: existing.ID}
			return nil
		}
		if !stderrors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}

		// Lock the user row so concurrent adjustments serialize.
		var u model.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&u, p.UserID).Error; err != nil {
			return mapUserErr(err)
		}
		newBalance := u.Moemoepoint + p.Delta

		log := model.MoemoepointLog{
			UserID:         p.UserID,
			Delta:          p.Delta,
			Reason:         p.Reason,
			SourceApp:      p.SourceApp,
			Ref:            p.Ref,
			ActorUserID:    p.ActorUserID,
			IdempotencyKey: p.IdempotencyKey,
			Note:           p.Note,
		}
		if err := tx.Create(&log).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.User{}).Where("id = ?", p.UserID).
			Update("moemoepoint", newBalance).Error; err != nil {
			return err
		}
		result = AdjustResult{Balance: newBalance, Applied: true, LogID: log.ID}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// GetBalance returns a user's current balance.
func (s *MoemoepointService) GetBalance(ctx context.Context, userID uint) (int, error) {
	var u model.User
	if err := s.db.WithContext(ctx).Select("moemoepoint").First(&u, userID).Error; err != nil {
		return 0, mapUserErr(err)
	}
	return u.Moemoepoint, nil
}

// GetLog returns a user's audit rows, newest first, keyset-paginated by id.
// beforeID == 0 means "from the latest". reason != "" filters to one reason.
func (s *MoemoepointService) GetLog(ctx context.Context, userID uint, limit int, beforeID int64, reason string) ([]model.MoemoepointLog, bool, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	q := s.db.WithContext(ctx).Where("user_id = ?", userID)
	if beforeID > 0 {
		q = q.Where("id < ?", beforeID)
	}
	if reason != "" {
		q = q.Where("reason = ?", reason)
	}
	var rows []model.MoemoepointLog
	// Fetch one extra to compute has_more.
	if err := q.Order("id DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return nil, false, err
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	return rows, hasMore, nil
}

// UserIDByUUID resolves a UUID to the numeric user id (admin endpoints take
// :uuid; the service works in numeric ids).
func (s *MoemoepointService) UserIDByUUID(ctx context.Context, uuid string) (uint, error) {
	u, err := s.userRepo.FindByUUID(ctx, uuid)
	if err != nil {
		return 0, errors.NewWithCode(errors.ErrAuthUserNotFound)
	}
	return u.ID, nil
}

func mapUserErr(err error) error {
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return errors.NewWithCode(errors.ErrAuthUserNotFound)
	}
	return err
}
