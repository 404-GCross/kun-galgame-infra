package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/repository"
	apperr "api/pkg/errors"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// TaxonomyService owns the full Create / Update / Delete / Revert
// lifecycle for tag / official / engine / series. Every mutating path
// writes a row to the polymorphic taxonomy_revision audit table; force-
// deletes additionally write per-affected-galgame galgame_revision rows
// so the tag/official/engine disappearing from those works is itself
// auditable.
//
// Concurrency: every mutation does `SELECT FOR UPDATE` on the entity's
// main row inside the transaction so two concurrent edits cannot
// allocate the same revision number for the same target.
type TaxonomyService struct {
	tagRepo      *repository.TagRepository
	officialRepo *repository.OfficialRepository
	engineRepo   *repository.EngineRepository
	seriesRepo   *repository.SeriesRepository
	taxRevRepo   *repository.TaxonomyRevisionRepository
	galgameRepo  *repository.GalgameRepository
}

func NewTaxonomyService(
	tagRepo *repository.TagRepository,
	officialRepo *repository.OfficialRepository,
	engineRepo *repository.EngineRepository,
	seriesRepo *repository.SeriesRepository,
	taxRevRepo *repository.TaxonomyRevisionRepository,
	galgameRepo *repository.GalgameRepository,
) *TaxonomyService {
	return &TaxonomyService{
		tagRepo:      tagRepo,
		officialRepo: officialRepo,
		engineRepo:   engineRepo,
		seriesRepo:   seriesRepo,
		taxRevRepo:   taxRevRepo,
		galgameRepo:  galgameRepo,
	}
}

// ListRevisions returns the revision history of any taxonomy entity.
// Newest first; pagination same convention as galgame's RevisionRepository.
func (s *TaxonomyService) ListRevisions(ctx context.Context, entity string, targetID, page, limit int) ([]model.TaxonomyRevision, int64, error) {
	if !isValidEntity(entity) {
		return nil, 0, apperr.NewWithCode(apperr.ErrBadRequest)
	}
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 50 {
		limit = 20
	}
	return s.taxRevRepo.List(ctx, entity, targetID, page, limit)
}

// ListRecentTaxonomy returns the cross-entity "taxonomy changed" feed for
// downstream activity timelines (e.g. the forum's "new series created" card).
// Same shape/contract as GalgameService.ListRecentRevisions: id cursor +
// has_more. Each item is self-describing — the entity's display name is read
// from its revision snapshot so a consumer needs no follow-up fetch.
func (s *TaxonomyService) ListRecentTaxonomy(ctx context.Context, entity, action string, sinceID int64, limit int) (*dto.TaxonomyFeedResponse, error) {
	if limit <= 0 {
		limit = 1000
	}
	if limit > 5000 {
		limit = 5000
	}
	items, err := s.taxRevRepo.ListRecent(ctx, entity, action, sinceID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]dto.TaxonomyFeedItem, len(items))
	for i, rev := range items {
		out[i] = dto.TaxonomyFeedItem{
			ID:       int64(rev.ID),
			Entity:   rev.Entity,
			TargetID: rev.TargetID,
			Revision: rev.Revision,
			Action:   rev.Action,
			UserID:   rev.UserID,
			Name:     snapshotName(rev.Snapshot),
			Created:  rev.Created.Time().UTC().Format("2006-01-02T15:04:05Z"),
		}
	}
	return &dto.TaxonomyFeedResponse{Items: out, HasMore: len(items) == limit}, nil
}

// snapshotName extracts the entity's display name from a taxonomy_revision
// snapshot. All four entity snapshots (tag/official/engine/series) carry a
// top-level `name`, so one generic parse covers every entity. Returns "" on a
// missing/unparseable snapshot — callers treat that as "no name available".
func snapshotName(snapshot datatypes.JSON) string {
	if len(snapshot) == 0 {
		return ""
	}
	var s struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(snapshot, &s)
	return s.Name
}

// GetRevision returns one specific revision row by (entity, target, revision).
func (s *TaxonomyService) GetRevision(ctx context.Context, entity string, targetID, revision int) (*model.TaxonomyRevision, error) {
	if !isValidEntity(entity) {
		return nil, apperr.NewWithCode(apperr.ErrBadRequest)
	}
	rev, err := s.taxRevRepo.FindByEntityTargetRevision(ctx, entity, targetID, revision)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperr.NewWithCode(apperr.ErrNotFound)
		}
		return nil, err
	}
	return rev, nil
}

// isValidEntity gates the entity-discriminator at the API boundary;
// matches the DB CHECK constraint exactly.
func isValidEntity(e string) bool {
	switch e {
	case model.TaxonomyEntityTag, model.TaxonomyEntityOfficial,
		model.TaxonomyEntityEngine, model.TaxonomyEntitySeries:
		return true
	}
	return false
}

// ─────────────────────────── Tag ───────────────────────────

// CreateTag inserts a new galgame_tag + aliases and writes the initial
// (revision=1, action='created') row. Name uniqueness enforced.
func (s *TaxonomyService) CreateTag(ctx context.Context, userID, userRole int, req *dto.CreateTagRequest) (*model.GalgameTag, error) {
	exists, err := s.tagRepo.ExistsByName(ctx, req.Name)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, apperr.New(apperr.ErrValidationFailed, "已存在同名 tag")
	}

	var tag model.GalgameTag
	err = s.tagRepo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Insert bare scalar row first; ApplyTagSnapshot then rebuilds
		// aliases. Mirrors galgame's "insert bare → ApplySnapshot writes
		// the rest" discipline so create/update/revert share one writer.
		tag = model.GalgameTag{
			Name:        req.Name,
			Category:    req.Category,
			Description: req.Description,
		}
		if err := tx.Create(&tag).Error; err != nil {
			return err
		}
		snap := &model.TagSnapshot{
			Name:        req.Name,
			Category:    req.Category,
			Description: req.Description,
			Aliases:     canonicalAliases(req.Alias),
		}
		if err := repository.ApplyTagSnapshot(tx, tag.ID, snap); err != nil {
			return err
		}
		return writeTaxonomyRevision(tx,
			model.TaxonomyEntityTag, tag.ID,
			model.TaxonomyActionCreated, userID, userRole,
			snap, model.TagSnapshotFieldNames(),
			0, nil, "",
		)
	})
	if err != nil {
		return nil, err
	}
	return &tag, nil
}

// UpdateTag applies a presence-semantics overlay onto the current tag
// snapshot. If nothing changed, no-op (no revision written). Concurrent
// updates are serialised by SELECT FOR UPDATE on galgame_tag.id.
func (s *TaxonomyService) UpdateTag(ctx context.Context, userID, userRole int, req *dto.UpdateTagRequest) error {
	return s.tagRepo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockMainRow(tx, "galgame_tag", req.TagID); err != nil {
			return err
		}
		curTag, err := loadTagWithAliases(tx, req.TagID)
		if err != nil {
			return err
		}
		cur := model.TakeTagSnapshot(curTag)
		next := model.OverlayTag(cur, req.Name, req.Category, req.Description, req.Alias)
		changed := model.ChangedTagKeys(cur, next)
		if len(changed) == 0 {
			return nil // no-op edit; do not produce a revision
		}
		if err := repository.ApplyTagSnapshot(tx, req.TagID, next); err != nil {
			return err
		}
		return writeTaxonomyRevision(tx,
			model.TaxonomyEntityTag, req.TagID,
			model.TaxonomyActionUpdated, userID, userRole,
			next, model.SortedKeys(changed),
			0, nil, "",
		)
	})
}

// DeleteTag is the force-purge two-step: caller has already shown the
// confirmation UI with the ref count. Writes one tag deletion revision
// AND one galgame_revision per affected galgame so the tag-disappearing
// is auditable from the galgame's history too.
func (s *TaxonomyService) DeleteTag(ctx context.Context, userID, userRole int, id int) (relations, aliases int64, affected []int, err error) {
	err = s.tagRepo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := lockMainRow(tx, "galgame_tag", id); e != nil {
			return e
		}
		curTag, e := loadTagWithAliases(tx, id)
		if e != nil {
			return e
		}
		cur := model.TakeTagSnapshot(curTag)

		// Capture ref counts + affected gids BEFORE deleting.
		if e := tx.Model(&model.GalgameTagRelation{}).
			Where("tag_id = ?", id).Count(&relations).Error; e != nil {
			return e
		}
		if e := tx.Model(&model.GalgameTagAlias{}).
			Where("galgame_tag_id = ?", id).Count(&aliases).Error; e != nil {
			return e
		}
		if e := tx.Model(&model.GalgameTagRelation{}).
			Where("tag_id = ?", id).
			Pluck("galgame_id", &affected).Error; e != nil {
			return e
		}

		// Cascade-delete the entity itself.
		if e := tx.Where("tag_id = ?", id).Delete(&model.GalgameTagRelation{}).Error; e != nil {
			return e
		}
		if e := tx.Where("galgame_tag_id = ?", id).Delete(&model.GalgameTagAlias{}).Error; e != nil {
			return e
		}
		if e := tx.Delete(&model.GalgameTag{}, id).Error; e != nil {
			return e
		}

		// Taxonomy revision: deleted (with pre-delete state preserved
		// for future "undo delete" replay).
		if e := writeTaxonomyRevision(tx,
			model.TaxonomyEntityTag, id,
			model.TaxonomyActionDeleted, userID, userRole,
			cur, []string{}, // §6.5: deleted → empty changed_fields
			int(relations), affected, "",
		); e != nil {
			return e
		}

		// Galgame revisions: every previously-attached galgame had its
		// tag_ids set shrink, so each gets a galgame_revision tagged
		// changed_fields=['tag_ids']. Without these, the galgame's
		// history shows the tag "disappearing" with no record of why.
		return writeAffectedGalgameRevisions(tx, affected, userID, "tag_ids")
	})
	return
}

// RevertTag rolls a tag back to the snapshot stored in revision N. Three
// branches depending on what's already in the DB and what the target's
// action was:
//
//   - target is created/updated/reverted AND tag row exists  → overlay-apply
//   - target is created/updated/reverted AND tag row missing → resurrect
//     (INSERT then apply) — happens when a later deleted-revert is being undone
//   - target is action='deleted'                             → resurrect, same path
//
// Note: undoing a deletion does NOT auto-restore galgame_tag_relation
// rows. AffectedGalgameIDs is still stored on the deleted revision so
// admin UI can show "these 8 works used to reference this tag — pick
// which to re-attach" (each re-attach goes through standard galgame
// edit producing its own galgame_revision; see §7.2.4).
func (s *TaxonomyService) RevertTag(ctx context.Context, userID, userRole, tagID, targetRevision int) error {
	return s.tagRepo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockMainRowMayNotExist(tx, "galgame_tag", tagID); err != nil {
			return err
		}
		target, err := s.taxRevRepo.FindByEntityTargetRevision(ctx, model.TaxonomyEntityTag, tagID, targetRevision)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperr.NewWithCode(apperr.ErrNotFound)
			}
			return err
		}
		targetSnap, err := model.TagSnapshotFromJSON(target.Snapshot)
		if err != nil {
			return err
		}
		if targetSnap == nil {
			return fmt.Errorf("target revision has no snapshot")
		}

		// Does the tag row still exist? If the most recent action was
		// 'deleted', it doesn't — resurrect with an explicit INSERT.
		var count int64
		if err := tx.Model(&model.GalgameTag{}).Where("id = ?", tagID).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			// Resurrect with the target's scalar state. ApplyTagSnapshot
			// then handles aliases.
			if err := tx.Create(&model.GalgameTag{
				ID:          tagID,
				Name:        targetSnap.Name,
				Category:    targetSnap.Category,
				Description: targetSnap.Description,
			}).Error; err != nil {
				return err
			}
		}

		if err := repository.ApplyTagSnapshot(tx, tagID, targetSnap); err != nil {
			return err
		}
		return writeTaxonomyRevision(tx,
			model.TaxonomyEntityTag, tagID,
			model.TaxonomyActionReverted, userID, userRole,
			targetSnap, model.TagSnapshotFieldNames(), // full snapshot rewrite
			0, nil,
			fmt.Sprintf("回滚到版本 %d", targetRevision),
		)
	})
}

// ─────────────────────────── Official ───────────────────────────

func (s *TaxonomyService) CreateOfficial(ctx context.Context, userID, userRole int, req *dto.CreateOfficialRequest) (*model.GalgameOfficial, error) {
	exists, err := s.officialRepo.ExistsByName(ctx, req.Name)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, apperr.New(apperr.ErrValidationFailed, "已存在同名 official")
	}

	var o model.GalgameOfficial
	err = s.officialRepo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		o = model.GalgameOfficial{
			Name:        req.Name,
			Original:    req.Original,
			Link:        req.Link,
			Category:    req.Category,
			Lang:        req.Lang,
			Description: req.Description,
		}
		if err := tx.Create(&o).Error; err != nil {
			return err
		}
		snap := &model.OfficialSnapshot{
			Name:        req.Name,
			Original:    req.Original,
			Link:        req.Link,
			Category:    req.Category,
			Lang:        req.Lang,
			Description: req.Description,
			Aliases:     canonicalAliases(req.Alias),
		}
		if err := repository.ApplyOfficialSnapshot(tx, o.ID, snap); err != nil {
			return err
		}
		return writeTaxonomyRevision(tx,
			model.TaxonomyEntityOfficial, o.ID,
			model.TaxonomyActionCreated, userID, userRole,
			snap, model.OfficialSnapshotFieldNames(),
			0, nil, "",
		)
	})
	if err != nil {
		return nil, err
	}
	return &o, nil
}

func (s *TaxonomyService) UpdateOfficial(ctx context.Context, userID, userRole int, req *dto.UpdateOfficialRequest) error {
	return s.officialRepo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockMainRow(tx, "galgame_official", req.OfficialID); err != nil {
			return err
		}
		curOfficial, err := loadOfficialWithAliases(tx, req.OfficialID)
		if err != nil {
			return err
		}
		cur := model.TakeOfficialSnapshot(curOfficial)
		next := model.OverlayOfficial(cur, req.Name, req.Original, req.Link, req.Category, req.Lang, req.Description, req.Alias)
		changed := model.ChangedOfficialKeys(cur, next)
		if len(changed) == 0 {
			return nil
		}
		if err := repository.ApplyOfficialSnapshot(tx, req.OfficialID, next); err != nil {
			return err
		}
		return writeTaxonomyRevision(tx,
			model.TaxonomyEntityOfficial, req.OfficialID,
			model.TaxonomyActionUpdated, userID, userRole,
			next, model.SortedKeys(changed),
			0, nil, "",
		)
	})
}

func (s *TaxonomyService) DeleteOfficial(ctx context.Context, userID, userRole int, id int) (relations, aliases int64, affected []int, err error) {
	err = s.officialRepo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := lockMainRow(tx, "galgame_official", id); e != nil {
			return e
		}
		curO, e := loadOfficialWithAliases(tx, id)
		if e != nil {
			return e
		}
		cur := model.TakeOfficialSnapshot(curO)

		if e := tx.Model(&model.GalgameOfficialRelation{}).
			Where("official_id = ?", id).Count(&relations).Error; e != nil {
			return e
		}
		if e := tx.Model(&model.GalgameOfficialAlias{}).
			Where("galgame_official_id = ?", id).Count(&aliases).Error; e != nil {
			return e
		}
		if e := tx.Model(&model.GalgameOfficialRelation{}).
			Where("official_id = ?", id).
			Pluck("galgame_id", &affected).Error; e != nil {
			return e
		}

		if e := tx.Where("official_id = ?", id).Delete(&model.GalgameOfficialRelation{}).Error; e != nil {
			return e
		}
		if e := tx.Where("galgame_official_id = ?", id).Delete(&model.GalgameOfficialAlias{}).Error; e != nil {
			return e
		}
		if e := tx.Delete(&model.GalgameOfficial{}, id).Error; e != nil {
			return e
		}

		if e := writeTaxonomyRevision(tx,
			model.TaxonomyEntityOfficial, id,
			model.TaxonomyActionDeleted, userID, userRole,
			cur, []string{},
			int(relations), affected, "",
		); e != nil {
			return e
		}
		return writeAffectedGalgameRevisions(tx, affected, userID, "official_ids")
	})
	return
}

func (s *TaxonomyService) RevertOfficial(ctx context.Context, userID, userRole, officialID, targetRevision int) error {
	return s.officialRepo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockMainRowMayNotExist(tx, "galgame_official", officialID); err != nil {
			return err
		}
		target, err := s.taxRevRepo.FindByEntityTargetRevision(ctx, model.TaxonomyEntityOfficial, officialID, targetRevision)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperr.NewWithCode(apperr.ErrNotFound)
			}
			return err
		}
		targetSnap, err := model.OfficialSnapshotFromJSON(target.Snapshot)
		if err != nil {
			return err
		}
		if targetSnap == nil {
			return fmt.Errorf("target revision has no snapshot")
		}
		var count int64
		if err := tx.Model(&model.GalgameOfficial{}).Where("id = ?", officialID).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			if err := tx.Create(&model.GalgameOfficial{
				ID:          officialID,
				Name:        targetSnap.Name,
				Original:    targetSnap.Original,
				Link:        targetSnap.Link,
				Category:    targetSnap.Category,
				Lang:        targetSnap.Lang,
				Description: targetSnap.Description,
			}).Error; err != nil {
				return err
			}
		}
		if err := repository.ApplyOfficialSnapshot(tx, officialID, targetSnap); err != nil {
			return err
		}
		return writeTaxonomyRevision(tx,
			model.TaxonomyEntityOfficial, officialID,
			model.TaxonomyActionReverted, userID, userRole,
			targetSnap, model.OfficialSnapshotFieldNames(),
			0, nil,
			fmt.Sprintf("回滚到版本 %d", targetRevision),
		)
	})
}

// ─────────────────────────── Engine ───────────────────────────

func (s *TaxonomyService) CreateEngine(ctx context.Context, userID, userRole int, req *dto.CreateEngineRequest) (*model.GalgameEngine, error) {
	exists, err := s.engineRepo.ExistsByName(ctx, req.Name)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, apperr.New(apperr.ErrValidationFailed, "已存在同名 engine")
	}

	var e model.GalgameEngine
	err = s.engineRepo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		e = model.GalgameEngine{
			Name:        req.Name,
			Description: req.Description,
			Alias:       datatypes.JSON([]byte("[]")),
		}
		if err := tx.Create(&e).Error; err != nil {
			return err
		}
		snap := &model.EngineSnapshot{
			Name:        req.Name,
			Description: req.Description,
			Aliases:     canonicalAliases(req.Alias),
		}
		if err := repository.ApplyEngineSnapshot(tx, e.ID, snap); err != nil {
			return err
		}
		return writeTaxonomyRevision(tx,
			model.TaxonomyEntityEngine, e.ID,
			model.TaxonomyActionCreated, userID, userRole,
			snap, model.EngineSnapshotFieldNames(),
			0, nil, "",
		)
	})
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (s *TaxonomyService) UpdateEngine(ctx context.Context, userID, userRole int, req *dto.UpdateEngineRequest) error {
	return s.engineRepo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockMainRow(tx, "galgame_engine", req.EngineID); err != nil {
			return err
		}
		var curE model.GalgameEngine
		if err := tx.First(&curE, req.EngineID).Error; err != nil {
			return err
		}
		cur := model.TakeEngineSnapshot(&curE)
		next := model.OverlayEngine(cur, req.Name, req.Description, req.Alias)
		changed := model.ChangedEngineKeys(cur, next)
		if len(changed) == 0 {
			return nil
		}
		if err := repository.ApplyEngineSnapshot(tx, req.EngineID, next); err != nil {
			return err
		}
		return writeTaxonomyRevision(tx,
			model.TaxonomyEntityEngine, req.EngineID,
			model.TaxonomyActionUpdated, userID, userRole,
			next, model.SortedKeys(changed),
			0, nil, "",
		)
	})
}

func (s *TaxonomyService) DeleteEngine(ctx context.Context, userID, userRole int, id int) (relations int64, affected []int, err error) {
	err = s.engineRepo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := lockMainRow(tx, "galgame_engine", id); e != nil {
			return e
		}
		var curE model.GalgameEngine
		if e := tx.First(&curE, id).Error; e != nil {
			return e
		}
		cur := model.TakeEngineSnapshot(&curE)

		if e := tx.Model(&model.GalgameEngineRelation{}).
			Where("engine_id = ?", id).Count(&relations).Error; e != nil {
			return e
		}
		if e := tx.Model(&model.GalgameEngineRelation{}).
			Where("engine_id = ?", id).
			Pluck("galgame_id", &affected).Error; e != nil {
			return e
		}

		if e := tx.Where("engine_id = ?", id).Delete(&model.GalgameEngineRelation{}).Error; e != nil {
			return e
		}
		if e := tx.Delete(&model.GalgameEngine{}, id).Error; e != nil {
			return e
		}

		if e := writeTaxonomyRevision(tx,
			model.TaxonomyEntityEngine, id,
			model.TaxonomyActionDeleted, userID, userRole,
			cur, []string{},
			int(relations), affected, "",
		); e != nil {
			return e
		}
		return writeAffectedGalgameRevisions(tx, affected, userID, "engine_ids")
	})
	return
}

func (s *TaxonomyService) RevertEngine(ctx context.Context, userID, userRole, engineID, targetRevision int) error {
	return s.engineRepo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockMainRowMayNotExist(tx, "galgame_engine", engineID); err != nil {
			return err
		}
		target, err := s.taxRevRepo.FindByEntityTargetRevision(ctx, model.TaxonomyEntityEngine, engineID, targetRevision)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperr.NewWithCode(apperr.ErrNotFound)
			}
			return err
		}
		targetSnap, err := model.EngineSnapshotFromJSON(target.Snapshot)
		if err != nil {
			return err
		}
		if targetSnap == nil {
			return fmt.Errorf("target revision has no snapshot")
		}
		var count int64
		if err := tx.Model(&model.GalgameEngine{}).Where("id = ?", engineID).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			if err := tx.Create(&model.GalgameEngine{
				ID:          engineID,
				Name:        targetSnap.Name,
				Description: targetSnap.Description,
				Alias:       datatypes.JSON([]byte("[]")),
			}).Error; err != nil {
				return err
			}
		}
		if err := repository.ApplyEngineSnapshot(tx, engineID, targetSnap); err != nil {
			return err
		}
		return writeTaxonomyRevision(tx,
			model.TaxonomyEntityEngine, engineID,
			model.TaxonomyActionReverted, userID, userRole,
			targetSnap, model.EngineSnapshotFieldNames(),
			0, nil,
			fmt.Sprintf("回滚到版本 %d", targetRevision),
		)
	})
}

// ─────────────────────────── Series ───────────────────────────
//
// Series is the special-case entity: galgame membership lives on
// galgame.series_id (galgame-side FK), so changing "which galgames
// belong to this series" is an EDIT OF EACH AFFECTED GALGAME, recorded
// in galgame_revision. Series_revision only sees its own Name +
// Description (per §7.5).

func (s *TaxonomyService) CreateSeries(ctx context.Context, userID, userRole int, req *dto.CreateSeriesRequest, galgameIDs []int) (*model.GalgameSeries, error) {
	// Pre-check duplicate name (mirrors CreateTag/CreateOfficial/CreateEngine)
	// so a same-name create returns a friendly 400 instead of a raw 500 from
	// the unique-index violation. The unique index stays the race backstop.
	exists, err := s.seriesRepo.ExistsByName(ctx, req.Name)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, apperr.New(apperr.ErrValidationFailed, "已存在同名系列")
	}

	var sr model.GalgameSeries
	err = s.seriesRepo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		sr = model.GalgameSeries{
			Name:        req.Name,
			Description: req.Description,
		}
		if err := tx.Create(&sr).Error; err != nil {
			return err
		}
		snap := &model.SeriesSnapshot{
			Name:        req.Name,
			Description: req.Description,
		}
		// Series row has no relation-table to rebuild — Apply is a
		// scalar Updates; called for consistency with the other
		// entities so all create paths share one shape.
		if err := repository.ApplySeriesSnapshot(tx, sr.ID, snap); err != nil {
			return err
		}
		if err := writeTaxonomyRevision(tx,
			model.TaxonomyEntitySeries, sr.ID,
			model.TaxonomyActionCreated, userID, userRole,
			snap, model.SeriesSnapshotFieldNames(),
			0, nil, "",
		); err != nil {
			return err
		}
		// Initial membership: each gid added gets its OWN galgame
		// revision with changed_fields=['series_id'].
		if len(galgameIDs) > 0 {
			if err := tx.Model(&model.Galgame{}).
				Where("id IN ?", galgameIDs).
				Update("series_id", sr.ID).Error; err != nil {
				return err
			}
			if err := writeAffectedGalgameRevisions(tx, galgameIDs, userID, "series_id"); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &sr, nil
}

// UpdateSeries reconciles BOTH the series scalar fields AND its
// galgame membership in one transaction. The two pieces of work
// produce DIFFERENT revisions:
//
//   - Name/Description change → taxonomy_revision (entity='series', action='updated')
//   - galgame membership change → ONE galgame_revision PER affected
//     galgame with changed_fields=['series_id']
//
// Both can happen in the same request; both can independently be
// no-ops.
func (s *TaxonomyService) UpdateSeries(ctx context.Context, userID, userRole int, req *dto.UpdateSeriesRequest) error {
	return s.seriesRepo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockMainRow(tx, "galgame_series", req.SeriesID); err != nil {
			return err
		}
		var curSr model.GalgameSeries
		if err := tx.First(&curSr, req.SeriesID).Error; err != nil {
			return err
		}
		cur := model.TakeSeriesSnapshot(&curSr)
		next := model.OverlaySeries(cur, req.Name, req.Description)
		changed := model.ChangedSeriesKeys(cur, next)
		if len(changed) > 0 {
			if err := repository.ApplySeriesSnapshot(tx, req.SeriesID, next); err != nil {
				return err
			}
			if err := writeTaxonomyRevision(tx,
				model.TaxonomyEntitySeries, req.SeriesID,
				model.TaxonomyActionUpdated, userID, userRole,
				next, model.SortedKeys(changed),
				0, nil, "",
			); err != nil {
				return err
			}
		}
		if req.GalgameIDs != nil {
			if err := reconcileSeriesMembership(tx, req.SeriesID, *req.GalgameIDs, userID); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *TaxonomyService) DeleteSeries(ctx context.Context, userID, userRole int, id int) (relations int64, affected []int, err error) {
	err = s.seriesRepo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := lockMainRow(tx, "galgame_series", id); e != nil {
			return e
		}
		var curSr model.GalgameSeries
		if e := tx.First(&curSr, id).Error; e != nil {
			return e
		}
		cur := model.TakeSeriesSnapshot(&curSr)

		// Series membership lives on galgame.series_id, not in a relation
		// table. So "ref count" is "how many galgame rows pointed at
		// this series", and the cleanup is UPDATE galgame SET series_id=null.
		if e := tx.Model(&model.Galgame{}).
			Where("series_id = ?", id).Count(&relations).Error; e != nil {
			return e
		}
		if e := tx.Model(&model.Galgame{}).
			Where("series_id = ?", id).
			Pluck("id", &affected).Error; e != nil {
			return e
		}
		if e := tx.Model(&model.Galgame{}).
			Where("series_id = ?", id).
			Update("series_id", nil).Error; e != nil {
			return e
		}
		if e := tx.Delete(&model.GalgameSeries{}, id).Error; e != nil {
			return e
		}

		if e := writeTaxonomyRevision(tx,
			model.TaxonomyEntitySeries, id,
			model.TaxonomyActionDeleted, userID, userRole,
			cur, []string{},
			int(relations), affected, "",
		); e != nil {
			return e
		}
		return writeAffectedGalgameRevisions(tx, affected, userID, "series_id")
	})
	return
}

func (s *TaxonomyService) RevertSeries(ctx context.Context, userID, userRole, seriesID, targetRevision int) error {
	return s.seriesRepo.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockMainRowMayNotExist(tx, "galgame_series", seriesID); err != nil {
			return err
		}
		target, err := s.taxRevRepo.FindByEntityTargetRevision(ctx, model.TaxonomyEntitySeries, seriesID, targetRevision)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperr.NewWithCode(apperr.ErrNotFound)
			}
			return err
		}
		targetSnap, err := model.SeriesSnapshotFromJSON(target.Snapshot)
		if err != nil {
			return err
		}
		if targetSnap == nil {
			return fmt.Errorf("target revision has no snapshot")
		}
		var count int64
		if err := tx.Model(&model.GalgameSeries{}).Where("id = ?", seriesID).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			if err := tx.Create(&model.GalgameSeries{
				ID:          seriesID,
				Name:        targetSnap.Name,
				Description: targetSnap.Description,
			}).Error; err != nil {
				return err
			}
		}
		if err := repository.ApplySeriesSnapshot(tx, seriesID, targetSnap); err != nil {
			return err
		}
		return writeTaxonomyRevision(tx,
			model.TaxonomyEntitySeries, seriesID,
			model.TaxonomyActionReverted, userID, userRole,
			targetSnap, model.SeriesSnapshotFieldNames(),
			0, nil,
			fmt.Sprintf("回滚到版本 %d", targetRevision),
		)
	})
}

// ─────────────────────────── helpers ───────────────────────────

// lockMainRow takes a row-level lock on the given table+id so concurrent
// writes to the SAME entity serialise (= NextTaxonomyRevision's max+1
// can't collide). The lock is held for the duration of the transaction.
//
// Returns ErrNotFound when the row does not exist — callers that need
// the row to be present anyway (Update/Delete) treat this as the entity
// being absent.
func lockMainRow(tx *gorm.DB, table string, id int) error {
	var one int
	err := tx.Raw(`SELECT 1 FROM `+table+` WHERE id = ? FOR UPDATE`, id).Scan(&one).Error
	if err != nil {
		return err
	}
	if one == 0 {
		return apperr.NewWithCode(apperr.ErrNotFound)
	}
	return nil
}

// lockMainRowMayNotExist is lockMainRow's variant for Revert paths,
// where the row legitimately MAY not exist yet (= we're undoing a
// delete and about to INSERT it back). Returns nil even when missing;
// concurrent revert+insert can't race because the
// (entity, target_id, revision) unique index catches duplicates and
// the resurrect INSERT below will FK-fail if a parallel writer already
// claimed the row.
func lockMainRowMayNotExist(tx *gorm.DB, table string, id int) error {
	var one int
	_ = tx.Raw(`SELECT 1 FROM `+table+` WHERE id = ? FOR UPDATE`, id).Scan(&one).Error
	return nil
}

// canonicalAliases ensures the slice is non-nil and sorted, matching
// what TakeXSnapshot produces; keeps snapshot diffs stable across
// "user sent ['B','A']" vs the canonical "['A','B']".
func canonicalAliases(in []string) []string {
	if in == nil {
		return []string{}
	}
	out := append([]string(nil), in...)
	// reuse sort.Strings indirectly via model package — re-importing here
	// would create a cycle, so inline.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// loadTagWithAliases is the read path for tag + its alias rows used by
// UpdateTag and DeleteTag. Tx-scoped so the SELECT sees the row we
// just locked via FOR UPDATE.
func loadTagWithAliases(tx *gorm.DB, tagID int) (*model.GalgameTag, error) {
	var t model.GalgameTag
	if err := tx.Preload("Alias").First(&t, tagID).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

func loadOfficialWithAliases(tx *gorm.DB, officialID int) (*model.GalgameOfficial, error) {
	var o model.GalgameOfficial
	if err := tx.Preload("Alias").First(&o, officialID).Error; err != nil {
		return nil, err
	}
	return &o, nil
}

// writeTaxonomyRevision is the single INSERT-into-taxonomy_revision
// path. Centralises NextTaxonomyRevision + ChangedFields encoding +
// AffectedGalgameIDs encoding so every entity flow shares one bug
// surface.
//
// snapshot is any *model.XSnapshot — marshalled via encoding/json so
// the union jsonb column shape stays type-free at the DB level.
func writeTaxonomyRevision(
	tx *gorm.DB,
	entity string, targetID int,
	action string, userID, userRole int,
	snapshot any,
	changedFields []string,
	refCount int, affectedGalgameIDs []int,
	note string,
) error {
	rev, err := repository.NextTaxonomyRevision(tx, entity, targetID)
	if err != nil {
		return err
	}
	snapJSON, err := marshalSnapshot(snapshot)
	if err != nil {
		return err
	}
	row := &model.TaxonomyRevision{
		Entity:   entity,
		TargetID: targetID,
		Revision: rev,
		Action:   action,
		UserID:   userID,
		UserRole: userRole,
		Snapshot: snapJSON,
		RefCount: refCount,
		Note:     note,
	}
	row.SetChangedFields(changedFields)
	row.SetAffectedGalgameIDs(affectedGalgameIDs)
	return tx.Create(row).Error
}

// marshalSnapshot serialises any per-entity snapshot struct to jsonb
// bytes. Keeps the per-entity Snapshot types decoupled from this file
// (= one switch on the writer side instead of N).
func marshalSnapshot(s any) (datatypes.JSON, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	return datatypes.JSON(b), nil
}

// writeAffectedGalgameRevisions iterates over a list of galgame ids
// (typically the set whose relation row was just purged by a taxonomy
// delete) and writes ONE galgame_revision row per gid with
// action='updated' and changed_fields=['tag_ids' | 'official_ids' |
// 'engine_ids' | 'series_id']. The snapshot stored is freshly re-taken
// from the galgame's post-update state so the canonical
// "revision.snapshot == DB == intent" invariant holds.
//
// changedKey identifies WHICH per-galgame field was effectively changed
// by the taxonomy operation (e.g., a tag deletion → 'tag_ids').
func writeAffectedGalgameRevisions(tx *gorm.DB, gids []int, userID int, changedKey string) error {
	for _, gid := range gids {
		full, err := loadGalgameWithRelations(tx, gid)
		if err != nil {
			// galgame may have been deleted in the same transaction
			// (shouldn't happen for taxonomy delete, but defensive).
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return err
		}
		snap := model.TakeSnapshot(full)
		snapJSON, err := snap.ToJSON()
		if err != nil {
			return err
		}
		nextRev, err := repository.NextRevision(tx, gid)
		if err != nil {
			return err
		}
		rippleRev := &model.GalgameRevision{
			GalgameID: gid,
			Revision:  nextRev,
			UserID:    userID,
			Action:    "updated",
			Snapshot:  snapJSON,
			Note:      fmt.Sprintf("taxonomy delete cascade: %s", changedKey),
		}
		// The taxonomy op changed exactly one per-galgame relation field.
		rippleRev.SetChangedFields([]string{changedKey})
		if err := tx.Create(rippleRev).Error; err != nil {
			return err
		}
	}
	return nil
}

// reconcileSeriesMembership diffs (currently in series N) vs (caller-
// supplied set) and applies the difference via per-galgame UPDATE.
// Each affected galgame gets a galgame_revision with
// changed_fields=['series_id']. Matches §7.5.2.
func reconcileSeriesMembership(tx *gorm.DB, seriesID int, want []int, userID int) error {
	var cur []int
	if err := tx.Model(&model.Galgame{}).
		Where("series_id = ?", seriesID).
		Pluck("id", &cur).Error; err != nil {
		return err
	}
	curSet := make(map[int]bool, len(cur))
	for _, id := range cur {
		curSet[id] = true
	}
	wantSet := make(map[int]bool, len(want))
	for _, id := range want {
		wantSet[id] = true
	}
	affected := make([]int, 0)
	for _, id := range want {
		if !curSet[id] {
			if err := tx.Model(&model.Galgame{}).
				Where("id = ?", id).
				Update("series_id", seriesID).Error; err != nil {
				return err
			}
			affected = append(affected, id)
		}
	}
	for _, id := range cur {
		if !wantSet[id] {
			if err := tx.Model(&model.Galgame{}).
				Where("id = ?", id).
				Update("series_id", nil).Error; err != nil {
				return err
			}
			affected = append(affected, id)
		}
	}
	return writeAffectedGalgameRevisions(tx, affected, userID, "series_id")
}
