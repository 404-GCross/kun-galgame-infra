package service

import (
	"context"
	"errors"
	"time"

	"api/internal/platform/ai/dto"
	"api/internal/platform/ai/model"
	"api/internal/platform/ai/route"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrUnknownRoute = errors.New("unknown ai route")

type BudgetService struct {
	db *gorm.DB
}

func NewBudgetService(db *gorm.DB) *BudgetService { return &BudgetService{db: db} }

func (s *BudgetService) List(ctx context.Context) ([]dto.BudgetView, error) {
	var rows []model.AIRouteBudget
	if err := s.db.WithContext(ctx).Order("route, site").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]dto.BudgetView, 0, len(rows))
	for _, r := range rows {
		out = append(out, toBudgetView(r))
	}
	return out, nil
}

func (s *BudgetService) Upsert(ctx context.Context, route, site string, cap *int64) (dto.BudgetView, error) {
	if !knownRoute(route) {
		return dto.BudgetView{}, ErrUnknownRoute
	}
	now := time.Now()
	row := model.AIRouteBudget{Route: route, Site: site, DailyCostCapMicro: cap, UpdatedAt: now}
	err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "route"}, {Name: "site"}},
		DoUpdates: clause.Assignments(map[string]any{
			"daily_cost_cap_micro": cap,
			"updated_at":           now,
		}),
	}).Create(&row).Error
	if err != nil {
		return dto.BudgetView{}, err
	}

	var saved model.AIRouteBudget
	if err := s.db.WithContext(ctx).Where("route = ? AND site = ?", route, site).Take(&saved).Error; err != nil {
		return dto.BudgetView{}, err
	}
	return toBudgetView(saved), nil
}

func knownRoute(name string) bool {
	_, ok := route.Lookup(name)
	return ok
}

func toBudgetView(r model.AIRouteBudget) dto.BudgetView {
	return dto.BudgetView{
		Route:             r.Route,
		Site:              r.Site,
		DailyCostCapMicro: r.DailyCostCapMicro,
		UpdatedAt:         r.UpdatedAt.UTC().Format(time.RFC3339),
	}
}
