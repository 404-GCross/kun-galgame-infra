package repository

import (
	"context"
	"time"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/model"

	"gorm.io/gorm"
)

// Data-access for the NextMoe open-API public projection (step 02): keyset
// (cursor) list, the changes stream, and pinned-image lookups that carry the
// per-image content rating so the sfw face can drop NSFW-rated pins. All queries
// are published-only (status = 0) and honor the caller's content_limit filter —
// the public face never sees drafts / banned rows.

// publicListColumns is the light column set the thin public list item needs
// (id, the four localized names, release date, age limit, updated).
const publicListColumns = "id, name_en_us, name_ja_jp, name_zh_cn, name_zh_tw, " +
	"release_date, release_date_tba, age_limit, updated"

// publicScope applies the shared public-face row filter: published only, plus
// the optional content_limit ("" = no filter; the sfw face passes "sfw").
func publicScope(contentLimit string) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		db = db.Where("status = 0")
		if contentLimit != "" {
			db = db.Where("content_limit = ?", contentLimit)
		}
		return db
	}
}

// PublicListByID returns a keyset page ordered by id ASC, strictly after afterID
// (afterID = 0 = first page). The caller passes limit+1 to detect a next page.
func (r *GalgameRepository) PublicListByID(ctx context.Context, afterID, limit int, contentLimit string) ([]model.Galgame, error) {
	q := r.db.WithContext(ctx).
		Model(&model.Galgame{}).
		Select(publicListColumns).
		Scopes(publicScope(contentLimit))
	if afterID > 0 {
		q = q.Where("id > ?", afterID)
	}
	var items []model.Galgame
	err := q.Order("id ASC").Limit(limit).Find(&items).Error
	return items, err
}

// PublicListByReleaseDate returns a keyset page ordered by release_date DESC
// NULLS LAST, id DESC. The cursor is (afterDate, afterID); hasCursor=false is the
// first page, afterDateNull marks a boundary already inside the undated (NULL)
// tail. Dated rows lead (newest first), undated rows trail.
func (r *GalgameRepository) PublicListByReleaseDate(ctx context.Context, hasCursor bool, afterDate time.Time, afterDateNull bool, afterID, limit int, contentLimit string) ([]model.Galgame, error) {
	q := r.db.WithContext(ctx).
		Model(&model.Galgame{}).
		Select(publicListColumns).
		Scopes(publicScope(contentLimit))
	if hasCursor {
		if afterDateNull {
			// Boundary is in the NULL tail → only further undated rows with a
			// smaller id remain.
			q = q.Where("release_date IS NULL AND id < ?", afterID)
		} else {
			// Boundary is a real date d → rows with an earlier date, or the same
			// date with a smaller id, then the whole undated tail. Compare against
			// the `date` column with a YYYY-MM-DD string to keep the btree index.
			d := afterDate.Format("2006-01-02")
			q = q.Where("release_date < ? OR (release_date = ? AND id < ?) OR release_date IS NULL", d, d, afterID)
		}
	}
	var items []model.Galgame
	err := q.Order("release_date DESC NULLS LAST, id DESC").Limit(limit).Find(&items).Error
	return items, err
}

// PublicChange is one changes-stream row: an id and its updated timestamp.
type PublicChange struct {
	ID      int
	Updated time.Time
}

// PublicChanges returns a keyset page of {id, updated} ordered by (updated, id)
// ascending, strictly after the (boundary, afterID) cursor. hasCursor=false is
// the first page. Backed by idx_galgame_updated_id (updated, id).
func (r *GalgameRepository) PublicChanges(ctx context.Context, hasCursor bool, boundary time.Time, afterID, limit int, contentLimit string) ([]PublicChange, error) {
	q := r.db.WithContext(ctx).
		Model(&model.Galgame{}).
		Select("id, updated").
		Scopes(publicScope(contentLimit))
	if hasCursor {
		q = q.Where("updated > ? OR (updated = ? AND id > ?)", boundary, boundary, afterID)
	}
	var rows []PublicChange
	err := q.Order("updated ASC, id ASC").Limit(limit).Scan(&rows).Error
	return rows, err
}

// PublicOfficials returns {galgame_id → [maker {id,name}]} for a whole page of
// ids in ONE relation→officials join — the batch loader behind the open-API
// list/search include=officials expansion (step 07 裁定 2: never one query per
// item). Ordered by (galgame_id, name) for a stable list. Safe for empty input.
func (r *GalgameRepository) PublicOfficials(ctx context.Context, ids []int) (map[int][]dto.PublicOfficial, error) {
	out := make(map[int][]dto.PublicOfficial, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	type row struct {
		GalgameID  int
		OfficialID int
		Name       string
	}
	var rows []row
	if err := r.db.WithContext(ctx).
		Table("galgame_official_relation AS rel").
		Select("rel.galgame_id AS galgame_id, o.id AS official_id, o.name AS name").
		Joins("JOIN galgame_official AS o ON o.id = rel.official_id").
		Where("rel.galgame_id IN ?", ids).
		Order("rel.galgame_id, o.name").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, rw := range rows {
		out[rw.GalgameID] = append(out[rw.GalgameID], dto.PublicOfficial{ID: rw.OfficialID, Name: rw.Name})
	}
	return out, nil
}

// PublicPinnedImage is a pinned cover/portrait row carrying the per-image content
// rating so the sfw face can drop an NSFW-rated pin (裁定 2).
type PublicPinnedImage struct {
	GalgameID int
	ImageHash string
	Sexual    int16
	Violence  int16
}

// PublicPinnedCovers returns the pinned landscape cover (sort_order = 0) for each
// id, with its content rating. Galgames without a pinned cover are absent.
func (r *GalgameRepository) PublicPinnedCovers(ctx context.Context, ids []int) (map[int]PublicPinnedImage, error) {
	return r.publicPinned(ctx, ids, "sort_order = 0")
}

// PublicPinnedPortraits returns the pinned portrait cover (portrait_pinned) for
// each id, with its content rating. Galgames without a pinned portrait are absent.
func (r *GalgameRepository) PublicPinnedPortraits(ctx context.Context, ids []int) (map[int]PublicPinnedImage, error) {
	return r.publicPinned(ctx, ids, "portrait_pinned")
}

func (r *GalgameRepository) publicPinned(ctx context.Context, ids []int, pinCond string) (map[int]PublicPinnedImage, error) {
	out := make(map[int]PublicPinnedImage, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []PublicPinnedImage
	if err := r.db.WithContext(ctx).
		Model(&model.GalgameCover{}).
		Select("galgame_id, image_hash, sexual, violence").
		Where("galgame_id IN ?", ids).
		Where(pinCond).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.GalgameID] = row
	}
	return out, nil
}
