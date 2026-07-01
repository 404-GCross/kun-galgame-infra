package dto

import "api/internal/platform/galgame/model"

// Typed read DTOs for the revision / PR endpoints. model.GalgameRevision and
// model.GalgamePR store the snapshot as a jsonb blob (datatypes.JSON); these
// decode it to a structured model.Snapshot (itself all plain JSON types) so the
// OpenAPI spec + generated TS carry the real snapshot shape instead of an opaque
// object. Timestamps are model.Timestamp (typed as date-time strings via its
// huma.SchemaProvider). Wire-equality with the raw models is pinned by
// revision_read_dto_test.go.

// RevisionResponse is the typed form of model.GalgameRevision.
type RevisionResponse struct {
	ID            int             `json:"id"`
	GalgameID     int             `json:"galgame_id"`
	Revision      int             `json:"revision"`
	UserID        int             `json:"user_id"`
	Action        string          `json:"action"`
	Note          string          `json:"note"`
	Snapshot      *model.Snapshot `json:"snapshot"`
	IsMinor       bool            `json:"is_minor"`
	RevertedTo    *int            `json:"reverted_to,omitempty"`
	ChangedFields []string        `json:"changed_fields,omitempty"`
	Created       model.Timestamp `json:"created"`
}

// NewRevisionResponse maps a revision row, decoding its jsonb snapshot.
func NewRevisionResponse(r *model.GalgameRevision) RevisionResponse {
	out := RevisionResponse{
		ID: r.ID, GalgameID: r.GalgameID, Revision: r.Revision, UserID: r.UserID,
		Action: r.Action, Note: r.Note, IsMinor: r.IsMinor, RevertedTo: r.RevertedTo,
		ChangedFields: r.ChangedFieldsList(), Created: r.Created,
	}
	if s, err := model.SnapshotFromJSON([]byte(r.Snapshot)); err == nil {
		out.Snapshot = s
	}
	return out
}

// PRResponse is the typed form of model.GalgamePR.
type PRResponse struct {
	ID            int              `json:"id"`
	GalgameID     int              `json:"galgame_id"`
	UserID        int              `json:"user_id"`
	Status        int              `json:"status"`
	Title         string           `json:"title"`
	Message       string           `json:"message"`
	BaseRevision  int              `json:"base_revision"`
	Snapshot      *model.Snapshot  `json:"snapshot"`
	CompletedBy   *int             `json:"completed_by,omitempty"`
	CompletedTime *model.Timestamp `json:"completed_time,omitempty"`
	RevisionID    *int             `json:"revision_id,omitempty"`
	Created       model.Timestamp  `json:"created"`
	Updated       model.Timestamp  `json:"updated"`
}

// NewPRResponse maps a PR row, decoding its jsonb snapshot.
func NewPRResponse(p *model.GalgamePR) PRResponse {
	out := PRResponse{
		ID: p.ID, GalgameID: p.GalgameID, UserID: p.UserID, Status: p.Status,
		Title: p.Title, Message: p.Message, BaseRevision: p.BaseRevision,
		CompletedBy: p.CompletedBy, CompletedTime: p.CompletedTime, RevisionID: p.RevisionID,
		Created: p.Created, Updated: p.Updated,
	}
	if s, err := model.SnapshotFromJSON([]byte(p.Snapshot)); err == nil {
		out.Snapshot = s
	}
	return out
}

// RevisionListData / PRListData are the {items, total} list payloads.
type RevisionListData struct {
	Items []RevisionResponse `json:"items"`
	Total int64              `json:"total"`
}
type PRListData struct {
	Items []PRResponse `json:"items"`
	Total int64        `json:"total"`
}

// RevisionDiffData is the body of GET /galgame/:gid/revisions/:rev/diff.
type RevisionDiffData struct {
	ChangedKeys map[string]bool      `json:"changed_keys"`
	Old         *model.Snapshot      `json:"old"`
	New         *model.Snapshot      `json:"new"`
	Names       *SnapshotEntityNames `json:"names"`
}

// PRDetailData is the body of GET /galgame/:gid/prs/:id — the PR plus its diff
// against the base revision (changed keys + entity display names).
type PRDetailData struct {
	PR          PRResponse           `json:"pr"`
	ChangedKeys map[string]bool      `json:"changed_keys"`
	Names       *SnapshotEntityNames `json:"names"`
}
