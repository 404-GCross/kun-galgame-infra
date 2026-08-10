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

type MoemoepointService struct {
	db       *gorm.DB
	userRepo *repository.UserRepository
}

func NewMoemoepointService(db *gorm.DB, userRepo *repository.UserRepository) *MoemoepointService {
	return &MoemoepointService{db: db, userRepo: userRepo}
}

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

type AdjustResult struct {
	Balance int  `json:"balance"`
	Applied bool `json:"applied"`
	LogID   int64 `json:"-"`
}

const maxAbsDelta = 1_000_000

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
		var existing model.MoemoepointLog
		e := tx.Where("idempotency_key = ?", p.IdempotencyKey).First(&existing).Error
		if e == nil {
			if !sameAdjust(&existing, p) {
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
		res := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "idempotency_key"}}, DoNothing: true,
		}).Create(&log)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			var winner model.MoemoepointLog
			if err := tx.Where("idempotency_key = ?", p.IdempotencyKey).First(&winner).Error; err != nil {
				return err
			}
			if !sameAdjust(&winner, p) {
				return errors.NewWithCode(errors.ErrMoemoepointIdemConflict)
			}
			result = AdjustResult{Balance: u.Moemoepoint, Applied: false, LogID: winner.ID}
			return nil
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

func (s *MoemoepointService) GetBalance(ctx context.Context, userID uint) (int, error) {
	var u model.User
	if err := s.db.WithContext(ctx).Select("moemoepoint").First(&u, userID).Error; err != nil {
		return 0, mapUserErr(err)
	}
	return u.Moemoepoint, nil
}

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
	if err := q.Order("id DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return nil, false, err
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	return rows, hasMore, nil
}

func (s *MoemoepointService) SourceNames(ctx context.Context, sourceApps []string) map[string]string {
	seen := make(map[string]struct{}, len(sourceApps))
	ids := make([]string, 0, len(sourceApps))
	for _, a := range sourceApps {
		if a == "" || a == "oauth" {
			continue
		}
		if _, ok := seen[a]; ok {
			continue
		}
		seen[a] = struct{}{}
		ids = append(ids, a)
	}
	out := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return out
	}
	var rows []struct {
		ID   string
		Name string
	}
	if err := s.db.WithContext(ctx).Table("oauth_clients").
		Select("id, name").Where("id IN ?", ids).Scan(&rows).Error; err != nil {
		return out
	}
	for _, r := range rows {
		out[r.ID] = r.Name
	}
	return out
}

func (s *MoemoepointService) UserIDByUUID(ctx context.Context, uuid string) (uint, error) {
	u, err := s.userRepo.FindByUUID(ctx, uuid)
	if err != nil {
		return 0, errors.NewWithCode(errors.ErrAuthUserNotFound)
	}
	return u.ID, nil
}

func sameAdjust(existing *model.MoemoepointLog, p AdjustParams) bool {
	return existing.UserID == p.UserID &&
		existing.Delta == p.Delta &&
		existing.Reason == p.Reason &&
		existing.Ref == p.Ref &&
		existing.SourceApp == p.SourceApp &&
		existing.ActorUserID == p.ActorUserID &&
		existing.Note == p.Note
}

func mapUserErr(err error) error {
	if stderrors.Is(err, gorm.ErrRecordNotFound) {
		return errors.NewWithCode(errors.ErrAuthUserNotFound)
	}
	return err
}
