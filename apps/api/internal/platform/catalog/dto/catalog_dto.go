// Package dto holds the wire shapes of the catalog service API (S2S face +
// admin review queues). Field names mirror the storage models; the OpenAPI
// spec generated from these types is the contract.
package dto

import "time"

// --- S2S: resolve ---

// ResolveRequest asks for the canonical ids of up to 1000 entity ids.
type ResolveRequest struct {
	EntityType int16   `json:"entity_type" minimum:"0" maximum:"6" doc:"Entity type constant (0=person 1=credit_name 3=label 4=character 5=work 6=release; 2 is retired and unallocated)"`
	IDs        []int64 `json:"ids" minItems:"1" maxItems:"1000" doc:"Entity ids to resolve"`
}

// ResolveResponse maps every requested id to its canonical id; ids that were
// redirected are additionally listed in redirected.
type ResolveResponse struct {
	Mappings   map[string]int64 `json:"mappings" doc:"old id (as string key) → canonical id; unredirected ids map to themselves"`
	Redirected []int64          `json:"redirected,omitempty" doc:"The subset of requested ids that had a redirect"`
}

// --- S2S: redirect feed ---

// RedirectItem is one redirect edge in the feed.
type RedirectItem struct {
	EntityType int16     `json:"entity_type"`
	OldID      int64     `json:"old_id"`
	CurrentID  int64     `json:"current_id"`
	MergedAt   time.Time `json:"merged_at"`
}

// RedirectFeedResponse is a keyset page of redirects, oldest first. Clients
// persist next_cursor and pass it back on the next poll.
type RedirectFeedResponse struct {
	Items      []RedirectItem `json:"items"`
	NextCursor string         `json:"next_cursor,omitempty" doc:"Opaque cursor for the next page; empty when this page is not full"`
}

// --- S2S: work claim ---

// ClaimAnchor is one external identity the claiming product knows for the
// work.
type ClaimAnchor struct {
	SourceID   int16  `json:"source_id" doc:"catalog_source registry id (e.g. 2=vndb 3=bangumi 4=dlsite)"`
	ExternalID string `json:"external_id" minLength:"1"`
	// Level is the anchor's grain. "release" for a SKU-natured id (a DLsite
	// workno, a VNDB release — R3/R5); "work" (the default) for a work-natured
	// id (a Bangumi subject, a VNDB vn). It only steers where a FRESH mint
	// writes the ref — the adopt lookup already matches both levels — but a
	// consumer minting SKU ids MUST set "release" so a later catalog import
	// keyed on release anchors finds this identity instead of splitting it.
	Level string `json:"level,omitempty" enum:"work,release" doc:"Anchor grain; defaults to work"`
}

// ClaimWorkRequest registers/claims a work for a product-side row.
type ClaimWorkRequest struct {
	MediumID      int16         `json:"medium_id" doc:"catalog_medium registry id (1=galgame ...)"`
	Site          string        `json:"site" minLength:"1" doc:"Claiming site key (e.g. galgame_wiki)"`
	ProductWorkID int64         `json:"product_work_id" doc:"The work's id inside the product database"`
	DisplayName   string        `json:"display_name" minLength:"1"`
	OLang         string        `json:"olang,omitempty" doc:"Original language (BCP-47); defaults to ja"`
	ContentRating int16         `json:"content_rating,omitempty" minimum:"0" maximum:"2" doc:"0=all_ages 1=sensitive 2=r18"`
	Anchors       []ClaimAnchor `json:"anchors,omitempty" maxItems:"16" doc:"External identity anchors; matched exact refs claim the existing registry row"`
}

// ClaimWorkResponse returns the (single) catalog identity of the work.
type ClaimWorkResponse struct {
	WorkID  int64 `json:"work_id"`
	Created bool  `json:"created" doc:"true when a new registry row was minted (no anchor matched)"`
}

// ClaimConflictInfo is the structured body of a 409 claim conflict: the
// anchor already resolves to a work another product owns. The caller records
// this link (work_id ↔ its own row) rather than retrying — claims never
// preempt another site's identity.
type ClaimConflictInfo struct {
	WorkID              int64  `json:"work_id" doc:"The catalog work the anchor already resolves to"`
	OwningSite          string `json:"owning_site" doc:"The site key that holds the claim"`
	OwningProductWorkID int64  `json:"owning_product_work_id,omitempty" doc:"The owner's product-side work id, when known"`
}

// --- admin: shared page envelope ---

// Page carries offset-paginated admin listings.
type Page[T any] struct {
	Items []T   `json:"items"`
	Total int64 `json:"total"`
}
