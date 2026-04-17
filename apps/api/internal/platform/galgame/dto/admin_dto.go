package dto

// AdminStatsRequest represents an admin stats query
type AdminStatsRequest struct {
	Days int `query:"days" validate:"min=1,max=365"`
}

// AdminStatsTotals holds total counts for each entity
type AdminStatsTotals struct {
	GalgameTag      int `json:"galgame_tag"`
	GalgameOfficial int `json:"galgame_official"`
	GalgameEngine   int `json:"galgame_engine"`
	GalgameSeries   int `json:"galgame_series"`
	GalgameLink     int `json:"galgame_link"`
	GalgamePR       int `json:"galgame_pr"`
	GalgameRevision int `json:"galgame_revision"`
}

// AdminStatsDaily holds daily counts for a single date
type AdminStatsDaily struct {
	Date            string `json:"date"`
	GalgameTag      int    `json:"galgame_tag"`
	GalgameOfficial int    `json:"galgame_official"`
	GalgameEngine   int    `json:"galgame_engine"`
	GalgameSeries   int    `json:"galgame_series"`
	GalgameLink     int    `json:"galgame_link"`
	GalgamePR       int    `json:"galgame_pr"`
	GalgameRevision int    `json:"galgame_revision"`
}

// AdminStatsResponse is the full admin stats response
type AdminStatsResponse struct {
	Totals AdminStatsTotals  `json:"totals"`
	Daily  []AdminStatsDaily `json:"daily"`
}
