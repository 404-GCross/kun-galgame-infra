// Admin-face read aggregates over the ai_usage ledger. The dashboard needs three
// axes (doc 20 §5): a site×route×channel usage/cost/status summary, a day×route
// trend, and the current budget-fuse config. These are plain SQL GROUP BYs (no
// materialization); the site-leading and route-leading indexes from
// migrate.rawSQL back the window scans.
package service

import (
	"context"

	"api/internal/platform/ai/dto"
	"api/internal/platform/ai/model"

	"gorm.io/gorm"
)

// StatsService serves the admin usage/trend reads.
type StatsService struct {
	db *gorm.DB
}

// NewStatsService wires the service to the kun_ai DB.
func NewStatsService(db *gorm.DB) *StatsService { return &StatsService{db: db} }

// windowIntervals maps the wire window token to a Postgres interval literal. The
// handler validates the token via a Huma enum, so an unknown value here falls
// back to 24h defensively.
var windowIntervals = map[string]string{
	"24h": "24 hours",
	"7d":  "7 days",
	"30d": "30 days",
}

func resolveWindow(window string) (string, string) {
	if iv, ok := windowIntervals[window]; ok {
		return window, iv
	}
	return "24h", windowIntervals["24h"]
}

// Summary aggregates the window by (site, route, channel): call counts, token
// sums, cost, and the terminal-status distribution — plus a totalled overview.
// The COUNT(*) FILTER columns are aliased to the exact snake_case the DTO's gorm
// tags expect (章程 GORM acronym-column trap: the OK count → "ok", never "o_k").
func (s *StatsService) Summary(ctx context.Context, window string) (dto.UsageSummary, error) {
	resolved, interval := resolveWindow(window)

	var rows []dto.SummaryRow
	err := s.db.WithContext(ctx).
		Table("ai_usage").
		Select(`site, route, channel,
			COUNT(*) AS calls,
			COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
			COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
			COALESCE(SUM(cost_micro), 0) AS cost_micro,
			COUNT(*) FILTER (WHERE status = ?) AS ok,
			COUNT(*) FILTER (WHERE status = ?) AS upstream_error,
			COUNT(*) FILTER (WHERE status = ?) AS budget_denied,
			COUNT(*) FILTER (WHERE status = ?) AS degraded,
			COUNT(*) FILTER (WHERE status = ?) AS truncated`,
			model.StatusOK, model.StatusUpstreamError, model.StatusBudgetDenied,
			model.StatusDegraded, model.StatusTruncated).
		Where("created_at >= now() - CAST(? AS interval)", interval).
		Group("site, route, channel").
		Order("calls DESC, site, route, channel").
		Scan(&rows).Error
	if err != nil {
		return dto.UsageSummary{}, err
	}

	return dto.UsageSummary{Window: resolved, Overview: overviewOf(rows), Rows: rows}, nil
}

// overviewOf totals the per-group rows into the dashboard's headline card. The
// error rate is the non-OK fraction (upstream_error + budget_denied + degraded +
// truncated) over total calls — derived as calls-ok rather than by summing the
// buckets, so a status with no bucket of its own still lands in the rate. That
// is how truncation stayed inside the error rate while being invisible in the
// breakdown; the derivation was right, the breakdown was incomplete.
func overviewOf(rows []dto.SummaryRow) dto.UsageOverview {
	var o dto.UsageOverview
	for _, r := range rows {
		o.Calls += r.Calls
		o.PromptTokens += r.PromptTokens
		o.CompletionTokens += r.CompletionTokens
		o.CostMicro += r.CostMicro
		o.OK += r.OK
		o.UpstreamError += r.UpstreamError
		o.BudgetDenied += r.BudgetDenied
		o.Degraded += r.Degraded
		o.Truncated += r.Truncated
	}
	if o.Calls > 0 {
		o.ErrorRate = float64(o.Calls-o.OK) / float64(o.Calls)
	}
	return o
}

// Daily returns a day×route calls/cost series covering the last `days` calendar
// days (inclusive of today). The lower bound is the start of (today-(days-1))
// in the DB session timezone.
func (s *StatsService) Daily(ctx context.Context, days int) (dto.DailySeries, error) {
	if days < 1 {
		days = 1
	}
	var points []dto.DailyPoint
	err := s.db.WithContext(ctx).
		Table("ai_usage").
		Select(`to_char(date_trunc('day', created_at), 'YYYY-MM-DD') AS day, route,
			COUNT(*) AS calls,
			COALESCE(SUM(cost_micro), 0) AS cost_micro`).
		Where("created_at >= date_trunc('day', now()) - (? * interval '1 day')", days-1).
		Group("day, route").
		Order("day, route").
		Scan(&points).Error
	if err != nil {
		return dto.DailySeries{}, err
	}
	return dto.DailySeries{Days: days, Points: points}, nil
}
