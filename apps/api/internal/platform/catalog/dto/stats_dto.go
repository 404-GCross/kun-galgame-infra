package dto

// Stats DTOs (step 19, D-02): the internal browser's dashboard. Every number is
// an index-ready count; the whole dashboard is one round-trip. Zeros are
// surfaced honestly (person=0, proposals=0 are doctrine/state, not gaps).

// CatalogStats is the whole dashboard in one payload.
type CatalogStats struct {
	Works               WorksMatrix       `json:"works"`
	Entities            EntityCounts      `json:"entities"`
	CreditsBySource     []KeyCount        `json:"credits_by_source"`
	AttributionsByKind  []KindCount       `json:"attributions_by_kind"`
	AnchorsBySourceTier []AnchorTierCell  `json:"anchors_by_source_tier"`
	Queues              QueueLevels       `json:"queues"`
	LLMBidVerdicts      []KeyCount        `json:"llm_bid_verdicts"`
	SourceFreshness     []SourceFreshness `json:"source_freshness"`
}

// WorksMatrix is the works count broken down by medium × claim state × status
// (the registry/body boundary made visible).
type WorksMatrix struct {
	Total int64       `json:"total"`
	Cells []WorksCell `json:"cells"`
}

type WorksCell struct {
	MediumID int16 `json:"medium_id"`
	Claimed  bool  `json:"claimed"`
	Status   int16 `json:"status" doc:"0=live 1=stub 2=merged"`
	Count    int64 `json:"count"`
}

// EntityCounts totals per entity family; OrphanCreditNames is called out (the
// orphan doctrine's running state, not a defect).
type EntityCounts struct {
	Persons           int64 `json:"persons"`
	CreditNames       int64 `json:"credit_names"`
	OrphanCreditNames int64 `json:"orphan_credit_names" doc:"credit_name.person_id IS NULL"`
	Orgs              int64 `json:"orgs"`
	Labels            int64 `json:"labels"`
	Characters        int64 `json:"characters"`
}

// KeyCount is a string-keyed count (source key, verdict, …). An empty key is a
// real bucket (e.g. user-curated credits with no source) and kept.
type KeyCount struct {
	Key   string `json:"key"`
	Count int64  `json:"count"`
}

type KindCount struct {
	Kind  int16 `json:"kind"`
	Count int64 `json:"count"`
}

// AnchorTierCell is one cell of the source × tier cross-table — the quality
// panel's soul (one table says all of identity quality).
type AnchorTierCell struct {
	Source   string `json:"source"`
	LinkKind int16  `json:"link_kind" doc:"0=exact 1=probable 2=related"`
	Count    int64  `json:"count"`
}

// QueueLevels is the human-review backlog.
type QueueLevels struct {
	CandidatesByStatus []StatusCount `json:"candidates_by_status"`
	ProposalsByStatus  []StatusCount `json:"proposals_by_status"`
	ProbableRefs       int64         `json:"probable_refs"`
	Rejections         int64         `json:"rejections"`
}

type StatusCount struct {
	Status int16 `json:"status"`
	Count  int64 `json:"count"`
}

// SourceFreshness is the honest freshness proxy: the latest external-ref
// created_at per source (no extra bookkeeping table added).
type SourceFreshness struct {
	Source    string `json:"source"`
	LatestRef string `json:"latest_ref,omitempty" doc:"RFC3339 max(created_at) of that source's anchors"`
}

// --- label reverse lookup ---

// LabelWorksResponse is a label's own identity plus a page of works attributed
// to it (self-sufficient for the reverse-lookup page).
type LabelWorksResponse struct {
	Label *LabelHead     `json:"label"`
	Total int64          `json:"total"`
	Items []LabelWorkRow `json:"items"`
}

// LabelHead is the label's own identity.
type LabelHead struct {
	ID          int64  `json:"id"`
	DisplayName string `json:"display_name"`
	Kind        int16  `json:"kind" doc:"label kind (0=game_brand … 4=doujin_circle …)"`
}

type LabelWorkRow struct {
	WorkID        int64  `json:"work_id"`
	DisplayName   string `json:"display_name"`
	MediumID      int16  `json:"medium_id"`
	ContentRating int16  `json:"content_rating"`
	Status        int16  `json:"status"`
	Kind          int16  `json:"kind" doc:"attribution edge kind"`
}
