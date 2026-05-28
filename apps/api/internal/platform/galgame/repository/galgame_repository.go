package repository

import (
	"context"
	"strings"
	"time"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GalgameRepository handles galgame data access on kun_galgame_wiki
type GalgameRepository struct {
	db *gorm.DB
}

// NewGalgameRepository creates a new GalgameRepository
func NewGalgameRepository(db *gorm.DB) *GalgameRepository {
	return &GalgameRepository{db: db}
}

// DB exposes the underlying gorm.DB for transactions
func (r *GalgameRepository) DB() *gorm.DB {
	return r.db
}

// FindByID finds a galgame by ID with all relations
func (r *GalgameRepository) FindByID(ctx context.Context, id int) (*model.Galgame, error) {
	var galgame model.Galgame
	err := r.db.WithContext(ctx).
		Preload("Alias").
		Preload("Series").
		Preload("Contributor").
		Preload("Link").
		// Inject the same `galgame_count` (aliased as `cnt`) subquery
		// that the dedicated GET /official, GET /engine, and GET /tag
		// list endpoints use, so detail-embedded officials / engines /
		// tags carry the count without consumers (kungal, moyu) having
		// to issue N follow-up requests to learn "this 会社 has how
		// many galgames?". The count filters galgame.status = 0 to
		// match what the list and per-official detail pages display —
		// drafts imported by sync-vndb stay excluded.
		Preload("Tag.Tag", func(db *gorm.DB) *gorm.DB {
			return db.
				Select("galgame_tag.*, COALESCE(tc.cnt, 0) AS cnt").
				Joins("LEFT JOIN (SELECT r.tag_id, COUNT(*) AS cnt FROM galgame_tag_relation r JOIN galgame g ON g.id = r.galgame_id AND g.status = 0 GROUP BY r.tag_id) tc ON tc.tag_id = galgame_tag.id")
		}).
		Preload("Official.Official", func(db *gorm.DB) *gorm.DB {
			return db.
				Select("galgame_official.*, COALESCE(oc.cnt, 0) AS cnt").
				Joins("LEFT JOIN (SELECT r.official_id, COUNT(*) AS cnt FROM galgame_official_relation r JOIN galgame g ON g.id = r.galgame_id AND g.status = 0 GROUP BY r.official_id) oc ON oc.official_id = galgame_official.id")
		}).
		Preload("Official.Official.Alias").
		Preload("Engine.Engine", func(db *gorm.DB) *gorm.DB {
			return db.
				Select("galgame_engine.*, COALESCE(ec.cnt, 0) AS cnt").
				Joins("LEFT JOIN (SELECT r.engine_id, COUNT(*) AS cnt FROM galgame_engine_relation r JOIN galgame g ON g.id = r.galgame_id AND g.status = 0 GROUP BY r.engine_id) ec ON ec.engine_id = galgame_engine.id")
		}).
		Preload("Cover", func(db *gorm.DB) *gorm.DB {
			return db.Order("sort_order ASC, created ASC")
		}).
		Preload("Screenshot", func(db *gorm.DB) *gorm.DB {
			return db.Order("sort_order ASC, created ASC")
		}).
		First(&galgame, id).Error
	if err != nil {
		return nil, err
	}
	// Populate the derived EffectiveBannerHash via the model-layer helper
	// so List / ListMine / FindByID share one implementation of the
	// "pinned cover" rule.
	model.PopulateEffectiveBanner(&galgame)
	return &galgame, nil
}

// ExistsByVNDBID checks if a galgame with the given VNDB ID exists
func (r *GalgameRepository) ExistsByVNDBID(ctx context.Context, vndbID string) (bool, int, error) {
	var galgame model.Galgame
	err := r.db.WithContext(ctx).
		Select("id").
		Where("vndb_id = ?", vndbID).
		First(&galgame).Error
	if err == gorm.ErrRecordNotFound {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	return true, galgame.ID, nil
}

// List returns a paginated list of galgames.
//
// contentLimit follows the canonical content-filter contract (see
// pkg/utils.ParseContentLimit): "sfw" / "nsfw" → WHERE filter, "" → no
// filter. Handlers resolve the default for missing query params.
//
// releasedFrom / releasedTo are inclusive bounds on galgame.release_date.
// Zero `time.Time` on either side = no filter on that side. The column
// has a btree index (declared in the model via gorm tag), so range
// scans are O(log N) — safe to expose to anonymous traffic.
// galgames with release_date IS NULL are excluded when either bound is
// set (SQL `>=` / `<=` on NULL is UNKNOWN → row drops out, matching
// the user-visible contract: "filter to 2024 = only games we know are
// in 2024").
func (r *GalgameRepository) List(ctx context.Context, page, limit int, sortField, sortOrder, search, contentLimit string, releasedFrom, releasedTo time.Time) (items []model.Galgame, total int64, err error) {
	defer func() {
		for i := range items {
			model.PopulateEffectiveBanner(&items[i])
		}
	}()

	query := r.db.WithContext(ctx).Model(&model.Galgame{}).Where("status = 0")

	if contentLimit != "" {
		query = query.Where("content_limit = ?", contentLimit)
	}

	// Date filter — compare against the `date`-typed column with a
	// formatted YYYY-MM-DD string. PG's `date >= 'YYYY-MM-DD'` uses the
	// btree index; passing time.Time directly would cause an implicit
	// timestamptz coercion that drops index access.
	if !releasedFrom.IsZero() {
		query = query.Where("release_date >= ?", releasedFrom.Format("2006-01-02"))
	}
	if !releasedTo.IsZero() {
		query = query.Where("release_date <= ?", releasedTo.Format("2006-01-02"))
	}

	if search != "" {
		like := "%" + strings.ToLower(search) + "%"
		query = query.Where(
			"LOWER(name_en_us) LIKE ? OR LOWER(name_ja_jp) LIKE ? OR LOWER(name_zh_cn) LIKE ? OR LOWER(name_zh_tw) LIKE ?",
			like, like, like, like,
		)
	}

	query.Count(&total)

	// Whitelist allowed sort fields (snake_case column names only).
	// SQL-injection-safe: the value is keyed into this map, never
	// interpolated raw — an unknown field falls back to "created".
	allowedSortFields := map[string]bool{
		"created":              true,
		"updated":              true,
		"view":                 true,
		"resource_update_time": true,
		"release_date":         true,
	}
	if !allowedSortFields[sortField] {
		sortField = "created"
	}
	if sortOrder != "asc" && sortOrder != "desc" {
		sortOrder = "desc"
	}
	order := sortField + " " + sortOrder
	// release_date is the only NULLable sort column. Default PG ordering
	// puts NULLs FIRST on DESC — which would float every undated game to
	// the top of a "newest first" list (bad UX). Force NULLS LAST so
	// dated games always lead, undated tail. Other sort columns are
	// NOT NULL (have defaults) so this clause never matters for them.
	if sortField == "release_date" {
		order += " NULLS LAST"
	}

	err = query.
		Order(order).
		Offset((page - 1) * limit).
		Limit(limit).
		Preload("Tag.Tag").
		Preload("Official.Official").
		Preload("Cover", func(db *gorm.DB) *gorm.DB {
			return db.Order("sort_order ASC, created ASC")
		}).
		Find(&items).Error

	return items, total, err
}

// FindByIDs finds galgames by a list of IDs (lightweight, no relations).
// status is selected so the brief can carry it through to the caller —
// FindByIDs filters status=0 here so the value is always 0 in this code
// path, but the column is still in the projection to keep one consistent
// brief shape across FindByIDs / FindByIDsWithViewer.
//
// contentLimit follows ParseContentLimit semantics ("" = no filter).
func (r *GalgameRepository) FindByIDs(ctx context.Context, ids []int, contentLimit string) ([]model.Galgame, error) {
	q := r.db.WithContext(ctx).
		Select("id, vndb_id, name_en_us, name_ja_jp, name_zh_cn, name_zh_tw, banner, content_limit, status, user_id, resource_update_time, original_language, age_limit").
		Where("id IN ? AND status = 0", ids)
	if contentLimit != "" {
		q = q.Where("content_limit = ?", contentLimit)
	}
	var galgames []model.Galgame
	err := q.Find(&galgames).Error
	return galgames, err
}

// FindByIDsAny is like FindByIDs but does NOT filter by status. Used by
// internal services (e.g. MessageService.enrich) that already authorize the
// audience and need the row regardless of state. Public API must use
// FindByIDs or FindByIDsWithViewer instead.
func (r *GalgameRepository) FindByIDsAny(ctx context.Context, ids []int) ([]model.Galgame, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var galgames []model.Galgame
	err := r.db.WithContext(ctx).
		Select("id, vndb_id, name_en_us, name_ja_jp, name_zh_cn, name_zh_tw, banner, content_limit, status, user_id, resource_update_time, original_language, age_limit").
		Where("id IN ?", ids).
		Find(&galgames).Error
	return galgames, err
}

// FindByIDsWithViewer is like FindByIDs but additionally returns entries
// in status {3, 4} where the viewer is the submitter. Used by
// GET /galgame/batch when the caller authenticates with a user JWT.
//
// viewerUserID == 0 falls back to FindByIDs (public visibility).
//
// contentLimit applies the same way as FindByIDs: "" = no filter.
// The viewer's own pending/declined drafts are NOT exempted from the
// filter — if you submit an NSFW draft and your client requests
// content_limit=sfw, you won't see it in batch results either (consistent
// with how List/search behave for the same caller's data).
func (r *GalgameRepository) FindByIDsWithViewer(ctx context.Context, ids []int, viewerUserID int, contentLimit string) ([]model.Galgame, error) {
	if viewerUserID <= 0 {
		return r.FindByIDs(ctx, ids, contentLimit)
	}
	q := r.db.WithContext(ctx).
		Select("id, vndb_id, name_en_us, name_ja_jp, name_zh_cn, name_zh_tw, banner, content_limit, status, user_id, resource_update_time, original_language, age_limit").
		Where("id IN ?", ids).
		Where("status = 0 OR (status IN (3, 4) AND user_id = ?)", viewerUserID)
	if contentLimit != "" {
		q = q.Where("content_limit = ?", contentLimit)
	}
	var galgames []model.Galgame
	err := q.Find(&galgames).Error
	return galgames, err
}

// PinnedCoverHashes returns the {galgame_id → image_hash} mapping of the
// pinned cover (sort_order=0) for each id. Galgames with no covers yet
// are absent from the map. Used by BatchGet to populate
// GalgameBrief.EffectiveBannerHash without per-row preloading.
//
// Safe for empty input — returns an empty map, no query.
func (r *GalgameRepository) PinnedCoverHashes(ctx context.Context, ids []int) (map[int]string, error) {
	out := make(map[int]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	type row struct {
		GalgameID int
		ImageHash string
	}
	var rows []row
	if err := r.db.WithContext(ctx).
		Model(&model.GalgameCover{}).
		Select("galgame_id, image_hash").
		Where("galgame_id IN ? AND sort_order = 0", ids).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.GalgameID] = r.ImageHash
	}
	return out, nil
}

// ListMine returns galgames the user has submitted matching one of the
// given statuses. Used by GET /galgame/mine.
func (r *GalgameRepository) ListMine(ctx context.Context, userID int, statuses []int, page, limit int) (items []model.Galgame, total int64, err error) {
	defer func() {
		for i := range items {
			model.PopulateEffectiveBanner(&items[i])
		}
	}()

	q := r.db.WithContext(ctx).Model(&model.Galgame{}).
		Where("user_id = ?", userID).
		Where("status IN ?", statuses)

	q.Count(&total)

	err = q.Order("updated DESC").
		Offset((page - 1) * limit).
		Limit(limit).
		Preload("Cover", func(db *gorm.DB) *gorm.DB {
			return db.Order("sort_order ASC, created ASC")
		}).
		Find(&items).Error
	return items, total, err
}

// CheckSubmitQuota counts submissions made today by this user.
// Caller compares against the configured per-day limit (default 5).
func (r *GalgameRepository) CountSubmissionsToday(ctx context.Context, userID int) (int64, error) {
	now := time.Now()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	var cnt int64
	err := r.db.WithContext(ctx).Model(&model.Galgame{}).
		Where("user_id = ? AND status IN (3, 4) AND created >= ?", userID, dayStart).
		Count(&cnt).Error
	return cnt, err
}

// FindForUpdate selects a galgame with row-level locking. Used in claim/patch
// to avoid race conditions between concurrent admins/users.
func (r *GalgameRepository) FindForUpdate(tx *gorm.DB, id int) (*model.Galgame, error) {
	var g model.Galgame
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&g, id).Error
	if err != nil {
		return nil, err
	}
	return &g, nil
}

// Create creates a galgame record
func (r *GalgameRepository) Create(ctx context.Context, galgame *model.Galgame) error {
	return r.db.WithContext(ctx).Create(galgame).Error
}

// Update updates a galgame record
func (r *GalgameRepository) Update(ctx context.Context, galgame *model.Galgame) error {
	return r.db.WithContext(ctx).Save(galgame).Error
}

// IncrementView increments the view count
func (r *GalgameRepository) IncrementView(ctx context.Context, id int) error {
	return r.db.WithContext(ctx).
		Model(&model.Galgame{}).
		Where("id = ?", id).
		Update("view", gorm.Expr("view + 1")).Error
}

// GetUserStats returns aggregated galgame statistics for a user
func (r *GalgameRepository) GetUserStats(ctx context.Context, userID int) (*dto.UserGalgameStats, error) {
	var stats dto.UserGalgameStats
	var cnt int64

	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	// Galgames created (total)
	r.db.WithContext(ctx).Model(&model.Galgame{}).
		Where("user_id = ? AND status = 0", userID).Count(&cnt)
	stats.GalgameCreated = int(cnt)

	// Galgames created today
	r.db.WithContext(ctx).Model(&model.Galgame{}).
		Where("user_id = ? AND status = 0 AND created >= ?", userID, todayStart).Count(&cnt)
	stats.GalgameCreatedToday = int(cnt)

	// Galgames contributed to (distinct galgame_id)
	r.db.WithContext(ctx).Model(&model.GalgameContributor{}).
		Where("user_id = ?", userID).Distinct("galgame_id").Count(&cnt)
	stats.GalgameContributed = int(cnt)

	// Revision count
	r.db.WithContext(ctx).Model(&model.GalgameRevision{}).
		Where("user_id = ?", userID).Count(&cnt)
	stats.RevisionCount = int(cnt)

	// PR submitted (total)
	r.db.WithContext(ctx).Model(&model.GalgamePR{}).
		Where("user_id = ?", userID).Count(&cnt)
	stats.PRSubmitted = int(cnt)

	// PR merged
	r.db.WithContext(ctx).Model(&model.GalgamePR{}).
		Where("user_id = ? AND status = 1", userID).Count(&cnt)
	stats.PRMerged = int(cnt)

	// PR declined
	r.db.WithContext(ctx).Model(&model.GalgamePR{}).
		Where("user_id = ? AND status = 2", userID).Count(&cnt)
	stats.PRDeclined = int(cnt)

	// PR pending
	r.db.WithContext(ctx).Model(&model.GalgamePR{}).
		Where("user_id = ? AND status = 0", userID).Count(&cnt)
	stats.PRPending = int(cnt)

	return &stats, nil
}
