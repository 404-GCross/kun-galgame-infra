package repository

import (
	"context"

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
// Must be called inside a transaction. Strategy: update scalar fields, clear+rebuild relations.
// snapshotBannerHashColumn maps Snapshot.BannerImageHash (a plain string)
// to the *string column representation: empty string becomes NULL.
func snapshotBannerHashColumn(s *model.Snapshot) any {
	if s.BannerImageHash == "" {
		return nil
	}
	return s.BannerImageHash
}

func ApplySnapshot(tx *gorm.DB, galgameID, userID int, snapshot *model.Snapshot) error {
	// 1. Update galgame scalar fields
	updates := map[string]any{
		"vndb_id":           snapshot.VNDBID,
		"bid":               snapshot.BangumiID,
		"released":          snapshot.Released,
		"name_en_us":        snapshot.NameEnUS,
		"name_ja_jp":        snapshot.NameJaJP,
		"name_zh_cn":        snapshot.NameZhCN,
		"name_zh_tw":        snapshot.NameZhTW,
		"banner":            snapshot.Banner,
		"banner_image_hash": snapshotBannerHashColumn(snapshot),
		"intro_en_us":       snapshot.IntroEnUS,
		"intro_ja_jp":       snapshot.IntroJaJP,
		"intro_zh_cn":       snapshot.IntroZhCN,
		"intro_zh_tw":       snapshot.IntroZhTW,
		"content_limit":     snapshot.ContentLimit,
		"original_language": snapshot.OriginalLanguage,
		"age_limit":         snapshot.AgeLimit,
		"series_id":         snapshot.SeriesID,
	}
	if err := tx.Model(&model.Galgame{}).Where("id = ?", galgameID).Updates(updates).Error; err != nil {
		return err
	}

	// 2. Rebuild aliases
	if err := tx.Where("galgame_id = ?", galgameID).Delete(&model.GalgameAlias{}).Error; err != nil {
		return err
	}
	for _, name := range snapshot.Aliases {
		if err := tx.Create(&model.GalgameAlias{GalgameID: galgameID, Name: name}).Error; err != nil {
			return err
		}
	}

	// 3. Rebuild tag relations
	if err := tx.Where("galgame_id = ?", galgameID).Delete(&model.GalgameTagRelation{}).Error; err != nil {
		return err
	}
	for _, tagID := range snapshot.TagIDs {
		if err := tx.Create(&model.GalgameTagRelation{GalgameID: galgameID, TagID: tagID}).Error; err != nil {
			return err
		}
	}

	// 4. Rebuild official relations
	if err := tx.Where("galgame_id = ?", galgameID).Delete(&model.GalgameOfficialRelation{}).Error; err != nil {
		return err
	}
	for _, officialID := range snapshot.OfficialIDs {
		if err := tx.Create(&model.GalgameOfficialRelation{GalgameID: galgameID, OfficialID: officialID}).Error; err != nil {
			return err
		}
	}

	// 5. Rebuild engine relations
	if err := tx.Where("galgame_id = ?", galgameID).Delete(&model.GalgameEngineRelation{}).Error; err != nil {
		return err
	}
	for _, engineID := range snapshot.EngineIDs {
		if err := tx.Create(&model.GalgameEngineRelation{GalgameID: galgameID, EngineID: engineID}).Error; err != nil {
			return err
		}
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
		}).Error; err != nil {
			return err
		}
	}

	return nil
}
