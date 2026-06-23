package repository

import (
	"context"

	"api/internal/platform/galgame/intronorm"
	"api/internal/platform/galgame/model"

	"gorm.io/gorm"
)

// RevisionRepository handles revision data access
type RevisionRepository struct {
	db *gorm.DB
}

// NewRevisionRepository creates a new RevisionRepository
func NewRevisionRepository(db *gorm.DB) *RevisionRepository {
	return &RevisionRepository{db: db}
}

// NextRevision returns the next revision number for a galgame (must be called inside a transaction)
func NextRevision(tx *gorm.DB, galgameID int) (int, error) {
	var maxRevision int
	err := tx.Model(&model.GalgameRevision{}).
		Where("galgame_id = ?", galgameID).
		Select("COALESCE(MAX(revision), 0)").
		Scan(&maxRevision).Error
	if err != nil {
		return 0, err
	}
	return maxRevision + 1, nil
}

// List returns a paginated list of revisions for a galgame
func (r *RevisionRepository) List(ctx context.Context, galgameID, page, limit int, includeMinor bool) ([]model.GalgameRevision, int64, error) {
	var items []model.GalgameRevision
	var total int64

	query := r.db.WithContext(ctx).Model(&model.GalgameRevision{}).Where("galgame_id = ?", galgameID)
	if !includeMinor {
		query = query.Where("is_minor = false")
	}

	query.Count(&total)

	err := query.
		Order("revision DESC").
		Offset((page - 1) * limit).
		Limit(limit).
		Find(&items).Error

	return items, total, err
}

// ListRecentMerged returns merged revisions (a "PR landed = edit applied"
// event) with id > sinceID, id-ascending — a linear since-id feed for
// downstream activity timelines (kungal/moyu). UserID on a merged revision is
// the editor (= pr.UserID at merge), so it's the correct actor. No galgame
// status gate here: the consumer drops events whose galgame brief it can't see
// (banned / NSFW), same contract as /messages/feed.
func (r *RevisionRepository) ListRecentMerged(ctx context.Context, sinceID int64, limit int) ([]model.GalgameRevision, error) {
	var items []model.GalgameRevision
	q := r.db.WithContext(ctx).Model(&model.GalgameRevision{}).
		Where("action = ?", "merged")
	if sinceID > 0 {
		q = q.Where("id > ?", sinceID)
	}
	// Project only the feed columns — never the heavy `snapshot` jsonb. The
	// since_id=0 backfill can scan up to 50×1000 rows; loading a full galgame
	// snapshot per row just to drop it in the DTO would read tens of MB for
	// nothing.
	err := q.Select("id", "galgame_id", "revision", "user_id", "action", "created").
		Order("id ASC").Limit(limit).Find(&items).Error
	return items, err
}

// FindByRevision finds a specific revision
func (r *RevisionRepository) FindByRevision(ctx context.Context, galgameID, revision int) (*model.GalgameRevision, error) {
	var rev model.GalgameRevision
	err := r.db.WithContext(ctx).
		Where("galgame_id = ? AND revision = ?", galgameID, revision).
		First(&rev).Error
	return &rev, err
}

// FindLatest returns the latest revision for a galgame
func (r *RevisionRepository) FindLatest(ctx context.Context, galgameID int) (*model.GalgameRevision, error) {
	var rev model.GalgameRevision
	err := r.db.WithContext(ctx).
		Where("galgame_id = ?", galgameID).
		Order("revision DESC").
		First(&rev).Error
	return &rev, err
}

// ApplySnapshot applies a snapshot to the galgame table and all relation tables.
// Must be called inside a transaction. Strategy: update scalar fields; reconcile
// the set-valued relations (alias/tag/official/engine) by delta via reconcileSet
// (only added/removed rows touched); clear-and-rebuild the order-significant /
// per-row-payload relations (links/covers/screenshots).
//
// Banner image is now sourced solely from galgame_cover (sort_order=0);
// the legacy galgame.banner_image_hash column was retired by PR5.
func ApplySnapshot(tx *gorm.DB, galgameID, userID int, snapshot *model.Snapshot) error {
	// 1. Update galgame scalar fields
	updates := map[string]any{
		"vndb_id":           snapshot.VNDBID,
		"bid":               snapshot.BangumiID,
		"release_date":      model.ParseSnapshotReleaseDate(snapshot.ReleaseDate),
		"release_date_tba": snapshot.ReleaseDateTBA,
		"name_en_us":        snapshot.NameEnUS,
		"name_ja_jp":        snapshot.NameJaJP,
		"name_zh_cn":        snapshot.NameZhCN,
		"name_zh_tw":        snapshot.NameZhTW,
		"banner":            snapshot.Banner,
		// Enforce "no images in intros — use the gallery" at the single write
		// path, for all languages and every caller (create / submit / edit /
		// PR-merge / revert). Images-free intros pass through verbatim.
		"intro_en_us":       intronorm.StripImages(snapshot.IntroEnUS),
		"intro_ja_jp":       intronorm.StripImages(snapshot.IntroJaJP),
		"intro_zh_cn":       intronorm.StripImages(snapshot.IntroZhCN),
		"intro_zh_tw":       intronorm.StripImages(snapshot.IntroZhTW),
		"content_limit":     snapshot.ContentLimit,
		"original_language": snapshot.OriginalLanguage,
		"age_limit":         snapshot.AgeLimit,
		"series_id":         snapshot.SeriesID,
	}
	if err := tx.Model(&model.Galgame{}).Where("id = ?", galgameID).Updates(updates).Error; err != nil {
		return err
	}

	// 2-5. Reconcile the set-valued relations (aliases / tags / officials /
	// engines) to match the snapshot. These are order-independent sets that the
	// snapshot canonical-sorts (§1.5 #6), so we write only the delta instead of
	// delete-all + per-row rebuild — identical end state, far less churn on
	// bulk edits. Still a complete authoritative write (§1.5 #2). See
	// reconcileSet. Links / covers / screenshots below intentionally keep the
	// clear-and-rebuild path (order-significant / mutable per-row payload).
	if err := reconcileSet(tx, "galgame_id", galgameID, "name", snapshot.Aliases,
		func(name string) model.GalgameAlias {
			return model.GalgameAlias{GalgameID: galgameID, Name: name}
		}); err != nil {
		return err
	}
	if err := reconcileSet(tx, "galgame_id", galgameID, "tag_id", snapshot.TagIDs,
		func(id int) model.GalgameTagRelation {
			return model.GalgameTagRelation{GalgameID: galgameID, TagID: id}
		}); err != nil {
		return err
	}
	if err := reconcileSet(tx, "galgame_id", galgameID, "official_id", snapshot.OfficialIDs,
		func(id int) model.GalgameOfficialRelation {
			return model.GalgameOfficialRelation{GalgameID: galgameID, OfficialID: id}
		}); err != nil {
		return err
	}
	if err := reconcileSet(tx, "galgame_id", galgameID, "engine_id", snapshot.EngineIDs,
		func(id int) model.GalgameEngineRelation {
			return model.GalgameEngineRelation{GalgameID: galgameID, EngineID: id}
		}); err != nil {
		return err
	}

	// 6. Rebuild links (use the current user as link owner)
	if err := tx.Where("galgame_id = ?", galgameID).Delete(&model.GalgameLink{}).Error; err != nil {
		return err
	}
	for _, link := range snapshot.Links {
		if err := tx.Create(&model.GalgameLink{
			GalgameID: galgameID,
			UserID:    userID,
			Name:      link.Name,
			Link:      link.Link,
			Source:    link.Source,
			SourceKey: link.SourceKey,
		}).Error; err != nil {
			return err
		}
	}

	// 7. Rebuild cover candidate set. Same clear-and-rebuild discipline as
	// the other relations — `galgame_cover` is a fully editable Snapshot
	// field, not a derived index. Partial unique index
	// `idx_galgame_cover_pinned` (galgame_id) WHERE sort_order=0 is
	// enforced by Postgres: the snapshot must contain at most one row
	// with SortOrder=0 per galgame, or this Create fails.
	if err := tx.Where("galgame_id = ?", galgameID).Delete(&model.GalgameCover{}).Error; err != nil {
		return err
	}
	for _, c := range snapshot.Covers {
		if err := tx.Create(&model.GalgameCover{
			GalgameID: galgameID,
			ImageHash: c.ImageHash,
			SortOrder: c.SortOrder,
			Sexual:    c.Sexual,
			Violence:  c.Violence,
			Source:    c.Source,
			SourceKey: c.SourceKey,
			Kind:      c.Kind,
		}).Error; err != nil {
			return err
		}
	}

	// 8. Rebuild gallery / screenshots.
	if err := tx.Where("galgame_id = ?", galgameID).Delete(&model.GalgameScreenshot{}).Error; err != nil {
		return err
	}
	for _, sh := range snapshot.Screenshots {
		if err := tx.Create(&model.GalgameScreenshot{
			GalgameID: galgameID,
			ImageHash: sh.ImageHash,
			SortOrder: sh.SortOrder,
			Caption:   sh.Caption,
			Sexual:    sh.Sexual,
			Violence:  sh.Violence,
			Source:    sh.Source,
			SourceKey: sh.SourceKey,
		}).Error; err != nil {
			return err
		}
	}

	return nil
}
