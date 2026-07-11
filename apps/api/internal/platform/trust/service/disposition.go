package service

import (
	"context"
	"errors"

	"api/internal/platform/trust/model"

	"gorm.io/gorm"
)

// DispositionService owns the dead-letter operator surface: listing callbacks by
// status and replaying a dead-lettered one (章程 ruling 9, invariant 13).
type DispositionService struct{ db *gorm.DB }

func NewDispositionService(db *gorm.DB) *DispositionService { return &DispositionService{db: db} }

// List returns dispositions filtered by callback_status (nil = all), newest
// first, with the total for pagination.
func (s *DispositionService) List(ctx context.Context, callbackStatus *int16, page, limit int) ([]model.TrustDisposition, int64, error) {
	q := s.db.WithContext(ctx).Model(&model.TrustDisposition{})
	if callbackStatus != nil {
		q = q.Where("callback_status = ?", *callbackStatus)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	l := clampLimit(limit)
	offset := 0
	if page > 1 {
		offset = (page - 1) * l
	}
	var items []model.TrustDisposition
	if err := q.Order("id DESC").Limit(l).Offset(offset).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// Redeliver resets a dead-lettered disposition to pending (attempts=0, due now)
// so the dispatch worker retries it. Only dead_letter rows are eligible.
func (s *DispositionService) Redeliver(ctx context.Context, actorID, id int64) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var disp model.TrustDisposition
		err := tx.Take(&disp, id).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrDispositionNotFound
		}
		if err != nil {
			return err
		}
		if disp.CallbackStatus == nil || *disp.CallbackStatus != model.CallbackStatusDeadLetter {
			return ErrNotDeadLetter
		}
		if err := tx.Model(&model.TrustDisposition{}).Where("id = ?", id).Updates(map[string]any{
			"callback_status":   model.CallbackStatusPending,
			"callback_attempts": 0,
			"next_attempt_at":   gorm.Expr("now()"),
		}).Error; err != nil {
			return err
		}
		var item model.TrustReviewItem
		if err := tx.Take(&item, disp.ReviewItemID).Error; err != nil {
			return err
		}
		return AppendAudit(tx, AuditEntry{
			ActorID: &actorID, Action: "callback_redeliver",
			Site: strptr(item.Site), SubjectKind: strptr(item.SubjectKind), SubjectID: strptr(item.SubjectID),
			ReasonCode: strptr(disp.ReasonCode),
		})
	})
}
