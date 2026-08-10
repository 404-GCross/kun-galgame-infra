package service

import (
	"context"
	"errors"

	"api/internal/platform/trust/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type RegistryService struct{ db *gorm.DB }

func NewRegistryService(db *gorm.DB) *RegistryService { return &RegistryService{db: db} }

func (s *RegistryService) ListSubjectKinds(ctx context.Context, site string, includeDeprecated bool) ([]model.TrustSubjectKind, error) {
	q := s.db.WithContext(ctx).Model(&model.TrustSubjectKind{})
	if site != "" {
		q = q.Where("site = ?", site)
	}
	if !includeDeprecated {
		q = q.Where("is_deprecated = false")
	}
	var kinds []model.TrustSubjectKind
	err := q.Order("site ASC").Order("key ASC").Find(&kinds).Error
	return kinds, err
}

func (s *RegistryService) CreateSubjectKind(ctx context.Context, actorID int64, site, key string, callbackURL, callbackSecret *string, notifyOnDismiss bool) (*model.TrustSubjectKind, error) {
	kind := model.TrustSubjectKind{Site: site, Key: key, CallbackURL: callbackURL, CallbackSecret: callbackSecret, IsDeprecated: false, NotifyOnDismiss: notifyOnDismiss}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&kind)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrSubjectKindExists
		}
		return AppendAudit(tx, AuditEntry{
			ActorID: &actorID, Action: "subject_kind_created",
			Site: strptr(site), SubjectKind: strptr(key),
		})
	})
	if err != nil {
		return nil, err
	}
	return &kind, nil
}

type SubjectKindPatch struct {
	CallbackURL     *string
	CallbackSecret  *string
	IsDeprecated    *bool
	NotifyOnDismiss *bool
}

func (s *RegistryService) PatchSubjectKind(ctx context.Context, actorID, id int64, patch SubjectKindPatch) (*model.TrustSubjectKind, error) {
	var kind model.TrustSubjectKind
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		lerr := tx.Take(&kind, id).Error
		if errors.Is(lerr, gorm.ErrRecordNotFound) {
			return ErrSubjectKindNotFound
		}
		if lerr != nil {
			return lerr
		}
		updates := map[string]any{}
		if patch.CallbackURL != nil {
			updates["callback_url"] = *patch.CallbackURL
		}
		if patch.CallbackSecret != nil {
			updates["callback_secret"] = *patch.CallbackSecret
		}
		if patch.IsDeprecated != nil {
			updates["is_deprecated"] = *patch.IsDeprecated
		}
		if patch.NotifyOnDismiss != nil {
			updates["notify_on_dismiss"] = *patch.NotifyOnDismiss
		}
		if len(updates) > 0 {
			if err := tx.Model(&model.TrustSubjectKind{}).Where("id = ?", id).Updates(updates).Error; err != nil {
				return err
			}
		}
		if err := tx.Take(&kind, id).Error; err != nil {
			return err
		}
		return AppendAudit(tx, AuditEntry{
			ActorID: &actorID, Action: "subject_kind_updated",
			Site: strptr(kind.Site), SubjectKind: strptr(kind.Key),
		})
	})
	if err != nil {
		return nil, err
	}
	return &kind, nil
}

func (s *RegistryService) ListReasons(ctx context.Context, site string) ([]model.TrustReportReason, error) {
	q := s.db.WithContext(ctx).Model(&model.TrustReportReason{})
	if site != "" {
		q = q.Where("site IS NULL OR site = ?", site)
	}
	var reasons []model.TrustReportReason
	err := q.Order("site NULLS FIRST").Order("key ASC").Find(&reasons).Error
	return reasons, err
}

func (s *RegistryService) ListUsableReasons(ctx context.Context, site string) ([]model.TrustReportReason, error) {
	var reasons []model.TrustReportReason
	err := s.db.WithContext(ctx).Model(&model.TrustReportReason{}).
		Where("(site IS NULL OR site = ?) AND is_deprecated = false", site).
		Order("site NULLS FIRST").Order("key ASC").Find(&reasons).Error
	return reasons, err
}

func (s *RegistryService) CreateReason(ctx context.Context, actorID int64, key, nameCN string, site *string, severity int16) (*model.TrustReportReason, error) {
	reason := model.TrustReportReason{Key: key, NameCN: nameCN, Site: site, Severity: severity, IsDeprecated: false}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&reason).Error; err != nil {
			return err
		}
		return AppendAudit(tx, AuditEntry{
			ActorID: &actorID, Action: "reason_created", Site: site, ReasonCode: strptr(key),
		})
	})
	if err != nil {
		return nil, err
	}
	return &reason, nil
}

type ReasonPatch struct {
	NameCN       *string
	Severity     *int16
	IsDeprecated *bool
}

func (s *RegistryService) PatchReason(ctx context.Context, actorID, id int64, patch ReasonPatch) (*model.TrustReportReason, error) {
	var reason model.TrustReportReason
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		lerr := tx.Take(&reason, id).Error
		if errors.Is(lerr, gorm.ErrRecordNotFound) {
			return ErrReasonNotFound
		}
		if lerr != nil {
			return lerr
		}
		updates := map[string]any{}
		if patch.NameCN != nil {
			updates["name_cn"] = *patch.NameCN
		}
		if patch.Severity != nil {
			updates["severity"] = *patch.Severity
		}
		if patch.IsDeprecated != nil {
			updates["is_deprecated"] = *patch.IsDeprecated
		}
		if len(updates) > 0 {
			if err := tx.Model(&model.TrustReportReason{}).Where("id = ?", id).Updates(updates).Error; err != nil {
				return err
			}
		}
		if err := tx.Take(&reason, id).Error; err != nil {
			return err
		}
		return AppendAudit(tx, AuditEntry{
			ActorID: &actorID, Action: "reason_updated", Site: reason.Site, ReasonCode: strptr(reason.Key),
		})
	})
	if err != nil {
		return nil, err
	}
	return &reason, nil
}
