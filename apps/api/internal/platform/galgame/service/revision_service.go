package service

import (
	"context"
	"fmt"

	"api/internal/platform/authz"
	"api/internal/platform/editing"
	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/editbridge"
	"api/internal/platform/galgame/editspec"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/perm"
	"api/pkg/errors"

	"gorm.io/gorm"
)

// E2a strangler: every method here keeps its old wire contract (apps/wiki
// consumes it unchanged until E3) but reads through editbridge and writes
// through the editing engine. galgame_revision / galgame_pr are frozen.

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

// ListRevisions returns revision history for a galgame (edit_* via the
// bridge, old row shape on the wire).
func (s *GalgameService) ListRevisions(ctx context.Context, galgameID, page, limit int, includeMinor bool) ([]model.GalgameRevision, int64, error) {
	if page <= 0 {
		page = 1
	}
	if limit <= 0 {
		limit = 20
	}
	return s.bridge.ListRevisions(ctx, galgameID, page, limit, includeMinor)
}

// ListRecentRevisions returns the merged-revision feed (edit-landed events) for
// downstream activity timelines. Mirrors MessageService.ListFeed's cursor shape
// (since_id + has_more). The cursor rides COALESCE(legacy_id, id), so consumer
// cursors saved against the old table survive the migration (the transform
// bumps the identity sequence above every legacy id).
func (s *GalgameService) ListRecentRevisions(ctx context.Context, sinceID int64, limit int) (*dto.RevisionFeedResponse, error) {
	if limit <= 0 {
		limit = 1000
	}
	if limit > 5000 {
		limit = 5000
	}
	items, err := s.bridge.Feed(ctx, sinceID, limit)
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

// GetRevision returns a specific revision (revision 0 = latest, as before).
func (s *GalgameService) GetRevision(ctx context.Context, galgameID, revision int) (*model.GalgameRevision, error) {
	rev, err := s.bridge.GetRevision(ctx, galgameID, revision)
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
// write time; legacy revisions (migrated with the null_changed_fields marker —
// surfaced by the bridge as an absent ChangedFields) fall back to the
// historical whole-snapshot comparison. Same semantics as pre-E2a, over edit_*.
func (s *GalgameService) GetRevisionDiff(ctx context.Context, galgameID, revision int) (map[string]bool, *model.Snapshot, *model.Snapshot, error) {
	current, err := s.bridge.GetRevision(ctx, galgameID, revision)
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
		if prev, err := s.bridge.GetRevision(ctx, galgameID, revision-1); err == nil {
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

// Revert rolls back a galgame to a specific revision through the engine
// (reverse patch, action=reverted, history untouched).
//
// E2a posture changes vs the frozen implementation, both deliberate:
//   - The dead-image scrub probe is gone — refping keeps every snapshot hash
//     alive (the job also walks edit_* now), so the probe was a safety net;
//     a lost hash degrades to a broken image, never a broken revert.
//   - Crossing a status boundary (the target snapshot pins a different
//     status than live) needs the status review authority — a creator can no
//     longer un-ban/un-publish by reverting past an admin action. Migrated
//     snapshots carry no status key, so reverts into pre-E2a history are
//     unaffected.
func (s *GalgameService) Revert(ctx context.Context, userID, galgameID, targetRevision int, roles []string) error {
	galgame, err := s.galgameRepo.FindByID(ctx, galgameID)
	if err != nil {
		return errors.NewWithCode(errors.ErrGalgameNotFound)
	}
	if galgame.UserID != userID && !perm.Resolver.Can(roles, perm.OwnerOverride) {
		return errors.NewWithCode(errors.ErrGalgameForbidden)
	}

	perms := []authz.Permission{perm.EditGameReview}
	if perm.Resolver.Can(roles, perm.AdminAccess) {
		perms = append(perms, perm.EditGameStatus)
	}

	_, _, err = s.edit.Revert(ctx, editing.RevertInput{
		EntityType: editspec.TypeGame,
		EntityID:   int64(galgameID),
		ToSeq:      targetRevision,
		Note:       fmt.Sprintf("回滚到版本 %d", targetRevision),
		Actor:      editActor(userID, 2, perms...),
	})
	switch {
	case err == nil:
		return nil
	case err == editing.ErrNoEffectiveChanges:
		// Live state already equals the target. The old machine wrote a
		// no-op revision here; the engine's changed_fields precision makes
		// this a clean success instead.
		return nil
	case err == editing.ErrRevisionNotFound:
		return errors.NewWithCode(errors.ErrNotFound)
	default:
		return mapEngineWriteError(err)
	}
}

// SubmitPR files a pull request as an open engine proposal. The stored patch
// is the field-level delta of the proposed snapshot against the latest
// revision (exactly the delta the old GetPR diff surfaced); the title/message
// pair rides the proposal note via the bijective EncodeNote packing.
func (s *GalgameService) SubmitPR(ctx context.Context, userID, galgameID int, proposedSnapshot *model.Snapshot, title, message string) (*model.GalgamePR, error) {
	base, err := s.baseSnapshotForPR(ctx, galgameID)
	if err != nil {
		return nil, err
	}

	changed := model.ChangedKeys(base, proposedSnapshot)
	if len(changed) == 0 {
		// The old machine happily stored a no-change PR; the engine refuses
		// an empty patch. Surface an actionable 400 instead.
		return nil, errors.New(errors.ErrValidationFailed, "PR 未包含任何变更")
	}
	patch, err := rekeyedPatch(proposedSnapshot, changed)
	if err != nil {
		return nil, err
	}

	// Trust tier 0: the PR route is the review track — automerge=trusted
	// must NOT fire regardless of who submits (the old wire has no
	// "submit-and-land" semantics; direct edits go through Update).
	prop, _, err := s.edit.CreateProposal(ctx, editing.CreateProposalInput{
		EntityType: editspec.TypeGame,
		EntityID:   int64(galgameID),
		Patch:      patch,
		Note:       editbridge.EncodeNote(title, message),
		Actor:      editActor(userID, 0),
	})
	if err != nil {
		return nil, mapEngineWriteError(err)
	}

	return s.bridge.GetPR(ctx, galgameID, int(prop.ID))
}

// baseSnapshotForPR resolves the snapshot a PR diffs against: the latest
// engine revision, falling back to the live state for an entity without a
// birth record (should not exist post-migration; defensive parity with the
// old handler's fallback).
func (s *GalgameService) baseSnapshotForPR(ctx context.Context, galgameID int) (*model.Snapshot, error) {
	if latest, err := s.bridge.GetRevision(ctx, galgameID, 0); err == nil {
		if snap, err := model.SnapshotFromJSON(latest.Snapshot); err == nil {
			return snap, nil
		}
	}
	full, err := loadGalgameWithRelations(s.galgameRepo.DB().WithContext(ctx), galgameID)
	if err != nil {
		return nil, errors.NewWithCode(errors.ErrGalgameNotFound)
	}
	return model.TakeSnapshot(full), nil
}

// MergePR merges a pull request through the engine. The engine's per-field
// rebase replaces the old snapshot-level auto-rebase with identical
// semantics: disjoint fields fast-forward, fields someone else changed since
// the PR's base conflict instead of clobbering.
func (s *GalgameService) MergePR(ctx context.Context, userID, galgameID, prID int, roles []string) error {
	pr, err := s.bridge.GetPR(ctx, galgameID, prID)
	if err != nil {
		return errors.NewWithCode(errors.ErrNotFound)
	}

	// Check permission: galgame creator or admin
	galgame, err := s.galgameRepo.FindByID(ctx, galgameID)
	if err != nil {
		return errors.NewWithCode(errors.ErrGalgameNotFound)
	}
	if galgame.UserID != userID && !perm.Resolver.Can(roles, perm.OwnerOverride) {
		return errors.NewWithCode(errors.ErrGalgameForbidden)
	}

	proposalID, err := s.bridge.ResolveProposalID(ctx, galgameID, prID)
	if err != nil {
		return errors.NewWithCode(errors.ErrNotFound)
	}

	if _, err := s.edit.MergeProposal(ctx, proposalID, editActor(userID, 2, perm.EditGameReview), ""); err != nil {
		return mapMergeError(err)
	}

	// Ensure the PR author is a contributor (post-merge, mirrors the old tx).
	return s.ensureContributor(ctx, galgameID, pr.UserID)
}

// mapMergeError keeps the old wire's merge failure texts: conflicts and
// double-processing surface as plain-text 400s, exactly as before.
func mapMergeError(err error) error {
	if err == editing.ErrNotOpen {
		return fmt.Errorf("PR 已被处理")
	}
	if conflict, ok := err.(*editing.ConflictError); ok && len(conflict.Keys) > 0 {
		keys := editbridge.RekeyKeysNewToOld(conflict.Keys)
		return fmt.Errorf("字段冲突: %s 已被其他编辑修改，请基于最新版本重新提交", keys[0])
	}
	if err == editing.ErrEmptyPatch || err == editing.ErrNoEffectiveChanges {
		return fmt.Errorf("PR 未包含任何变更")
	}
	return mapEngineWriteError(err)
}

// DeclinePR declines a pull request.
func (s *GalgameService) DeclinePR(ctx context.Context, userID, galgameID, prID int, roles []string) error {
	if _, err := s.bridge.GetPR(ctx, galgameID, prID); err != nil {
		return errors.NewWithCode(errors.ErrNotFound)
	}

	galgame, err := s.galgameRepo.FindByID(ctx, galgameID)
	if err != nil {
		return errors.NewWithCode(errors.ErrGalgameNotFound)
	}
	if galgame.UserID != userID && !perm.Resolver.Can(roles, perm.OwnerOverride) {
		return errors.NewWithCode(errors.ErrGalgameForbidden)
	}

	proposalID, err := s.bridge.ResolveProposalID(ctx, galgameID, prID)
	if err != nil {
		return errors.NewWithCode(errors.ErrNotFound)
	}

	if err := s.edit.DeclineProposal(ctx, proposalID, editActor(userID, 2, perm.EditGameReview), ""); err != nil {
		if err == editing.ErrNotOpen {
			return fmt.Errorf("PR 已被处理")
		}
		return mapEngineWriteError(err)
	}
	return nil
}

// ListPRs returns PRs for a galgame.
func (s *GalgameService) ListPRs(ctx context.Context, galgameID, page, limit int) ([]model.GalgamePR, int64, error) {
	if page <= 0 {
		page = 1
	}
	if limit <= 0 {
		limit = 20
	}
	return s.bridge.ListPRs(ctx, galgameID, page, limit)
}

// GetPR returns a PR by ID, scoped to its galgame (route contract: the PR
// must belong to :gid, else 404).
func (s *GalgameService) GetPR(ctx context.Context, galgameID, prID int) (*model.GalgamePR, error) {
	pr, err := s.bridge.GetPR(ctx, galgameID, prID)
	if err != nil {
		return nil, errors.NewWithCode(errors.ErrNotFound)
	}
	return pr, nil
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
