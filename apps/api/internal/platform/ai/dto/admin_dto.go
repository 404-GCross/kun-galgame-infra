// Admin-face wire types for the AI usage dashboard (doc 20 §5). These are read
// projections over ai_usage + ai_route_budget, never the raw ledger rows. Huma
// bodies are named structs with explicit fields (章程 ruling 8). The aggregate
// rows carry `gorm:"column:..."` tags so the stats service can Scan a raw
// GROUP BY result straight into them — the columns are aliased explicitly in the
// SQL to dodge GORM's acronym-column trap (e.g. the OK count → column "ok").
package dto

// SummaryRow is one (site, route, channel) aggregate over the window. Channel ”
// means the calls were served degraded (no upstream dialled).
type SummaryRow struct {
	Site             string `gorm:"column:site" json:"site"`
	Route            string `gorm:"column:route" json:"route"`
	Channel          string `gorm:"column:channel" json:"channel"`
	Calls            int64  `gorm:"column:calls" json:"calls"`
	PromptTokens     int64  `gorm:"column:prompt_tokens" json:"prompt_tokens"`
	CompletionTokens int64  `gorm:"column:completion_tokens" json:"completion_tokens"`
	CostMicro        int64  `gorm:"column:cost_micro" json:"cost_micro"`
	// Status distribution within the group (doc 20 §5 terminal states).
	OK            int64 `gorm:"column:ok" json:"ok"`
	UpstreamError int64 `gorm:"column:upstream_error" json:"upstream_error"`
	BudgetDenied  int64 `gorm:"column:budget_denied" json:"budget_denied"`
	Degraded      int64 `gorm:"column:degraded" json:"degraded"`
}

// UsageOverview totals every row in the window (the dashboard's top cards).
type UsageOverview struct {
	Calls            int64 `json:"calls"`
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	CostMicro        int64 `json:"cost_micro"`
	OK               int64 `json:"ok"`
	UpstreamError    int64 `json:"upstream_error"`
	BudgetDenied     int64 `json:"budget_denied"`
	Degraded         int64 `json:"degraded"`
	// ErrorRate is the non-OK fraction of calls in [0,1] (0 when no calls). v0 is
	// degraded by default (empty upstream env), so a high rate is expected until
	// the channel layer is wired — the status breakdown tells the composition.
	ErrorRate float64 `json:"error_rate"`
}

// UsageSummary is the summary endpoint payload: the resolved window, the
// totalled overview, and the per-(site,route,channel) rows.
type UsageSummary struct {
	Window   string        `json:"window"`
	Overview UsageOverview `json:"overview"`
	Rows     []SummaryRow  `json:"rows"`
}

// DailyPoint is one day×route calls/cost sample (trend data). Day is the local
// calendar day as YYYY-MM-DD.
type DailyPoint struct {
	Day       string `gorm:"column:day" json:"day"`
	Route     string `gorm:"column:route" json:"route"`
	Calls     int64  `gorm:"column:calls" json:"calls"`
	CostMicro int64  `gorm:"column:cost_micro" json:"cost_micro"`
}

// DailySeries is the daily endpoint payload.
type DailySeries struct {
	Days   int          `json:"days"`
	Points []DailyPoint `json:"points"`
}

// BudgetView is one ai_route_budget row. DailyCostCapMicro is null when there is
// no cap (the v0 record-don't-block default / an explicit per-site "no cap").
type BudgetView struct {
	Route             string `json:"route"`
	Site              string `json:"site"`
	DailyCostCapMicro *int64 `json:"daily_cost_cap_micro"`
	UpdatedAt         string `json:"updated_at"`
}

// UpsertBudgetRequest sets or clears a per-route (optionally per-site) daily
// cap. Site ” is the route-wide default scope. A null/absent
// daily_cost_cap_micro CLEARS the cap (writes NULL) — the row is kept so a
// concrete site can still shadow the route-wide default with "no block".
type UpsertBudgetRequest struct {
	Route             string `json:"route" doc:"semantic route name (v0: moderate-text)"`
	Site              string `json:"site" doc:"tenant site; '' = the route-wide default scope"`
	DailyCostCapMicro *int64 `json:"daily_cost_cap_micro,omitempty" doc:"per-day micro-currency cap; null/absent = clear (no block)"`
}
