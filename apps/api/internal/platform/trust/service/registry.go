package service

import (
	"context"
	"errors"

	"api/internal/platform/trust/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// RegistryService owns the subject-kind and reason registries. Registry rows are
// NEVER deleted — is_deprecated retires them (章程 ruling 13). Every mutation
// appends an audit row (章程 ruling 10).
type RegistryService struct{ db *gorm.DB }

func NewRegistryService(db *gorm.DB) *RegistryService { return &RegistryService{db: db} }

// ---- subject kinds ----

// ListSubjectKinds returns registered kinds. site "" = all sites (admin);
// includeDeprecated=false hides retired kinds (the S2S sanity-check face).
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

// CreateSubjectKind registers a new (site, key). A duplicate → ErrSubjectKindExists
// (re-open a deprecated one via PatchSubjectKind, never re-insert).
func (s *RegistryService) CreateSubjectKind(ctx context.Context, actorID int64, site, key string, callbackURL, callbackSecret *string) (*model.TrustSubjectKind, error) {
	kind := model.TrustSubjectKind{Site: site, Key: key, CallbackURL: callbackURL, CallbackSecret: callbackSecret, IsDeprecated: false}
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

// SubjectKindPatch is a partial update; only non-nil fields are applied.
type SubjectKindPatch struct {
	CallbackURL    *string
	CallbackSecret *string
	IsDeprecated   *bool
}

// PatchSubjectKind updates a kind's callback config / deprecation.
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

// ---- reasons ----

// ListReasons returns the global base (site NULL) plus, when site is given, that
// site's extensions. site "" = all reasons (admin).
func (s *RegistryService) ListReasons(ctx context.Context, site string) ([]model.TrustReportReason, error) {
	q := s.db.WithContext(ctx).Model(&model.TrustReportReason{})
	if site != "" {
		q = q.Where("site IS NULL OR site = ?", site)
	}
	var reasons []model.TrustReportReason
	err := q.Order("site NULLS FIRST").Order("key ASC").Find(&reasons).Error
	return reasons, err
}

// CreateReason registers a reason. site nil = a global reason; non-nil = a
// per-site extension. severity is written explicitly (no default-tag trap).
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

// ReasonPatch is a partial update; only non-nil fields are applied.
type ReasonPatch struct {
	NameCN       *string
	Severity     *int16
	IsDeprecated *bool
}

// PatchReason updates a reason's label / severity / deprecation.
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
