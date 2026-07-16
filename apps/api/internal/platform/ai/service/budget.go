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

// ErrUnknownRoute rejects a budget upsert for a route the gateway does not
// serve — a cap row keyed to a typo would be dead config that never fuses.
var ErrUnknownRoute = errors.New("unknown ai route")

// BudgetService serves the admin budget-fuse config reads and upserts. The v0
// posture stays record-don't-block: setting a cap is an ops choice, and a NULL
// cap never blocks (章程 ruling 6).
type BudgetService struct {
	db *gorm.DB
}

// NewBudgetService wires the service to the kun_ai DB.
func NewBudgetService(db *gorm.DB) *BudgetService { return &BudgetService{db: db} }

// List returns every ai_route_budget row, route-then-site ordered.
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

// Upsert sets or clears the (route, site) daily cap. cap==nil writes NULL
// (clears the cap) — a valid, distinct state from a 0 cap (block everything).
// The explicit column assignment forces the NULL through GORM's zero-value
// omission (a nil *int64 in a plain Create would be dropped, leaving a stale
// cap on conflict). Returns the persisted row.
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

	// Re-read to return the canonical persisted row (updated_at as the DB stored
	// it, cap as NULL/value).
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
