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

// AdminListGalgamesRequest represents an admin galgame list query
// (unlike the public list which hardcodes status=0, admin can filter any status)
type AdminListGalgamesRequest struct {
	Page  int `query:"page" validate:"min=1"`
	Limit int `query:"limit" validate:"min=1,max=100"`
	// Status 0=published, 1=banned, 2=vndb-draft, 3=pending-review, 4=declined.
	// Admin can filter on any single status.
	Status *int   `query:"status" validate:"omitempty,oneof=0 1 2 3 4"`
	Search string `query:"search"`
}

// AdminUpdateGalgameStatusRequest represents a status change request.
//
// Target status values (allowed):
//
//	0 — publish (typically from source status=3 → approved)
//	1 — ban (any source)
//	4 — decline (only allowed from source status=3)
//
// status=2 (VNDB draft) is NOT a valid target — that domain belongs to sync-vndb.
// status=3 (pending) is NOT a valid target — pending is produced by user submission only.
type AdminUpdateGalgameStatusRequest struct {
	Status int `json:"status" validate:"oneof=0 1 4"`
	// Optional moderator note. Stored in revision payload and the resulting
	// message payload (e.g. decline reason shown to the submitter).
	Reason string `json:"reason" validate:"omitempty,max=500"`
}
