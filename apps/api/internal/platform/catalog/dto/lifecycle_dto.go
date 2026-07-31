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

// WorkSubmitRequest mints a work in the pending claim state (wave 162).
//
// The content half is keyed exactly like an edit patch — the SAME field keys
// the editing face registers for catalog.work — so a wizard renders one form
// from one schema and posts it here to create or there to amend. Two keys the
// matrix does not have and this face therefore does not accept: a work-level
// release date (send `released` instead: it becomes one curated release row)
// and a status (lifecycle is claim_state, moved only by the semantic actions).
type WorkSubmitRequest struct {
	Site  string    `json:"site" minLength:"1" doc:"Submitting tenant; must equal the client's catalog_site binding"`
	Actor EditActor `json:"actor" doc:"The end user the product backend is submitting for"`
	// ProductWorkID is OPTIONAL (charter §6.P4-verdict 1). Supplied = the
	// product allocated the id first and the registry records it; omitted = the
	// registry issues the identity and the claim adopts the minted work's own
	// id, which the response hands back for the product to create its local row
	// at.
	ProductWorkID int64           `json:"product_work_id,omitempty" minimum:"0" doc:"The product-side work id to anchor this submission at. OMIT IT to have the registry issue the identity: the claim then adopts the minted work id, which the response returns. Idempotency depends on this choice — see the endpoint summary"`
	Fields        map[string]any  `json:"fields" doc:"Field-key → value, the submission subset of catalog.work: display_name (required), olang, content_rating, titles, intros, display_nsfw, tag_ids, labels, engine_ids, series_ids, links. covers/screenshots are NOT accepted here — upload the bytes, then edit those facets"`
	Released      *WorkSubmitDate `json:"released,omitempty" doc:"Optional submitted release date; becomes ONE curated catalog_release row. Omit for TBA"`
}

// WorkSubmitDate is the fuzzy submitted date. The nullable tail IS the
// precision — {y:2019} means "sometime in 2019" — which is why there is no
// separate precision enum (03 §6-1).
type WorkSubmitDate struct {
	Y int16 `json:"y" minimum:"1970" maximum:"2200"`
	M int16 `json:"m,omitempty" minimum:"0" maximum:"12" doc:"0 = unknown month"`
	D int16 `json:"d,omitempty" minimum:"0" maximum:"31" doc:"0 = unknown day; requires m"`
}

// WorkSubmitResponse is the minted identity plus the birth event.
type WorkSubmitResponse struct {
	WorkID int64 `json:"work_id"`
	// ProductWorkID is the id the claim is anchored at — the supplied one, or
	// the registry-issued one (equal to work_id) when the request omitted it.
	// A product that omitted it creates its local row at THIS id.
	ProductWorkID int64  `json:"product_work_id"`
	ClaimState    string `json:"claim_state" doc:"Always pending — a submission is born awaiting review"`
	EventID       int64  `json:"event_id" doc:"The claim-event row recording the birth (from_state null → pending)"`
	ReleaseID     int64  `json:"release_id,omitempty" doc:"The curated release row the submitted date produced; absent when no date was given"`
}

// WorkSubmitConflictInfo rides the 409 of a repeat submission so the wizard can
// resume the existing work instead of retrying the mint.
type WorkSubmitConflictInfo struct {
	WorkID        int64  `json:"work_id"`
	ProductWorkID int64  `json:"product_work_id"`
	CurrentState  string `json:"current_state"`
	// MatchedBy names which idempotency key recognized the request: `claim`
	// (site + product_work_id, exact) or `anchor` (an identity ref the payload
	// asserted, which also fires when a DIFFERENT user already registered this
	// game for the same site).
	MatchedBy string `json:"matched_by" doc:"claim | anchor"`
	Anchor    string `json:"anchor,omitempty" doc:"The matching identity coordinate (source:external_id), for matched_by=anchor"`
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
