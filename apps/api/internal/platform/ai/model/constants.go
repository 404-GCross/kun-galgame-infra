package model

const RouteModerateText = "moderate-text"

const (
	StatusOK            int16 = 0
	StatusUpstreamError int16 = 1
	StatusBudgetDenied  int16 = 2
	StatusRateLimited   int16 = 3
	StatusDegraded      int16 = 4
	StatusTruncated     int16 = 5
)
