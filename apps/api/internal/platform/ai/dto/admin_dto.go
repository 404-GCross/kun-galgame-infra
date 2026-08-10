package dto

type SummaryRow struct {
	Site             string `gorm:"column:site" json:"site"`
	Route            string `gorm:"column:route" json:"route"`
	Channel          string `gorm:"column:channel" json:"channel"`
	Calls            int64  `gorm:"column:calls" json:"calls"`
	PromptTokens     int64  `gorm:"column:prompt_tokens" json:"prompt_tokens"`
	CompletionTokens int64  `gorm:"column:completion_tokens" json:"completion_tokens"`
	CostMicro        int64  `gorm:"column:cost_micro" json:"cost_micro"`
	OK               int64  `gorm:"column:ok" json:"ok"`
	UpstreamError    int64  `gorm:"column:upstream_error" json:"upstream_error"`
	BudgetDenied     int64  `gorm:"column:budget_denied" json:"budget_denied"`
	Degraded         int64  `gorm:"column:degraded" json:"degraded"`
	Truncated        int64  `gorm:"column:truncated" json:"truncated"`
}

type UsageOverview struct {
	Calls            int64   `json:"calls"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	CostMicro        int64   `json:"cost_micro"`
	OK               int64   `json:"ok"`
	UpstreamError    int64   `json:"upstream_error"`
	BudgetDenied     int64   `json:"budget_denied"`
	Degraded         int64   `json:"degraded"`
	Truncated        int64   `json:"truncated"`
	ErrorRate        float64 `json:"error_rate"`
}

type UsageSummary struct {
	Window   string        `json:"window"`
	Overview UsageOverview `json:"overview"`
	Rows     []SummaryRow  `json:"rows"`
}

type DailyPoint struct {
	Day       string `gorm:"column:day" json:"day"`
	Route     string `gorm:"column:route" json:"route"`
	Calls     int64  `gorm:"column:calls" json:"calls"`
	CostMicro int64  `gorm:"column:cost_micro" json:"cost_micro"`
}

type DailySeries struct {
	Days   int          `json:"days"`
	Points []DailyPoint `json:"points"`
}

type BudgetView struct {
	Route             string `json:"route"`
	Site              string `json:"site"`
	DailyCostCapMicro *int64 `json:"daily_cost_cap_micro"`
	UpdatedAt         string `json:"updated_at"`
}

type UpsertBudgetRequest struct {
	Route             string `json:"route" doc:"semantic route name (v0: moderate-text)"`
	Site              string `json:"site" doc:"tenant site; '' = the route-wide default scope"`
	DailyCostCapMicro *int64 `json:"daily_cost_cap_micro,omitempty" doc:"per-day micro-currency cap; null/absent = clear (no block)"`
}
