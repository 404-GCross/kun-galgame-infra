package dto

import "time"

type StaffClaimActionRequest struct {
	Reason string `json:"reason,omitempty" doc:"Moderator note; REQUIRED for decline, recorded on the event"`
}

type UserClaimActionRequest struct {
	ProductWorkID *int64 `json:"product_work_id,omitempty" minimum:"1" doc:"The product-side work id to anchor (claim only)"`
	Reason        string `json:"reason,omitempty" doc:"Moderator note; REQUIRED for decline, recorded on the event"`
}

type UserWorkSubmitRequest struct {
	ProductWorkID *int64          `json:"product_work_id,omitempty" minimum:"1" doc:"The product-side work id to anchor this submission at. OMIT IT to have the registry issue the identity: the claim then adopts the minted work id, which the response returns. Idempotency depends on this choice — see the endpoint summary"`
	Fields        map[string]any  `json:"fields" doc:"Field-key → value, the submission subset of catalog.work: display_name (required), olang, content_rating, titles, intros, display_nsfw, tag_ids, labels, engine_ids, series_ids, links. covers/screenshots are NOT accepted here — upload the bytes, then edit those facets"`
	Released      *WorkSubmitDate `json:"released,omitempty" doc:"Optional submitted release date; becomes ONE curated catalog_release row. Omit for TBA"`
}

type WorkSubmitDate struct {
	Y int16 `json:"y" minimum:"1970" maximum:"2200"`
	M int16 `json:"m,omitempty" minimum:"0" maximum:"12" doc:"0 = unknown month"`
	D int16 `json:"d,omitempty" minimum:"0" maximum:"31" doc:"0 = unknown day; requires m"`
}

type WorkSubmitResponse struct {
	WorkID        int64  `json:"work_id"`
	ProductWorkID int64  `json:"product_work_id"`
	ClaimState    string `json:"claim_state" doc:"Always pending — a submission is born awaiting review"`
	EventID       int64  `json:"event_id" doc:"The claim-event row recording the birth (from_state null → pending)"`
	ReleaseID     int64  `json:"release_id,omitempty" doc:"The curated release row the submitted date produced; absent when no date was given"`
}

type WorkSubmitConflictInfo struct {
	WorkID        int64  `json:"work_id"`
	ProductWorkID int64  `json:"product_work_id"`
	CurrentState  string `json:"current_state"`
	MatchedBy     string `json:"matched_by" doc:"claim | anchor"`
	Anchor        string `json:"anchor,omitempty" doc:"The matching identity coordinate (source:external_id), for matched_by=anchor"`
}

type ClaimTransitionInfo struct {
	CurrentState string   `json:"current_state"`
	AllowedFrom  []string `json:"allowed_from"`
}

type ClaimEventFeedItem struct {
	ID            int64     `json:"id"`
	WorkID        int64     `json:"work_id"`
	FromState     *string   `json:"from_state"`
	ToState       string    `json:"to_state"`
	ActorUID      int64     `json:"actor_uid"`
	Reason        *string   `json:"reason"`
	Site          string    `json:"site"`
	ProductWorkID *int64    `json:"product_work_id" doc:"The claim's CURRENT product-side id (a snapshot, not the value at event time)"`
	CreatedAt     time.Time `json:"created_at"`
}

type ClaimEventFeed struct {
	Items     []ClaimEventFeedItem `json:"items"`
	NextSince int64                `json:"next_since"`
}

type CursorPage[T any] struct {
	Items      []T   `json:"items"`
	NextBefore int64 `json:"next_before"`
	Total      int64 `json:"total"`
}

type EditRevisionFeedItem struct {
	ID            int64     `json:"id"`
	EntityFamily  string    `json:"entity_family"`
	EntityType    string    `json:"entity_type"`
	EntityID      int64     `json:"entity_id"`
	Seq           int       `json:"seq"`
	Action        int16     `json:"action" doc:"0=created 1=merged 2=direct 3=reverted"`
	ChangedFields []string  `json:"changed_fields"`
	ActorUID      int64     `json:"actor_uid"`
	AmenderUID    *int64    `json:"amender_uid"`
	ProposalID    *int64    `json:"proposal_id"`
	Site          string    `json:"site"`
	CreatedAt     time.Time `json:"created_at"`
	ProductWorkID *int64    `json:"product_work_id"`
}

type EditRevisionFeed struct {
	Items     []EditRevisionFeedItem `json:"items"`
	NextSince int64                  `json:"next_since"`
}
