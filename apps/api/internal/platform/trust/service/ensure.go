package service

import (
	"context"
	"errors"

	"api/internal/platform/trust/model"

	"gorm.io/gorm"
)

type EnsureOutcome string

const (
	EnsureCreated           EnsureOutcome = "created"
	EnsureUpdated           EnsureOutcome = "updated"
	EnsureUnchanged         EnsureOutcome = "unchanged"
	EnsureDeprecatedSkipped EnsureOutcome = "deprecated_skipped"
)

type EnsureSubjectKindItem struct {
	Key             string
	CallbackURL     *string
	CallbackSecret  *string
	NotifyOnDismiss *bool
}

type EnsureSubjectKindResult struct {
	Key     string
	Outcome EnsureOutcome
}

func (s *RegistryService) EnsureSubjectKinds(ctx context.Context, actorID *int64, site string, items []EnsureSubjectKindItem) ([]EnsureSubjectKindResult, error) {
	results := make([]EnsureSubjectKindResult, 0, len(items))
	for _, item := range items {
		outcome, err := s.ensureOneSubjectKind(ctx, actorID, site, item)
		if err != nil {
			return nil, err
		}
		results = append(results, EnsureSubjectKindResult{Key: item.Key, Outcome: outcome})
	}
	return results, nil
}

func (s *RegistryService) ensureOneSubjectKind(ctx context.Context, actorID *int64, site string, item EnsureSubjectKindItem) (EnsureOutcome, error) {
	var outcome EnsureOutcome
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing model.TrustSubjectKind
		lerr := tx.Where("site = ? AND key = ?", site, item.Key).Take(&existing).Error

		if errors.Is(lerr, gorm.ErrRecordNotFound) {
			kind := model.TrustSubjectKind{
				Site: site, Key: item.Key,
				CallbackURL: item.CallbackURL, CallbackSecret: item.CallbackSecret,
				IsDeprecated: false, NotifyOnDismiss: item.NotifyOnDismiss != nil && *item.NotifyOnDismiss,
			}
			if err := tx.Create(&kind).Error; err != nil {
				return err
			}
			outcome = EnsureCreated
			return AppendAudit(tx, AuditEntry{
				ActorID: actorID, Action: "subject_kind_created",
				Site: strptr(site), SubjectKind: strptr(item.Key),
			})
		}
		if lerr != nil {
			return lerr
		}

		if existing.IsDeprecated {
			outcome = EnsureDeprecatedSkipped
			return nil
		}

		updates := map[string]any{}
		if item.CallbackURL != nil && derefStr(existing.CallbackURL) != *item.CallbackURL {
			updates["callback_url"] = *item.CallbackURL
		}
		if item.CallbackSecret != nil && derefStr(existing.CallbackSecret) != *item.CallbackSecret {
			updates["callback_secret"] = *item.CallbackSecret
		}
		if item.NotifyOnDismiss != nil && *item.NotifyOnDismiss != existing.NotifyOnDismiss {
			updates["notify_on_dismiss"] = *item.NotifyOnDismiss
		}
		if len(updates) == 0 {
			outcome = EnsureUnchanged
			return nil
		}
		if err := tx.Model(&model.TrustSubjectKind{}).Where("id = ?", existing.ID).Updates(updates).Error; err != nil {
			return err
		}
		outcome = EnsureUpdated
		return AppendAudit(tx, AuditEntry{
			ActorID: actorID, Action: "subject_kind_updated",
			Site: strptr(site), SubjectKind: strptr(item.Key),
		})
	})
	if err != nil {
		return "", err
	}
	return outcome, nil
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
