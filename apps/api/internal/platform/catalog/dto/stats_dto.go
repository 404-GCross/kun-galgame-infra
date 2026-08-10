package dto

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

type EntityCounts struct {
	Persons           int64 `json:"persons"`
	CreditNames       int64 `json:"credit_names"`
	OrphanCreditNames int64 `json:"orphan_credit_names" doc:"credit_name.person_id IS NULL"`
	Labels            int64 `json:"labels"`
	Characters        int64 `json:"characters"`
}

type KeyCount struct {
	Key   string `json:"key"`
	Count int64  `json:"count"`
}

type KindCount struct {
	Kind  int16 `json:"kind"`
	Count int64 `json:"count"`
}

type AnchorTierCell struct {
	Source   string `json:"source"`
	LinkKind int16  `json:"link_kind" doc:"0=exact 1=probable 2=related"`
	Count    int64  `json:"count"`
}

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

type SourceFreshness struct {
	Source    string `json:"source"`
	LatestRef string `json:"latest_ref,omitempty" doc:"RFC3339 max(created_at) of that source's anchors"`
}

type LabelWorksResponse struct {
	Label *LabelHead     `json:"label"`
	Total int64          `json:"total"`
	Items []LabelWorkRow `json:"items"`
}

type LabelHead struct {
	ID          int64  `json:"id"`
	DisplayName string `json:"display_name"`
	Kind        int16  `json:"kind" doc:"label kind (0=game_brand … 4=doujin_circle …)"`
	LogoHash    string `json:"logo_hash" doc:"brand logo content hash in the image service; \"\" = none"`
}

type LabelWorkRow struct {
	WorkID        int64  `json:"work_id"`
	DisplayName   string `json:"display_name"`
	MediumID      int16  `json:"medium_id"`
	ContentRating int16  `json:"content_rating"`
	Status        int16  `json:"status"`
	Kind          int16  `json:"kind" doc:"attribution edge kind"`
}
