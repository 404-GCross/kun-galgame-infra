package dto

// public_stats_dto.go — the SLIM public counts payload (GET /v1/catalog/stats,
// wave 149b). Deliberately its own type rather than a projection of
// CatalogStats: that dashboard is INTERNAL TELEMETRY (review queues, LLM
// verdicts, the anchor tier matrix, source freshness, orphan counts, the
// claim-state matrix) and none of it may cross onto the frozen public face.
// What a product site legitimately wants — "how big is this catalogue" — is
// exactly the two blocks below, so the two shapes stay free to evolve apart.

// PublicCatalogStats is the whole public counts payload in one round-trip.
type PublicCatalogStats struct {
	Works    PublicStatsWorks    `json:"works"`
	Entities PublicStatsEntities `json:"entities"`
}

// PublicStatsWorks counts the LIVE registry rows (status=live, not stub, not
// merged, not soft-deleted), broken down by medium. Total is the sum of the
// breakdown, so the two can never disagree.
type PublicStatsWorks struct {
	Total    int64                    `json:"total"`
	ByMedium []PublicStatsMediumCount `json:"by_medium"`
}

// PublicStatsMediumCount is one medium's LIVE work count. It carries BOTH the
// numeric medium_id and the public medium key — the id because it is the
// registry's own vocabulary axis, the key because the public face speaks
// string keys everywhere else (a consumer should never have to hardcode an
// enum int to label a row).
type PublicStatsMediumCount struct {
	MediumID int16  `json:"medium_id"`
	Medium   string `json:"medium"`
	Count    int64  `json:"count"`
}

// PublicStatsEntities totals the identity families. Live rows only (the
// soft-deleted rows a merge left behind are not part of the catalogue).
type PublicStatsEntities struct {
	Labels      int64 `json:"labels"`
	Characters  int64 `json:"characters"`
	CreditNames int64 `json:"credit_names"`
	Persons     int64 `json:"persons"`
}
