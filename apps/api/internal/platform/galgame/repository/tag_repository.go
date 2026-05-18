package repository

import (
	"context"
	"strings"

	"api/internal/platform/galgame/model"

	"gorm.io/gorm"
)

// TagRepository handles tag data access
type TagRepository struct {
	db *gorm.DB
}

// NewTagRepository creates a new TagRepository
func NewTagRepository(db *gorm.DB) *TagRepository {
	return &TagRepository{db: db}
}

// DB exposes the underlying gorm.DB for transactions
func (r *TagRepository) DB() *gorm.DB {
	return r.db
}

// List returns a paginated list of tags with galgame counts
func (r *TagRepository) List(ctx context.Context, page, limit int) ([]model.GalgameTag, int64, error) {
	var items []model.GalgameTag
	var total int64

	r.db.WithContext(ctx).Model(&model.GalgameTag{}).Count(&total)

	err := r.db.WithContext(ctx).
		Select("galgame_tag.*, COALESCE(tc.cnt, 0) AS cnt").
		Preload("Alias").
		// Count only published galgames so the list total matches the detail
		// page (FindGalgamesByTagID filters status=0).
		Joins("LEFT JOIN (SELECT r.tag_id, COUNT(*) AS cnt FROM galgame_tag_relation r JOIN galgame g ON g.id = r.galgame_id AND g.status = 0 GROUP BY r.tag_id) tc ON tc.tag_id = galgame_tag.id").
		Order("cnt DESC").
		Offset((page - 1) * limit).
		Limit(limit).
		Find(&items).Error

	return items, total, err
}

// FindByID finds a tag by ID with aliases
func (r *TagRepository) FindByID(ctx context.Context, id int) (*model.GalgameTag, error) {
	var tag model.GalgameTag
	err := r.db.WithContext(ctx).Preload("Alias").First(&tag, id).Error
	return &tag, err
}

// FindGalgamesByTagID returns galgames associated with a tag.
// If contentLimit is non-empty ("sfw" or "nsfw"), filters galgames accordingly
// so total / pagination reflects only matching entries.
func (r *TagRepository) FindGalgamesByTagID(ctx context.Context, tagID, page, limit int, sortField, sortOrder, contentLimit string) ([]model.Galgame, int64, error) {
	var galgames []model.Galgame
	var total int64

	sub := r.db.WithContext(ctx).
		Model(&model.GalgameTagRelation{}).
		Select("galgame_id").
		Where("tag_id = ?", tagID)

	query := r.db.WithContext(ctx).
		Model(&model.Galgame{}).
		Where("id IN (?) AND status = 0", sub)

	if contentLimit != "" {
		query = query.Where("content_limit = ?", contentLimit)
	}

	query.Count(&total)

	if sortField == "" {
		sortField = "resource_update_time"
	}
	if sortOrder == "" {
		sortOrder = "desc"
	}

	err := query.
		Order(sortField + " " + sortOrder).
		Offset((page - 1) * limit).
		Limit(limit).
		Find(&galgames).Error

	return galgames, total, err
}

// Search searches tags by name or alias
func (r *TagRepository) Search(ctx context.Context, terms []string) ([]model.GalgameTag, error) {
	var tags []model.GalgameTag

	query := r.db.WithContext(ctx).Model(&model.GalgameTag{})

	for _, term := range terms {
		like := "%" + strings.ToLower(term) + "%"
		aliasSubquery := r.db.Model(&model.GalgameTagAlias{}).
			Select("galgame_tag_id").
			Where("LOWER(name) LIKE ?", like)
		query = query.Where(
			"LOWER(name) LIKE ? OR id IN (?)",
			like, aliasSubquery,
		)
	}

	err := query.Preload("Alias").Limit(50).Find(&tags).Error
	return tags, err
}

// FindGalgamesByMultipleTags returns galgames matching ALL given tag IDs.
// If contentLimit is non-empty ("sfw" or "nsfw"), filters accordingly.
func (r *TagRepository) FindGalgamesByMultipleTags(ctx context.Context, tagIDs []int, page, limit int, contentLimit string) ([]model.Galgame, int64, error) {
	var galgames []model.Galgame
	var total int64

	// Galgames that have ALL specified tags
	sub := r.db.WithContext(ctx).
		Model(&model.GalgameTagRelation{}).
		Select("galgame_id").
		Where("tag_id IN ?", tagIDs).
		Group("galgame_id").
		Having("COUNT(DISTINCT tag_id) = ?", len(tagIDs))

	query := r.db.WithContext(ctx).
		Model(&model.Galgame{}).
		Where("id IN (?) AND status = 0", sub)

	if contentLimit != "" {
		query = query.Where("content_limit = ?", contentLimit)
	}

	query.Count(&total)

	err := query.
		Order("resource_update_time DESC").
		Offset((page - 1) * limit).
		Limit(limit).
		Find(&galgames).Error

	return galgames, total, err
}

// Update updates a tag and replaces its aliases in a transaction
func (r *TagRepository) Update(ctx context.Context, tagID int, updates map[string]any, aliases []string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(updates) > 0 {
			if err := tx.Model(&model.GalgameTag{}).Where("id = ?", tagID).Updates(updates).Error; err != nil {
				return err
			}
		}

		if aliases != nil {
			if err := tx.Where("galgame_tag_id = ?", tagID).Delete(&model.GalgameTagAlias{}).Error; err != nil {
				return err
			}
			for _, name := range aliases {
				name = strings.TrimSpace(name)
				if name == "" {
					continue
				}
				if err := tx.Create(&model.GalgameTagAlias{
					GalgameTagID: tagID,
					Name:         name,
				}).Error; err != nil {
					return err
				}
			}
		}

		return nil
	})
}

// ExistsByName reports whether a tag with this exact name already exists.
// The unique index on name is the real guard; this gives a clean
// pre-check so the handler can return a friendly "already exists".
func (r *TagRepository) ExistsByName(ctx context.Context, name string) (bool, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&model.GalgameTag{}).
		Where("name = ?", name).Count(&n).Error
	return n > 0, err
}

// Create inserts a new tag plus its aliases in one transaction.
func (r *TagRepository) Create(ctx context.Context, tag *model.GalgameTag, aliases []string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(tag).Error; err != nil {
			return err
		}
		for _, name := range aliases {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if err := tx.Create(&model.GalgameTagAlias{
				GalgameTagID: tag.ID,
				Name:         name,
			}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// CountReferences returns how many galgame relations and how many alias
// rows point at this tag. Used by the delete gate: a plain delete is
// refused while relations > 0; a force-purge clears them first.
func (r *TagRepository) CountReferences(ctx context.Context, id int) (relations, aliases int64, err error) {
	if err = r.db.WithContext(ctx).Model(&model.GalgameTagRelation{}).
		Where("tag_id = ?", id).Count(&relations).Error; err != nil {
		return
	}
	err = r.db.WithContext(ctx).Model(&model.GalgameTagAlias{}).
		Where("galgame_tag_id = ?", id).Count(&aliases).Error
	return
}

// Delete removes a tag with its aliases and galgame relations.
func (r *TagRepository) Delete(ctx context.Context, id int) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("tag_id = ?", id).
			Delete(&model.GalgameTagRelation{}).Error; err != nil {
			return err
		}
		if err := tx.Where("galgame_tag_id = ?", id).
			Delete(&model.GalgameTagAlias{}).Error; err != nil {
			return err
		}
		return tx.Delete(&model.GalgameTag{}, id).Error
	})
}
