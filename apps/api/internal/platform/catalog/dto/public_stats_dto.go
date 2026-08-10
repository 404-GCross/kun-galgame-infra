package dto

type PublicCatalogStats struct {
	Works    PublicStatsWorks    `json:"works"`
	Entities PublicStatsEntities `json:"entities"`
}

type PublicStatsWorks struct {
	Total    int64                    `json:"total"`
	ByMedium []PublicStatsMediumCount `json:"by_medium"`
}

type PublicStatsMediumCount struct {
	MediumID int16  `json:"medium_id"`
	Medium   string `json:"medium"`
	Count    int64  `json:"count"`
}

type PublicStatsEntities struct {
	Labels      int64 `json:"labels"`
	Characters  int64 `json:"characters"`
	CreditNames int64 `json:"credit_names"`
	Persons     int64 `json:"persons"`
}
