package service

import (
	"context"
	"fmt"

	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/repository"
	"api/pkg/errors"

	"gorm.io/gorm"
)

// ListRevisions returns revision history for a galgame
func (s *GalgameService) ListRevisions(ctx context.Context, galgameID, page, limit int, includeMinor bool) ([]model.GalgameRevision, int64, error) {
	if page <= 0 {
		page = 1
	}
	if limit <= 0 {
		limit = 20
	}
	return s.revisionRepo.List(ctx, galgameID, page, limit, includeMinor)
}

// GetRevision returns a specific revision
func (s *GalgameService) GetRevision(ctx context.Context, galgameID, revision int) (*model.GalgameRevision, error) {
	rev, err := s.revisionRepo.FindByRevision(ctx, galgameID, revision)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, errors.NewWithCode(errors.ErrNotFound)
		}
		return nil, err
	}
	return rev, nil
}

// GetRevisionDiff computes the diff between a revision and its predecessor
func (s *GalgameService) GetRevisionDiff(ctx context.Context, galgameID, revision int) (map[string]bool, *model.Snapshot, *model.Snapshot, error) {
	current, err := s.revisionRepo.FindByRevision(ctx, galgameID, revision)
	if err != nil {
		return nil, nil, nil, errors.NewWithCode(errors.ErrNotFound)
	}

	currentSnapshot, err := model.SnapshotFromJSON(current.Snapshot)
	if err != nil {
		return nil, nil, nil, err
	}

	if revision <= 1 {
		// First revision: diff against empty
		empty := &model.Snapshot{}
		return model.ChangedKeys(empty, currentSnapshot), empty, currentSnapshot, nil
	}

	prev, err := s.revisionRepo.FindByRevision(ctx, galgameID, revision-1)
	if err != nil {
		empty := &model.Snapshot{}
		return model.ChangedKeys(empty, currentSnapshot), empty, currentSnapshot, nil
	}

	prevSnapshot, err := model.SnapshotFromJSON(prev.Snapshot)
	if err != nil {
		return nil, nil, nil, err
	}

	return model.ChangedKeys(prevSnapshot, currentSnapshot), prevSnapshot, currentSnapshot, nil
}

// Revert rolls back a galgame to a specific revision
func (s *GalgameService) Revert(ctx context.Context, uid, galgameID, targetRevision int, roles []string) error {
	// Check permission
	galgame, err := s.galgameRepo.FindByID(ctx, galgameID)
	if err != nil {
		return errors.NewWithCode(errors.ErrGalgameNotFound)
	}
	if galgame.UserID != uid && !hasRole(roles, "admin") {
		return errors.NewWithCode(errors.ErrGalgameForbidden)
	}

	// Load target revision's snapshot
	targetRev, err := s.revisionRepo.FindByRevision(ctx, galgameID, targetRevision)
	if err != nil {
		return errors.NewWithCode(errors.ErrNotFound)
	}

	snapshot, err := model.SnapshotFromJSON(targetRev.Snapshot)
	if err != nil {
		return err
	}

	return s.galgameRepo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Apply the old snapshot
		if err := repository.ApplySnapshot(tx, galgameID, uid, snapshot); err != nil {
			return err
		}

		// Get next revision number
		nextRev, err := repository.NextRevision(tx, galgameID)
		if err != nil {
			return err
		}

		// Create revert revision
		snapshotJSON, err := snapshot.ToJSON()
		if err != nil {
			return err
		}

		revertedTo := targetRevision
		return tx.Create(&model.GalgameRevision{
			GalgameID:  galgameID,
			Revision:   nextRev,
			UserID:     uid,
			Action:     "reverted",
			Note:       fmt.Sprintf("回滚到版本 %d", targetRevision),
			Snapshot:   snapshotJSON,
			RevertedTo: &revertedTo,
		}).Error
	})
}

// SubmitPR creates a new pull request
func (s *GalgameService) SubmitPR(ctx context.Context, uid, galgameID int, proposedSnapshot *model.Snapshot, note string) (*model.GalgamePR, error) {
	// Get current latest revision
	latestRev, err := s.revisionRepo.FindLatest(ctx, galgameID)
	if err != nil {
		return nil, errors.NewWithCode(errors.ErrGalgameNotFound)
	}

	snapshotJSON, err := proposedSnapshot.ToJSON()
	if err != nil {
		return nil, err
	}

	pr := &model.GalgamePR{
		GalgameID:    galgameID,
		UserID:       uid,
		Note:         note,
		BaseRevision: latestRev.Revision,
		Snapshot:     snapshotJSON,
	}

	if err := s.prRepo.Create(ctx, pr); err != nil {
		return nil, err
	}

	return pr, nil
}

// MergePR merges a pull request with automatic field-level rebase
func (s *GalgameService) MergePR(ctx context.Context, uid, prID int, roles []string) error {
	pr, err := s.prRepo.FindByID(ctx, prID)
	if err != nil {
		return errors.NewWithCode(errors.ErrNotFound)
	}

	// Check permission: galgame creator or admin
	galgame, err := s.galgameRepo.FindByID(ctx, pr.GalgameID)
	if err != nil {
		return errors.NewWithCode(errors.ErrGalgameNotFound)
	}
	if galgame.UserID != uid && !hasRole(roles, "admin") {
		return errors.NewWithCode(errors.ErrGalgameForbidden)
	}

	prSnapshot, err := model.SnapshotFromJSON(pr.Snapshot)
	if err != nil {
		return err
	}

	return s.galgameRepo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Atomically check PR is still pending
		result := tx.Model(&model.GalgamePR{}).
			Where("id = ? AND status = 0", prID).
			Update("status", 1)
		if result.RowsAffected == 0 {
			return fmt.Errorf("PR 已被处理")
		}

		// Get latest revision (with lock)
		latestRev, err := repository.NextRevision(tx, pr.GalgameID)
		if err != nil {
			return err
		}
		currentRevNum := latestRev - 1

		finalSnapshot := prSnapshot

		// Auto-rebase if base_revision is not the latest
		if pr.BaseRevision < currentRevNum {
			// Load base and current snapshots
			baseRev, err := findRevisionInTx(tx, pr.GalgameID, pr.BaseRevision)
			if err != nil {
				return err
			}
			currentRev, err := findRevisionInTx(tx, pr.GalgameID, currentRevNum)
			if err != nil {
				return err
			}

			baseSnapshot, err := model.SnapshotFromJSON(baseRev.Snapshot)
			if err != nil {
				return err
			}
			currentSnapshot, err := model.SnapshotFromJSON(currentRev.Snapshot)
			if err != nil {
				return err
			}

			// Check for field-level conflicts
			prChangedKeys := model.ChangedKeys(baseSnapshot, prSnapshot)
			otherChangedKeys := model.ChangedKeys(baseSnapshot, currentSnapshot)

			for key := range prChangedKeys {
				if otherChangedKeys[key] {
					return fmt.Errorf("字段冲突: %s 已被其他编辑修改，请基于最新版本重新提交", key)
				}
			}

			// No conflicts — rebase: apply PR changes onto current snapshot
			rebased := *currentSnapshot
			model.ApplyChanges(&rebased, prSnapshot, prChangedKeys)
			finalSnapshot = &rebased
		}

		// Apply snapshot to galgame tables
		if err := repository.ApplySnapshot(tx, pr.GalgameID, uid, finalSnapshot); err != nil {
			return err
		}

		// Create revision
		snapshotJSON, err := finalSnapshot.ToJSON()
		if err != nil {
			return err
		}

		rev := &model.GalgameRevision{
			GalgameID: pr.GalgameID,
			Revision:  latestRev,
			UserID:    pr.UserID,
			Action:    "merged",
			Note:      pr.Note,
			Snapshot:  snapshotJSON,
		}
		if err := tx.Create(rev).Error; err != nil {
			return err
		}

		// Update PR with completion info
		now := galgame.Updated // reuse as approx time
		if err := tx.Model(&model.GalgamePR{}).Where("id = ?", prID).Updates(map[string]any{
			"completed_by":   uid,
			"completed_time": now,
			"revision_id":    rev.ID,
		}).Error; err != nil {
			return err
		}

		// Ensure PR author is a contributor
		var count int64
		tx.Model(&model.GalgameContributor{}).Where("galgame_id = ? AND user_id = ?", pr.GalgameID, pr.UserID).Count(&count)
		if count == 0 {
			tx.Create(&model.GalgameContributor{GalgameID: pr.GalgameID, UserID: pr.UserID})
		}

		return nil
	})
}

// DeclinePR declines a pull request
func (s *GalgameService) DeclinePR(ctx context.Context, uid, prID int, roles []string) error {
	pr, err := s.prRepo.FindByID(ctx, prID)
	if err != nil {
		return errors.NewWithCode(errors.ErrNotFound)
	}

	galgame, err := s.galgameRepo.FindByID(ctx, pr.GalgameID)
	if err != nil {
		return errors.NewWithCode(errors.ErrGalgameNotFound)
	}
	if galgame.UserID != uid && !hasRole(roles, "admin") {
		return errors.NewWithCode(errors.ErrGalgameForbidden)
	}

	result := s.galgameRepo.DB().WithContext(ctx).
		Model(&model.GalgamePR{}).
		Where("id = ? AND status = 0", prID).
		Updates(map[string]any{
			"status":         2,
			"completed_by":   uid,
			"completed_time": gorm.Expr("NOW()"),
		})

	if result.RowsAffected == 0 {
		return fmt.Errorf("PR 已被处理")
	}

	return nil
}

// ListPRs returns PRs for a galgame
func (s *GalgameService) ListPRs(ctx context.Context, galgameID, page, limit int) ([]model.GalgamePR, int64, error) {
	if page <= 0 {
		page = 1
	}
	if limit <= 0 {
		limit = 20
	}
	return s.prRepo.List(ctx, galgameID, page, limit)
}

// GetPR returns a PR by ID
func (s *GalgameService) GetPR(ctx context.Context, prID int) (*model.GalgamePR, error) {
	return s.prRepo.FindByID(ctx, prID)
}

func findRevisionInTx(tx *gorm.DB, galgameID, revision int) (*model.GalgameRevision, error) {
	var rev model.GalgameRevision
	err := tx.Where("galgame_id = ? AND revision = ?", galgameID, revision).First(&rev).Error
	return &rev, err
}
