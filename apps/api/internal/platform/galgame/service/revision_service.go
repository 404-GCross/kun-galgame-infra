package service

import (
	"context"
	"fmt"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/repository"
	"api/pkg/errors"

	"gorm.io/gorm"
)

// LookupSnapshotNames resolves the IDs referenced in one or more
// Snapshots (tag / official / engine / series) to display names.
//
// Used by GetRevisionDiff and GetPR responses so the frontend can
// render "tag added: 校园, 治愈" instead of "tag added: 42, 87" —
// avoids N+1 follow-up queries from the client. See
// [dto.SnapshotEntityNames] for the response shape + perf notes.
//
// Performance: 4 indexed `WHERE id IN (...)` SELECTs (~1ms each on
// PK), union of all IDs across both snapshots, batched per entity
// type. Skips queries when the corresponding ID slice is empty.
// Missing IDs (entity deleted since the revision was taken) are
// silently dropped from the map; the caller falls back to rendering
// "已删除 #ID" client-side.
func (s *GalgameService) LookupSnapshotNames(
	ctx context.Context,
	snapshots ...*model.Snapshot,
) *dto.SnapshotEntityNames {
	names := &dto.SnapshotEntityNames{
		Tags:      map[int]string{},
		Officials: map[int]string{},
		Engines:   map[int]string{},
		Series:    map[int]string{},
	}

	// Union all IDs across the input snapshots. Sets dedupe before
	// hitting the DB so the IN clause stays as small as possible.
	tagIDs := map[int]struct{}{}
	offIDs := map[int]struct{}{}
	engIDs := map[int]struct{}{}
	serIDs := map[int]struct{}{}
	for _, snap := range snapshots {
		if snap == nil {
			continue
		}
		for _, id := range snap.TagIDs {
			tagIDs[id] = struct{}{}
		}
		for _, id := range snap.OfficialIDs {
			offIDs[id] = struct{}{}
		}
		for _, id := range snap.EngineIDs {
			engIDs[id] = struct{}{}
		}
		if snap.SeriesID != nil {
			serIDs[*snap.SeriesID] = struct{}{}
		}
	}

	db := s.galgameRepo.DB().WithContext(ctx)

	type idNameRow struct {
		ID   int
		Name string
	}

	fillMap := func(table string, ids map[int]struct{}, target map[int]string) {
		if len(ids) == 0 {
			return
		}
		flat := make([]int, 0, len(ids))
		for id := range ids {
			flat = append(flat, id)
		}
		var rows []idNameRow
		// Soft-deleted rows are excluded by GORM's default scoping if
		// the model has gorm.DeletedAt — but querying by Table() skips
		// that. Diff context wants the name even if the entity is
		// soft-deleted (so the user sees what was there at that
		// revision), so the raw Table() query is intentional.
		if err := db.Table(table).
			Select("id, name").
			Where("id IN ?", flat).
			Scan(&rows).Error; err == nil {
			for _, r := range rows {
				target[r.ID] = r.Name
			}
		}
	}

	fillMap("galgame_tag", tagIDs, names.Tags)
	fillMap("galgame_official", offIDs, names.Officials)
	fillMap("galgame_engine", engIDs, names.Engines)
	fillMap("galgame_series", serIDs, names.Series)

	return names
}

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

// ListRecentRevisions returns the merged-revision feed (edit-landed events) for
// downstream activity timelines. Mirrors MessageService.ListFeed's cursor shape
// (since_id + has_more). Maps to a minimal DTO — no snapshot/note (the timeline
// only needs who / which / when).
func (s *GalgameService) ListRecentRevisions(ctx context.Context, sinceID int64, limit int) (*dto.RevisionFeedResponse, error) {
	if limit <= 0 {
		limit = 1000
	}
	if limit > 5000 {
		limit = 5000
	}
	items, err := s.revisionRepo.ListRecentMerged(ctx, sinceID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]dto.RevisionFeedItem, len(items))
	for i, rev := range items {
		out[i] = dto.RevisionFeedItem{
			ID:        int64(rev.ID),
			GalgameID: rev.GalgameID,
			Revision:  rev.Revision,
			UserID:    rev.UserID,
			Action:    rev.Action,
			Created:   rev.Created.Time().UTC().Format("2006-01-02T15:04:05Z"),
		}
	}
	return &dto.RevisionFeedResponse{
		Items:   out,
		HasMore: len(items) == limit,
	}, nil
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

// GetRevisionDiff computes the diff between a revision and its predecessor.
//
// changed_keys is authoritative when the revision recorded changed_fields at
// write time (every revision minted after that feature shipped): it reflects
// exactly what the editor touched, so an edit to one field shows one field —
// even if the adjacent snapshot drifted because VNDB enrichment mutated the
// live tables without minting a revision. Legacy revisions (changed_fields
// NULL) fall back to the historical whole-snapshot comparison. The old/new
// snapshots are always returned for value rendering.
func (s *GalgameService) GetRevisionDiff(ctx context.Context, galgameID, revision int) (map[string]bool, *model.Snapshot, *model.Snapshot, error) {
	current, err := s.revisionRepo.FindByRevision(ctx, galgameID, revision)
	if err != nil {
		return nil, nil, nil, errors.NewWithCode(errors.ErrNotFound)
	}

	currentSnapshot, err := model.SnapshotFromJSON(current.Snapshot)
	if err != nil {
		return nil, nil, nil, err
	}

	// Resolve the baseline snapshot (empty for the first revision or when the
	// predecessor is missing — only used for value rendering + legacy fallback).
	prevSnapshot := &model.Snapshot{}
	if revision > 1 {
		if prev, err := s.revisionRepo.FindByRevision(ctx, galgameID, revision-1); err == nil {
			if ps, err := model.SnapshotFromJSON(prev.Snapshot); err == nil {
				prevSnapshot = ps
			}
		}
	}

	if current.HasChangedFields() {
		recorded := map[string]bool{}
		for _, k := range current.ChangedFieldsList() {
			recorded[k] = true
		}
		return recorded, prevSnapshot, currentSnapshot, nil
	}

	// Legacy revision: derive from the snapshot delta.
	return model.ChangedKeys(prevSnapshot, currentSnapshot), prevSnapshot, currentSnapshot, nil
}

// Revert rolls back a galgame to a specific revision
func (s *GalgameService) Revert(ctx context.Context, userID, galgameID, targetRevision int, roles []string) error {
	// Check permission
	galgame, err := s.galgameRepo.FindByID(ctx, galgameID)
	if err != nil {
		return errors.NewWithCode(errors.ErrGalgameNotFound)
	}
	if galgame.UserID != userID && !hasRole(roles, "admin") {
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

	// Defence-in-depth probe: refping is supposed to keep every revision-
	// referenced hash alive in image_service, but if a hash slipped
	// through (manual purge, image_service GC race, …) the snapshot can
	// resurrect a galgame pointing at a now-deleted image. Strip those
	// hashes here and record the loss on the new revision's Note so an
	// admin can re-upload. When probeImages is unset (= tests), skip.
	missing, noteSuffix := s.scrubMissingImages(ctx, snapshot)

	return s.galgameRepo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Capture the live state before the revert so changed_fields records
		// exactly what rolling back to the target alters (live → target).
		beforeFull, err := loadGalgameWithRelations(tx, galgameID)
		if err != nil {
			return err
		}
		revertChanged := model.KeysOf(model.ChangedKeys(model.TakeSnapshot(beforeFull), snapshot))

		// Apply the old snapshot
		if err := repository.ApplySnapshot(tx, galgameID, userID, snapshot); err != nil {
			return err
		}

		// Get next revision number
		nextRev, err := repository.NextRevision(tx, galgameID)
		if err != nil {
			return err
		}

		// Create revert revision (re-take is NOT done here because the
		// snapshot we just applied IS the canonical state — same flow as
		// the original Revert).
		snapshotJSON, err := snapshot.ToJSON()
		if err != nil {
			return err
		}

		revertedTo := targetRevision
		note := fmt.Sprintf("回滚到版本 %d", targetRevision)
		if noteSuffix != "" {
			note = note + "；" + noteSuffix
		}
		_ = missing // surfaced via Note; future API could echo the list to the response
		rev := &model.GalgameRevision{
			GalgameID:  galgameID,
			Revision:   nextRev,
			UserID:     userID,
			Action:     "reverted",
			Note:       note,
			Snapshot:   snapshotJSON,
			RevertedTo: &revertedTo,
		}
		rev.SetChangedFields(revertChanged)
		return tx.Create(rev).Error
	})
}

// scrubMissingImages mutates `snap` in place, removing any cover /
// screenshot whose image_hash is missing from image_service. Returns
// the list of stripped hashes (for the Note) and a Chinese note suffix
// describing the degrade. When probeImages is unset OR returns an
// error, the snapshot is left untouched — the assumption is that a
// failing probe is worse than a possibly-broken revert (admin can re-
// upload images afterwards; a hard-failed revert breaks the workflow).
//
// PR5 retired snap.BannerImageHash, so the only image references left
// in a snapshot are Covers and Screenshots — handled below.
func (s *GalgameService) scrubMissingImages(ctx context.Context, snap *model.Snapshot) (missing []string, noteSuffix string) {
	if s.probeImages == nil || snap == nil {
		return nil, ""
	}
	candidates := snapshotHashes(snap)
	if len(candidates) == 0 {
		return nil, ""
	}
	notFound, err := s.probeImages(ctx, candidates)
	if err != nil || len(notFound) == 0 {
		return nil, ""
	}
	missingSet := make(map[string]bool, len(notFound))
	for _, h := range notFound {
		missingSet[h] = true
	}
	snap.Covers = filterImageRows(snap.Covers, func(c model.SnapshotCover) bool {
		return !missingSet[c.ImageHash]
	})
	snap.Screenshots = filterImageRows(snap.Screenshots, func(sh model.SnapshotScreenshot) bool {
		return !missingSet[sh.ImageHash]
	})
	return notFound, fmt.Sprintf("partial revert：%d 张图片在 image_service 已不存在，已从快照剔除", len(notFound))
}

// snapshotHashes returns every image_hash referenced by `snap`, deduped.
// Used as the input batch to the image-service existence probe.
//
// Covers + Screenshots are the only image references in a snapshot
// since PR5 retired BannerImageHash; the canonical banner now lives in
// covers[sort_order=0] and is included by the Covers loop below.
func snapshotHashes(snap *model.Snapshot) []string {
	seen := make(map[string]bool)
	for _, c := range snap.Covers {
		if c.ImageHash != "" {
			seen[c.ImageHash] = true
		}
	}
	for _, sh := range snap.Screenshots {
		if sh.ImageHash != "" {
			seen[sh.ImageHash] = true
		}
	}
	out := make([]string, 0, len(seen))
	for h := range seen {
		out = append(out, h)
	}
	return out
}

// filterImageRows is a tiny generic helper: keep rows where `keep` is true,
// preserving order. Used to strip missing-hash entries from cover/screenshot
// slices without mutating other fields of the snapshot.
func filterImageRows[T any](rows []T, keep func(T) bool) []T {
	out := make([]T, 0, len(rows))
	for _, r := range rows {
		if keep(r) {
			out = append(out, r)
		}
	}
	return out
}

// SubmitPR creates a new pull request
func (s *GalgameService) SubmitPR(ctx context.Context, userID, galgameID int, proposedSnapshot *model.Snapshot, title, message string) (*model.GalgamePR, error) {
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
		UserID:       userID,
		Title:        title,
		Message:      message,
		BaseRevision: latestRev.Revision,
		Snapshot:     snapshotJSON,
	}

	if err := s.prRepo.Create(ctx, pr); err != nil {
		return nil, err
	}

	return pr, nil
}

// MergePR merges a pull request with automatic field-level rebase
func (s *GalgameService) MergePR(ctx context.Context, userID, galgameID, prID int, roles []string) error {
	pr, err := s.prRepo.FindByID(ctx, prID)
	if err != nil {
		return errors.NewWithCode(errors.ErrNotFound)
	}
	// Enforce the route contract: the PR must belong to :gid.
	if pr.GalgameID != galgameID {
		return errors.NewWithCode(errors.ErrNotFound)
	}

	// Check permission: galgame creator or admin
	galgame, err := s.galgameRepo.FindByID(ctx, pr.GalgameID)
	if err != nil {
		return errors.NewWithCode(errors.ErrGalgameNotFound)
	}
	if galgame.UserID != userID && !hasRole(roles, "admin") {
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

		// Same dead-image safeguard as Revert: PRs may have been
		// submitted long before merge, so their stored snapshot can
		// reference a hash that image_service has TTL-deleted in the
		// interim. Strip those hashes before ApplySnapshot so the merge
		// doesn't resurrect a broken galgame; record the degrade on the
		// merged revision's Note. Skipped when probeImages is unset.
		_, mergeNoteSuffix := s.scrubMissingImages(ctx, finalSnapshot)

		// Capture the live state before applying so changed_fields records the
		// net effect of the merge (live → finalSnapshot), independent of how
		// stale the PR's base revision was.
		beforeFull, err := loadGalgameWithRelations(tx, pr.GalgameID)
		if err != nil {
			return err
		}
		mergeChanged := model.KeysOf(model.ChangedKeys(model.TakeSnapshot(beforeFull), finalSnapshot))

		// Apply snapshot to galgame tables
		if err := repository.ApplySnapshot(tx, pr.GalgameID, userID, finalSnapshot); err != nil {
			return err
		}

		// Create revision
		snapshotJSON, err := finalSnapshot.ToJSON()
		if err != nil {
			return err
		}

		mergedNote := prMergeNote(pr)
		if mergeNoteSuffix != "" {
			if mergedNote != "" {
				mergedNote = mergedNote + "；" + mergeNoteSuffix
			} else {
				mergedNote = mergeNoteSuffix
			}
		}
		rev := &model.GalgameRevision{
			GalgameID: pr.GalgameID,
			Revision:  latestRev,
			UserID:    pr.UserID,
			Action:    "merged",
			Note:      mergedNote,
			Snapshot:  snapshotJSON,
		}
		rev.SetChangedFields(mergeChanged)
		if err := tx.Create(rev).Error; err != nil {
			return err
		}

		// Update PR with completion info. completed_time = the actual merge
		// time (NOW()), like DeclinePR — not the galgame's stale last-modified
		// timestamp.
		if err := tx.Model(&model.GalgamePR{}).Where("id = ?", prID).Updates(map[string]any{
			"completed_by":   userID,
			"completed_time": gorm.Expr("NOW()"),
			"revision_id":    rev.ID,
		}).Error; err != nil {
			return err
		}

		// Ensure PR author is a contributor. Propagate the Count and Create
		// errors (previously both were swallowed, so a failed insert silently
		// dropped the contributor row — and the table has no unique index to
		// fall back on).
		var count int64
		if err := tx.Model(&model.GalgameContributor{}).
			Where("galgame_id = ? AND user_id = ?", pr.GalgameID, pr.UserID).
			Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			if err := tx.Create(&model.GalgameContributor{GalgameID: pr.GalgameID, UserID: pr.UserID}).Error; err != nil {
				return err
			}
		}

		return nil
	})
}

// DeclinePR declines a pull request
func (s *GalgameService) DeclinePR(ctx context.Context, userID, galgameID, prID int, roles []string) error {
	pr, err := s.prRepo.FindByID(ctx, prID)
	if err != nil {
		return errors.NewWithCode(errors.ErrNotFound)
	}
	if pr.GalgameID != galgameID {
		return errors.NewWithCode(errors.ErrNotFound)
	}

	galgame, err := s.galgameRepo.FindByID(ctx, pr.GalgameID)
	if err != nil {
		return errors.NewWithCode(errors.ErrGalgameNotFound)
	}
	if galgame.UserID != userID && !hasRole(roles, "admin") {
		return errors.NewWithCode(errors.ErrGalgameForbidden)
	}

	result := s.galgameRepo.DB().WithContext(ctx).
		Model(&model.GalgamePR{}).
		Where("id = ? AND status = 0", prID).
		Updates(map[string]any{
			"status":         2,
			"completed_by":   userID,
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

// GetPR returns a PR by ID, scoped to its galgame (route contract: the PR
// must belong to :gid, else 404).
func (s *GalgameService) GetPR(ctx context.Context, galgameID, prID int) (*model.GalgamePR, error) {
	pr, err := s.prRepo.FindByID(ctx, prID)
	if err != nil {
		return nil, errors.NewWithCode(errors.ErrNotFound)
	}
	if pr.GalgameID != galgameID {
		return nil, errors.NewWithCode(errors.ErrNotFound)
	}
	return pr, nil
}

// prMergeNote composes the merged-revision note from a PR's title/message.
func prMergeNote(pr *model.GalgamePR) string {
	switch {
	case pr.Title != "" && pr.Message != "":
		return pr.Title + "\n\n" + pr.Message
	case pr.Title != "":
		return pr.Title
	default:
		return pr.Message
	}
}

// AssertGalgameVisible gates revision/PR read endpoints to the same status
// visibility as GetByIDWithViewer: published(0) is public; pending(3)/
// declined(4) are visible only to the submitter; everything else (banned,
// VNDB-draft, missing) is 404. Prevents leaking full snapshot content of
// hidden galgames via the revision/PR history.
func (s *GalgameService) AssertGalgameVisible(ctx context.Context, galgameID, viewerUserID int) error {
	g, err := s.galgameRepo.FindByID(ctx, galgameID)
	if err != nil {
		return errors.NewWithCode(errors.ErrGalgameNotFound)
	}
	switch {
	case g.Status == model.GalgameStatusPublished:
		return nil
	case (g.Status == model.GalgameStatusPending || g.Status == model.GalgameStatusDeclined) &&
		viewerUserID > 0 && g.UserID == viewerUserID:
		return nil
	default:
		return errors.NewWithCode(errors.ErrGalgameNotFound)
	}
}

func findRevisionInTx(tx *gorm.DB, galgameID, revision int) (*model.GalgameRevision, error) {
	var rev model.GalgameRevision
	err := tx.Where("galgame_id = ? AND revision = ?", galgameID, revision).First(&rev).Error
	return &rev, err
}
