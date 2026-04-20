package repository

import (
	"context"
	"sort"
	"strings"
	"time"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/model"

	"gorm.io/gorm"
)

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

	// Totals
	var cnt int64
	r.db.WithContext(ctx).Model(&model.GalgameTag{}).Count(&cnt)
	resp.Totals.GalgameTag = int(cnt)

	r.db.WithContext(ctx).Model(&model.GalgameOfficial{}).Count(&cnt)
	resp.Totals.GalgameOfficial = int(cnt)

	r.db.WithContext(ctx).Model(&model.GalgameEngine{}).Count(&cnt)
	resp.Totals.GalgameEngine = int(cnt)

	r.db.WithContext(ctx).Model(&model.GalgameSeries{}).Count(&cnt)
	resp.Totals.GalgameSeries = int(cnt)

	r.db.WithContext(ctx).Model(&model.GalgameLink{}).Count(&cnt)
	resp.Totals.GalgameLink = int(cnt)

	r.db.WithContext(ctx).Model(&model.GalgamePR{}).Count(&cnt)
	resp.Totals.GalgamePR = int(cnt)

	r.db.WithContext(ctx).Model(&model.GalgameRevision{}).Count(&cnt)
	resp.Totals.GalgameRevision = int(cnt)

	// Daily counts
	now := time.Now()
	since := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -days+1)

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
	prDaily, err := queryDaily("galgame_pr")
	if err != nil {
		return nil, err
	}
	revisionDaily, err := queryDaily("galgame_revision")
	if err != nil {
		return nil, err
	}

	// Merge all dates
	dateSet := make(map[string]bool)
	for d := range tagDaily {
		dateSet[d] = true
	}
	for d := range officialDaily {
		dateSet[d] = true
	}
	for d := range engineDaily {
		dateSet[d] = true
	}
	for d := range seriesDaily {
		dateSet[d] = true
	}
	for d := range linkDaily {
		dateSet[d] = true
	}
	for d := range prDaily {
		dateSet[d] = true
	}
	for d := range revisionDaily {
		dateSet[d] = true
	}

	resp.Daily = make([]dto.AdminStatsDaily, 0, len(dateSet))
	for d := range dateSet {
		resp.Daily = append(resp.Daily, dto.AdminStatsDaily{
			Date:            d,
			GalgameTag:      tagDaily[d],
			GalgameOfficial: officialDaily[d],
			GalgameEngine:   engineDaily[d],
			GalgameSeries:   seriesDaily[d],
			GalgameLink:     linkDaily[d],
			GalgamePR:       prDaily[d],
			GalgameRevision: revisionDaily[d],
		})
	}

	sort.Slice(resp.Daily, func(i, j int) bool {
		return resp.Daily[i].Date < resp.Daily[j].Date
	})

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
		like := "%" + strings.ToLower(req.Search) + "%"
		query = query.Where(
			"vndb_id LIKE ? OR LOWER(name_en_us) LIKE ? OR LOWER(name_ja_jp) LIKE ? OR LOWER(name_zh_cn) LIKE ? OR LOWER(name_zh_tw) LIKE ?",
			req.Search, like, like, like, like,
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
		First(&g, id).Error
	if err != nil {
		return nil, err
	}
	return &g, nil
}

// UpdateGalgameStatus sets the status field on a galgame.
func (r *AdminRepository) UpdateGalgameStatus(ctx context.Context, id, status int) error {
	return r.db.WithContext(ctx).
		Model(&model.Galgame{}).
		Where("id = ?", id).
		Update("status", status).Error
}
