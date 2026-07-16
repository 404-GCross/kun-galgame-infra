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

// List returns a paginated list of tags with galgame counts.
//
// contentLimit gates which tag categories are returned — the "sexual" category
// is the NSFW tag set, so "sfw" hides it (safe-by-default, handbook §16), "nsfw"
// returns only it, and "" (= all) returns every category. The filter is applied
// to BOTH the count and the page so total + pagination reflect the filtered set
// — this lets downstream sites forward page/limit/content_limit and proxy total
// for real server-side pagination instead of over-fetching every page + doing
// client-side filtering (handbook §16: gating belongs in the query, not the RP).
func (r *TagRepository) List(ctx context.Context, page, limit int, contentLimit string) ([]model.GalgameTag, int64, error) {
	var items []model.GalgameTag
	var total int64

	applyCategory := func(q *gorm.DB) *gorm.DB {
		switch contentLimit {
		case "sfw":
			return q.Where("galgame_tag.category <> ?", "sexual")
		case "nsfw":
			return q.Where("galgame_tag.category = ?", "sexual")
		default: // "" = all
			return q
		}
	}

	applyCategory(r.db.WithContext(ctx).Model(&model.GalgameTag{})).Count(&total)

	err := applyCategory(r.db.WithContext(ctx).
		Select("galgame_tag.*, COALESCE(tc.cnt, 0) AS cnt").
		Preload("Alias").
		// Count only published galgames so the list total matches the detail
		// page (FindGalgamesByTagID filters status=0).
		Joins("LEFT JOIN (SELECT r.tag_id, COUNT(*) AS cnt FROM galgame_tag_relation r JOIN galgame g ON g.id = r.galgame_id AND g.status = 0 GROUP BY r.tag_id) tc ON tc.tag_id = galgame_tag.id")).
		Order("cnt DESC").
		Offset((page - 1) * limit).
		Limit(limit).
		Find(&items).Error

	return items, total, err
}

// FindByID finds a tag by ID with aliases and the published galgame count.
// The cnt LEFT JOIN mirrors List so the detail-page header count matches the
// list view (a plain First leaves the read-only cnt column at 0).
func (r *TagRepository) FindByID(ctx context.Context, id int) (*model.GalgameTag, error) {
	var tag model.GalgameTag
	err := r.db.WithContext(ctx).
		Select("galgame_tag.*, COALESCE(tc.cnt, 0) AS cnt").
		Preload("Alias").
		Joins("LEFT JOIN (SELECT r.tag_id, COUNT(*) AS cnt FROM galgame_tag_relation r JOIN galgame g ON g.id = r.galgame_id AND g.status = 0 GROUP BY r.tag_id) tc ON tc.tag_id = galgame_tag.id").
		First(&tag, id).Error
	return &tag, err
}

// GalgameIDsByTagID returns the ids of every PUBLISHED galgame carrying this
// tag (ids only — no metadata, no pagination). The downstream forum intersects
// these with its local galgames and runs its own resource-based filtering
// (platform/language/有无下载), which the wiki has no concept of.
func (r *TagRepository) GalgameIDsByTagID(ctx context.Context, tagID int) ([]int, error) {
	var ids []int
	err := r.db.WithContext(ctx).
		Table("galgame_tag_relation r").
		Joins("JOIN galgame g ON g.id = r.galgame_id AND g.status = 0").
		Where("r.tag_id = ?", tagID).
		// Deterministic order: without it the bare-ids body leaks heap order,
		// which flaps per query under concurrent scans (prod W2 replay caught it).
		Order("r.galgame_id ASC").
		Pluck("r.galgame_id", &ids).Error
	return ids, err
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

	// Whitelist the sort field/order BEFORE concatenation — the DTO oneof
	// never runs (no app-wide StructValidator), so an attacker-controlled
	// sort_field/sort_order would otherwise be interpolated raw into ORDER BY.
	// Matches the galgame_repository.List guard + the tag_dto oneof.
	allowedSortFields := map[string]bool{
		"created":              true,
		"resource_update_time": true,
		"view":                 true,
	}
	if !allowedSortFields[sortField] {
		sortField = "resource_update_time"
	}
	if sortOrder != "asc" && sortOrder != "desc" {
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
			cleaned := make([]string, 0, len(aliases))
			for _, name := range aliases {
				if name = strings.TrimSpace(name); name != "" {
					cleaned = append(cleaned, name)
				}
			}
			if err := reconcileSet(tx, "galgame_tag_id", tagID, "name", cleaned,
				func(name string) model.GalgameTagAlias {
					return model.GalgameTagAlias{GalgameTagID: tagID, Name: name}
				}); err != nil {
				return err
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
