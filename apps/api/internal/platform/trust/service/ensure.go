package service

import (
	"context"
	"errors"

	"api/internal/platform/trust/model"

	"gorm.io/gorm"
)

// Declarative subject-kind registration (step 06). A site (or an admin batch)
// declares the kinds it wants and trust CONVERGES the registry to that
// declaration, per kind: create the missing ones, update the ones whose provided
// mutable fields drift, leave the matching ones untouched, and NEVER revive a
// deprecated kind (reviving one is an admin decision — 章程 ruling 13). The same
// convergence backs the S2S ensure face and the admin batch endpoint (site is
// the only difference: derived from the client binding vs an explicit param).

// EnsureOutcome is one kind's convergence verdict.
type EnsureOutcome string

const (
	EnsureCreated           EnsureOutcome = "created"
	EnsureUpdated           EnsureOutcome = "updated"
	EnsureUnchanged         EnsureOutcome = "unchanged"
	EnsureDeprecatedSkipped EnsureOutcome = "deprecated_skipped"
)

// EnsureSubjectKindItem is one declared kind. The three optional fields are
// SPARSE: a nil field is not part of the declaration, so an omitted field never
// forces an update and never clobbers an admin-set value the caller does not know
// about. On create, an omitted callback stays unset and notify_on_dismiss
// defaults false.
type EnsureSubjectKindItem struct {
	Key             string
	CallbackURL     *string
	CallbackSecret  *string
	NotifyOnDismiss *bool
}

// EnsureSubjectKindResult pairs a declared key with its convergence verdict. The
// results preserve request order (duplicated keys included).
type EnsureSubjectKindResult struct {
	Key     string
	Outcome EnsureOutcome
}

// EnsureSubjectKinds converges the registry to the declared kinds for one site.
// actorID is the audit actor (nil = a system/S2S actor; an admin batch passes the
// operator's id). Each kind is converged in its own transaction — matching the
// single-row Create/Patch paths — so a partial batch on error is re-converged by
// the next (idempotent) call. Results are returned in request order.
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

// ensureOneSubjectKind applies the convergence rules to a single (site, key).
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

		// A deprecated kind is never revived by a declaration.
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

// derefStr treats a nil *string as the empty string, so a NULL callback column
// compares equal to a declared "" (no phantom update).
func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
