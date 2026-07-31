package dto

import "time"

// Wire types of the claim-lifecycle face (wave 155 W2/W3).
//
// The two feeds are deliberately NOT modelled on the retiring wiki feeds'
// DTOs. They cursor the same way (exclusive since-id, ascending) because that
// is what the downstream crons already implement, but every field name is
// catalog-native: a new id space wearing the old wire's spelling is how a
// consumer ends up assuming the old semantics still hold.

// ClaimActionRequest is the body of one lifecycle action.
type ClaimActionRequest struct {
	// Site is the acting tenant, enforced against the client's catalog_site
	// binding for the four owner actions. Review actions ignore it (a curator
	// acts across tenants) — the event still records the claim's own site.
	Site  string    `json:"site,omitempty" doc:"Acting tenant; must equal the client's catalog_site binding for owner actions"`
	Actor EditActor `json:"actor" doc:"The end user the product backend is acting for"`
	// ProductWorkID is required by `claim` and ignored by every other action:
	// it is the product-side id the registry row starts pointing at.
	ProductWorkID int64  `json:"product_work_id,omitempty" minimum:"0" doc:"The product-side work id to anchor (claim only)"`
	Reason        string `json:"reason,omitempty" doc:"Moderator note; REQUIRED for decline, recorded on the event"`
}

// StaffClaimActionRequest is the body of a curator's action. The actor is the
// operator's JWT, not an asserted user, so only the note is on the wire.
type StaffClaimActionRequest struct {
	Reason string `json:"reason,omitempty" doc:"Moderator note; REQUIRED for decline, recorded on the event"`
}

// ClaimTransitionInfo rides the 409 of an illegal transition so the caller can
// re-render without a second read.
type ClaimTransitionInfo struct {
	CurrentState string   `json:"current_state"`
	AllowedFrom  []string `json:"allowed_from"`
}

// ClaimEventFeedItem is one claim transition on the wire. from_state is null
// exactly once per claim: the transition that created it.
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

// ClaimEventFeed is one page of the claim-transition cursor feed.
type ClaimEventFeed struct {
	Items []ClaimEventFeedItem `json:"items"`
	// NextSince is the cursor to pass next time. It echoes the request's cursor
	// on an empty page, so a consumer that stores it unconditionally never
	// rewinds.
	NextSince int64 `json:"next_since"`
}

// CursorPage is one page of a DESCENDING, cursor-paged list (wave 157). It is
// a different shape from Page[T] on purpose: Page is offset-paged with a total
// only, while a list a user is actively writing to needs a stable cursor —
// here the id the next page must start below. Total accompanies it because on
// this face the count IS the per-user statistic downstream products used to
// need a separate endpoint for.
type CursorPage[T any] struct {
	Items []T `json:"items"`
	// NextBefore is the cursor for the following page; 0 = no more rows.
	NextBefore int64 `json:"next_before"`
	Total      int64 `json:"total"`
}

// EditRevisionFeedItem is one engine revision on the wire. The snapshot is
// deliberately absent: the feed exists so a consumer knows THAT an entity
// changed and which fields did, and shipping every full snapshot would make a
// routine catch-up page megabytes wide. The revision read face serves the
// snapshot for the rows a consumer actually cares about.
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
}

// EditRevisionFeed is one page of the global revision cursor feed.
type EditRevisionFeed struct {
	Items     []EditRevisionFeedItem `json:"items"`
	NextSince int64                  `json:"next_since"`
}
