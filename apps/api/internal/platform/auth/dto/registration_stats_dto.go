package dto

// RegistrationStatsRequest is the query for GET /admin/stats/registrations.
// Days defaults to 30 (handler) and is capped at 90.
type RegistrationStatsRequest struct {
	Days int `query:"days" validate:"omitempty,min=1,max=90"`
}

// HourlyStatsRequest is the query for GET /admin/stats/registrations/hourly.
type HourlyStatsRequest struct {
	Date string `query:"date" validate:"required,datetime=2006-01-02"`
}

// DailyRegistration is one day's registration count (date in stats timezone).
type DailyRegistration struct {
	Date  string `json:"date"` // YYYY-MM-DD
	Count int    `json:"count"`
}

// RegistrationSummary holds the headline figures for the selected range.
type RegistrationSummary struct {
	Total           int     `json:"total"`             // registrations in range
	DailyAverage    float64 `json:"daily_average"`     // total / range_days, 1dp
	PeakDate        string  `json:"peak_date"`         // busiest day in range
	PeakCount       int     `json:"peak_count"`
	Today           int     `json:"today"`             // today's count (stats tz)
	TotalAllTime    int64   `json:"total_all_time"`    // all users ever
	PrevPeriodTotal int     `json:"prev_period_total"` // the preceding equal window
	GrowthPercent   float64 `json:"growth_percent"`    // vs prev period, 1dp
}

// RegistrationStatsResponse is the daily series + summary for a range.
type RegistrationStatsResponse struct {
	RangeDays int                 `json:"range_days"`
	Timezone  string              `json:"timezone"`
	Series    []DailyRegistration `json:"series"` // oldest→newest, 0-filled
	Summary   RegistrationSummary `json:"summary"`
}

// HourlyRegistration is one hour-of-day's registration count.
type HourlyRegistration struct {
	Hour  int `json:"hour"` // 0-23
	Count int `json:"count"`
}

// HourlyStatsResponse is the 24-hour series for a single day.
type HourlyStatsResponse struct {
	Date     string               `json:"date"`
	Timezone string               `json:"timezone"`
	Series   []HourlyRegistration `json:"series"` // 24 entries, hour 0-23, 0-filled
	Total    int                  `json:"total"`
}
