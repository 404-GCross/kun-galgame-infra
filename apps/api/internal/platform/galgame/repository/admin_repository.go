package repository

import (
	"context"
	"os"
	"strings"
	"time"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/model"

	"gorm.io/gorm"
)

// pgSessionLocation returns the timezone Postgres uses for date_trunc('day')
// bucketing, so the Go-side day boundaries (since/today) align with the SQL
// buckets instead of using the host's process-local zone. Mirrors the DSN's
// TimeZone (KUN_GALGAME_PG_TIMEZONE / KUN_PG_TIMEZONE, default Asia/Shanghai);
// falls back to UTC if the zone can't be loaded.
func pgSessionLocation() *time.Location {
	tz := os.Getenv("KUN_GALGAME_PG_TIMEZONE")
	if tz == "" {
		tz = os.Getenv("KUN_PG_TIMEZONE")
	}
	if tz == "" {
		tz = "Asia/Shanghai"
	}
	if loc, err := time.LoadLocation(tz); err == nil {
		return loc
	}
	return time.UTC
}

// AdminRepository handles admin statistics queries
type AdminRepository struct {
	db *gorm.DB
}

// NewAdminRepository creates a new AdminRepository
func NewAdminRepository(db *gorm.DB) *AdminRepository {
	return &AdminRepository{db: db}
}

// GetStats returns totals and daily counts for the last N days
func (r *AdminRepository) GetStats(ctx context.Context, days int) (*dto.AdminStatsResponse, error) {
	var resp dto.AdminStatsResponse

	// Totals — each Count propagates its error so a DB failure surfaces as a
	// 500 instead of returning half-true totals with the error swallowed.
	countAll := func(m any) (int, error) {
		var cnt int64
		if err := r.db.WithContext(ctx).Model(m).Count(&cnt).Error; err != nil {
			return 0, err
		}
		return int(cnt), nil
	}
	var err error
	if resp.Totals.GalgameTag, err = countAll(&model.GalgameTag{}); err != nil {
		return nil, err
	}
	if resp.Totals.GalgameOfficial, err = countAll(&model.GalgameOfficial{}); err != nil {
		return nil, err
	}
	if resp.Totals.GalgameEngine, err = countAll(&model.GalgameEngine{}); err != nil {
		return nil, err
	}
	if resp.Totals.GalgameSeries, err = countAll(&model.GalgameSeries{}); err != nil {
		return nil, err
	}
	if resp.Totals.GalgameLink, err = countAll(&model.GalgameLink{}); err != nil {
		return nil, err
	}
	// galgame_pr / galgame_revision counts retired at E3b: those tables froze at
	// the E2b engine migration (edits land in edit_* now), so their totals were
	// stale. The per-user edit counters live in GetUserStats (via editquery).

	// Daily counts. Compute day boundaries in the PG session timezone so they
	// line up with date_trunc('day', created) below (host TZ != session TZ
	// would otherwise shift buckets by a day).
	loc := pgSessionLocation()
	now := time.Now().In(loc)
	since := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -days+1)

	type dateCount struct {
		Date  string `gorm:"column:date"`
		Count int    `gorm:"column:count"`
	}

	// Helper: query daily counts for a table
	queryDaily := func(tableName string) (map[string]int, error) {
		var rows []dateCount
		err := r.db.WithContext(ctx).
			Raw("SELECT date_trunc('day', created)::date::text AS date, COUNT(*) AS count FROM "+tableName+" WHERE created >= ? GROUP BY date ORDER BY date", since).
			Scan(&rows).Error
		if err != nil {
			return nil, err
		}
		m := make(map[string]int, len(rows))
		for _, row := range rows {
			m[row.Date] = row.Count
		}
		return m, nil
	}

	tagDaily, err := queryDaily("galgame_tag")
	if err != nil {
		return nil, err
	}
	officialDaily, err := queryDaily("galgame_official")
	if err != nil {
		return nil, err
	}
	engineDaily, err := queryDaily("galgame_engine")
	if err != nil {
		return nil, err
	}
	seriesDaily, err := queryDaily("galgame_series")
	if err != nil {
		return nil, err
	}
	linkDaily, err := queryDaily("galgame_link")
	if err != nil {
		return nil, err
	}

	// Zero-fill the full [since .. today] range so the daily series is
	// contiguous — days with no activity were previously absent, leaving the
	// frontend chart with fewer-than-N bars and non-consecutive dates. Keys
	// are formatted from `since` (same basis as the SQL bucket lower bound),
	// already ascending so no sort needed.
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	resp.Daily = make([]dto.AdminStatsDaily, 0, days)
	for d := since; !d.After(today); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		resp.Daily = append(resp.Daily, dto.AdminStatsDaily{
			Date:            key,
			GalgameTag:      tagDaily[key],
			GalgameOfficial: officialDaily[key],
			GalgameEngine:   engineDaily[key],
			GalgameSeries:   seriesDaily[key],
			GalgameLink:     linkDaily[key],
		})
	}

	return &resp, nil
}

// ListGalgames returns a paginated list of galgames with status filtering.
// Unlike the public List, this does NOT hardcode status=0 — used by admins to
// audit drafts (status=2) and banned entries (status=1).
func (r *AdminRepository) ListGalgames(ctx context.Context, req *dto.AdminListGalgamesRequest) ([]model.Galgame, int64, error) {
	var items []model.Galgame
	var total int64

	query := r.db.WithContext(ctx).Model(&model.Galgame{})

	if req.Status != nil {
		query = query.Where("status = ?", *req.Status)
	}

	if req.Search != "" {
		// vndb_id was bound to the raw (unwrapped) search term, so LIKE
		// degraded to exact match and substring/prefix vndb_id search never
		// hit. Wrap it like the name columns (and LOWER it for case-insensitivity).
		like := "%" + strings.ToLower(req.Search) + "%"
		query = query.Where(
			"LOWER(vndb_id) LIKE ? OR LOWER(name_en_us) LIKE ? OR LOWER(name_ja_jp) LIKE ? OR LOWER(name_zh_cn) LIKE ? OR LOWER(name_zh_tw) LIKE ?",
			like, like, like, like, like,
		)
	}

	query.Count(&total)

	err := query.
		Order("updated DESC").
		Offset((req.Page - 1) * req.Limit).
		Limit(req.Limit).
		Find(&items).Error

	return items, total, err
}

// GetGalgame returns a galgame by id with full relations, regardless of status.
func (r *AdminRepository) GetGalgame(ctx context.Context, id int) (*model.Galgame, error) {
	var g model.Galgame
	err := r.db.WithContext(ctx).
		Preload("Alias").
		Preload("Tag.Tag").
		Preload("Official.Official").
		Preload("Engine.Engine").
		Preload("Series").
		Preload("Cover", func(db *gorm.DB) *gorm.DB {
			return db.Order("sort_order ASC, created ASC")
		}).
		Preload("Screenshot", func(db *gorm.DB) *gorm.DB {
			return db.Order("sort_order ASC, created ASC")
		}).
		First(&g, id).Error
	if err != nil {
		return nil, err
	}
	model.PopulateEffectiveBanner(&g)
	return &g, nil
}

// UpdateGalgameStatus sets the status field on a galgame.
func (r *AdminRepository) UpdateGalgameStatus(ctx context.Context, id, status int) error {
	return r.db.WithContext(ctx).
		Model(&model.Galgame{}).
		Where("id = ?", id).
		Update("status", status).Error
}
