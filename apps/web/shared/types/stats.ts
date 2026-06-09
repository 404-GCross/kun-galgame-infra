// Registration analytics returned by the OAuth admin endpoints
//   GET /admin/stats/registrations?days=N
//   GET /admin/stats/registrations/hourly?date=YYYY-MM-DD
// Buckets are in the server's stats timezone (Asia/Shanghai).

export interface DailyRegistration {
  date: string // YYYY-MM-DD
  count: number
}

export interface RegistrationSummary {
  total: number
  daily_average: number
  peak_date: string
  peak_count: number
  today: number
  total_all_time: number
  prev_period_total: number
  growth_percent: number
}

export interface RegistrationStats {
  range_days: number
  timezone: string
  series: DailyRegistration[]
  summary: RegistrationSummary
}

export interface HourlyRegistration {
  hour: number // 0-23
  count: number
}

export interface HourlyStats {
  date: string
  timezone: string
  series: HourlyRegistration[]
  total: number
}
