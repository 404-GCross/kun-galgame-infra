package repository

import (
	"context"

	"api/internal/platform/galgame/model"

	"gorm.io/gorm"
)

// TaxonomyRevisionRepository is the read-side over the polymorphic
// taxonomy_revision table. Writes happen through the per-entity
// Apply*Snapshot helpers below (called from the taxonomy services
// inside a transaction).
type TaxonomyRevisionRepository struct {
	db *gorm.DB
}

func NewTaxonomyRevisionRepository(db *gorm.DB) *TaxonomyRevisionRepository {
	return &TaxonomyRevisionRepository{db: db}
}

func (r *TaxonomyRevisionRepository) DB() *gorm.DB { return r.db }

// List returns a paginated slice of revisions for one entity row.
// Newest revision first (mirrors RevisionRepository.List for galgame).
func (r *TaxonomyRevisionRepository) List(ctx context.Context, entity string, targetID, page, limit int) ([]model.TaxonomyRevision, int64, error) {
	var items []model.TaxonomyRevision
	var total int64

	q := r.db.WithContext(ctx).
		Model(&model.TaxonomyRevision{}).
		Where("entity = ? AND target_id = ?", entity, targetID)

	q.Count(&total)

	err := q.
		Order("revision DESC").
		Offset((page - 1) * limit).
		Limit(limit).
		Find(&items).Error

	return items, total, err
}

// ListRecent returns the cross-entity "taxonomy changed" feed ordered by id ASC
// (sinceID = exclusive lower-bound cursor), optionally filtered by entity and
// action. Mirrors RevisionRepository.ListRecentMerged. The snapshot IS selected
// here — taxonomy snapshots are small (name/desc/aliases, not the heavy galgame
// snapshot jsonb) — so the service can extract the display name without a second
// query.
func (r *TaxonomyRevisionRepository) ListRecent(ctx context.Context, entity, action string, sinceID int64, limit int) ([]model.TaxonomyRevision, error) {
	var items []model.TaxonomyRevision
	q := r.db.WithContext(ctx).Model(&model.TaxonomyRevision{})
	if entity != "" {
		q = q.Where("entity = ?", entity)
	}
	if action != "" {
		q = q.Where("action = ?", action)
	}
	if sinceID > 0 {
		q = q.Where("id > ?", sinceID)
	}
	err := q.
		Select("id", "entity", "target_id", "revision", "action", "user_id", "snapshot", "created").
		Order("id ASC").
		Limit(limit).
		Find(&items).Error
	return items, err
}

// FindByEntityTargetRevision loads a specific revision for an entity
// row. Used by Revert (to load the target snapshot) and by the
// "view revision N" admin endpoint.
func (r *TaxonomyRevisionRepository) FindByEntityTargetRevision(ctx context.Context, entity string, targetID, revision int) (*model.TaxonomyRevision, error) {
	var rev model.TaxonomyRevision
	err := r.db.WithContext(ctx).
		Where("entity = ? AND target_id = ? AND revision = ?", entity, targetID, revision).
		First(&rev).Error
	return &rev, err
}

// FindLatest returns the most recent revision row for an entity. nil
// returned (with gorm.ErrRecordNotFound) when the entity has never
// been written through this revision system — callers treat that as
// "no prior revision exists yet".
func (r *TaxonomyRevisionRepository) FindLatest(ctx context.Context, entity string, targetID int) (*model.TaxonomyRevision, error) {
	var rev model.TaxonomyRevision
	err := r.db.WithContext(ctx).
		Where("entity = ? AND target_id = ?", entity, targetID).
		Order("revision DESC").
		First(&rev).Error
	return &rev, err
}

// NextTaxonomyRevision returns the next revision number for an
// (entity, target_id) pair. MUST be called inside the same transaction
// that already holds a SELECT FOR UPDATE on the main entity row
// (galgame_tag / galgame_official / galgame_engine / galgame_series) —
// the main-row lock is what serialises concurrent writers, NOT this
// function. Without that lock two writers can pick the same number
// here and collide on idx_taxonomy_revision later.
func NextTaxonomyRevision(tx *gorm.DB, entity string, targetID int) (int, error) {
	var max int
	err := tx.Model(&model.TaxonomyRevision{}).
		Where("entity = ? AND target_id = ?", entity, targetID).
		Select("COALESCE(MAX(revision), 0)").
		Scan(&max).Error
	if err != nil {
		return 0, err
	}
	return max + 1, nil
}

// ─────────────────────────── per-entity ApplySnapshot ───────────────────────────
//
// These are standalone functions (NOT methods) — they MUST be called
// inside a transaction (matching the galgame ApplySnapshot pattern).
// Each one runs the same two-step skeleton:
//
//   1. Updates the main entity row's scalar columns.
//   2. Reconciles the aliases to match the snapshot — separate-table aliases
//      (tag/official) by delta via reconcileSet (order-independent set, only
//      added/removed written); engine aliases are jsonb-inlined in step 1.
//
// The transaction is the caller's responsibility (tx parameter).
// Concurrent edits on the same entity must be serialised by the caller
// via SELECT FOR UPDATE on the main row.

// ApplyTagSnapshot updates the tag row + reconciles its galgame_tag_alias rows
// to match snap. Same set-reconcile discipline as galgame's ApplySnapshot
// (aliases are an order-independent set → only the delta is written). Caller
// has already locked galgame_tag where id=tagID.
func ApplyTagSnapshot(tx *gorm.DB, tagID int, snap *model.TagSnapshot) error {
	if err := tx.Model(&model.GalgameTag{}).
		Where("id = ?", tagID).
		Updates(map[string]any{
			"name":        snap.Name,
			"category":    snap.Category,
			"description": snap.Description,
		}).Error; err != nil {
		return err
	}
	return reconcileSet(tx, "galgame_tag_id", tagID, "name", snap.Aliases,
		func(name string) model.GalgameTagAlias {
			return model.GalgameTagAlias{GalgameTagID: tagID, Name: name}
		})
}

// ApplyOfficialSnapshot — same shape as ApplyTagSnapshot but writes
// the wider scalar set (Original/Link/Lang) and the
// galgame_official_alias relation.
func ApplyOfficialSnapshot(tx *gorm.DB, officialID int, snap *model.OfficialSnapshot) error {
	if err := tx.Model(&model.GalgameOfficial{}).
		Where("id = ?", officialID).
		Updates(map[string]any{
			"name":        snap.Name,
			"original":    snap.Original,
			"link":        snap.Link,
			"category":    snap.Category,
			"lang":        snap.Lang,
			"description": snap.Description,
		}).Error; err != nil {
		return err
	}
	return reconcileSet(tx, "galgame_official_id", officialID, "name", snap.Aliases,
		func(name string) model.GalgameOfficialAlias {
			return model.GalgameOfficialAlias{GalgameOfficialID: officialID, Name: name}
		})
}

// ApplyEngineSnapshot writes engine scalars AND the inline jsonb alias
// column. No separate alias table to rebuild.
func ApplyEngineSnapshot(tx *gorm.DB, engineID int, snap *model.EngineSnapshot) error {
	return tx.Model(&model.GalgameEngine{}).
		Where("id = ?", engineID).
		Updates(map[string]any{
			"name":        snap.Name,
			"description": snap.Description,
			"alias":       MarshalAlias(snap.Aliases),
		}).Error
}

// ApplySeriesSnapshot — only Name + Description; series has no alias
// table and membership (galgame.series_id) is owned by the galgame
// side, not by this Apply path.
func ApplySeriesSnapshot(tx *gorm.DB, seriesID int, snap *model.SeriesSnapshot) error {
	return tx.Model(&model.GalgameSeries{}).
		Where("id = ?", seriesID).
		Updates(map[string]any{
			"name":        snap.Name,
			"description": snap.Description,
		}).Error
}
