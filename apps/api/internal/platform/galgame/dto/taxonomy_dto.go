package dto

// RecentTaxonomyRequest is the query of GET /galgame/taxonomy/recent — the
// cross-entity "taxonomy changed" feed. Same consumption pattern as
// GET /galgame/revisions/recent: since_id cursor + has_more, Basic Auth (OAuth
// client). entity / action are optional server-side filters — e.g. the forum
// pulls entity=series&action=created for its "new series" homepage card.
type RecentTaxonomyRequest struct {
	Entity  string `query:"entity" validate:"omitempty,oneof=tag official engine series"`
	Action  string `query:"action" validate:"omitempty,oneof=created updated deleted reverted"`
	SinceID int64  `query:"since_id" validate:"omitempty,min=0"`
	Limit   int    `query:"limit" validate:"omitempty,min=1,max=5000"`
}

// TaxonomyFeedItem is one taxonomy change event (tag/official/engine/series ×
// created/updated/deleted/reverted) for downstream activity timelines.
//
// Self-describing: it carries the entity's display Name (read from the revision
// snapshot) so a consumer renders a card without a follow-up fetch — same design
// as RevisionFeedItem. ID is taxonomy_revision.id (global), the feed cursor;
// TargetID is the entity row id (e.g. the series id), for linking to its page.
type TaxonomyFeedItem struct {
	ID       int64  `json:"id"`
	Entity   string `json:"entity"`
	TargetID int    `json:"target_id"`
	Revision int    `json:"revision"`
	Action   string `json:"action"`
	UserID   int    `json:"user_id"`
	Name     string `json:"name"`
	Created  string `json:"created"`
}

// TaxonomyFeedResponse wraps the feed with has_more for cursor paging.
type TaxonomyFeedResponse struct {
	Items   []TaxonomyFeedItem `json:"items"`
	HasMore bool               `json:"has_more"`
}
