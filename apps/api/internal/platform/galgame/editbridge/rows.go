// Package editbridge is the galgame family's strangler adapter over the
// editing engine (E2a, 03 号裁定 2/3): it reconstructs the OLD galgame wire
// shapes (model.GalgameRevision / model.GalgamePR) from the edit_* tables, and
// hosts the one-shot legacy transform that moves galgame_revision/galgame_pr
// into the unified revision log. apps/wiki keeps consuming the byte-compatible
// old wire until E3; this package dies with it.
//
// The engine's Go models stay pure — the legacy_* bookkeeping columns
// (catalog/migrate.EditLegacyColumns) are mapped here by package-local row
// structs, and only the transform ever writes them.
package editbridge

import (
	"encoding/json"
	"time"

	"gorm.io/datatypes"
)

// revisionRow maps edit_revision INCLUDING the legacy bookkeeping columns.
// Every column tag is explicit (the UID→u_i_d acronym snake-case trap).
type revisionRow struct {
	ID            int64          `gorm:"column:id"`
	EntityFamily  string         `gorm:"column:entity_family"`
	EntityType    string         `gorm:"column:entity_type"`
	EntityID      int64          `gorm:"column:entity_id"`
	Seq           int            `gorm:"column:seq"`
	Action        int16          `gorm:"column:action"`
	ChangedFields datatypes.JSON `gorm:"column:changed_fields"`
	Snapshot      datatypes.JSON `gorm:"column:snapshot"`
	ActorUID      int64          `gorm:"column:actor_uid"`
	AmenderUID    *int64         `gorm:"column:amender_uid"`
	ProposalID    *int64         `gorm:"column:proposal_id"`
	Site          string         `gorm:"column:site"`
	CreatedAt     time.Time      `gorm:"column:created_at"`
	LegacyAction  *string        `gorm:"column:legacy_action"`
	LegacyID      *int64         `gorm:"column:legacy_id"`
	LegacyMeta    datatypes.JSON `gorm:"column:legacy_meta"`
}

func (revisionRow) TableName() string { return "edit_revision" }

// wireID is the old-wire id: the source-row PK for migrated rows, the engine
// id for new-era rows (the transform bumps the identity sequence past every
// legacy id, so the two ranges never collide).
func (r *revisionRow) wireID() int64 {
	if r.LegacyID != nil {
		return *r.LegacyID
	}
	return r.ID
}

// proposalRow maps edit_proposal including the legacy bookkeeping columns.
type proposalRow struct {
	ID              int64          `gorm:"column:id"`
	EntityFamily    string         `gorm:"column:entity_family"`
	EntityType      string         `gorm:"column:entity_type"`
	EntityID        int64          `gorm:"column:entity_id"`
	BaseRevisionSeq int            `gorm:"column:base_revision_seq"`
	Patch           datatypes.JSON `gorm:"column:patch"`
	ProposerUID     int64          `gorm:"column:proposer_uid"`
	Note            string         `gorm:"column:note"`
	Site            string         `gorm:"column:site"`
	Status          int16          `gorm:"column:status"`
	DecidedByUID    *int64         `gorm:"column:decided_by_uid"`
	DecidedAt       *time.Time     `gorm:"column:decided_at"`
	DecisionNote    string         `gorm:"column:decision_note"`
	CreatedAt       time.Time      `gorm:"column:created_at"`
	UpdatedAt       time.Time      `gorm:"column:updated_at"`
	LegacyPRID      *int64         `gorm:"column:legacy_pr_id"`
	LegacyMeta      datatypes.JSON `gorm:"column:legacy_meta"`
}

func (proposalRow) TableName() string { return "edit_proposal" }

func (p *proposalRow) wireID() int64 {
	if p.LegacyPRID != nil {
		return *p.LegacyPRID
	}
	return p.ID
}

// revisionLegacyMeta is edit_revision.legacy_meta: the old-wire-only fields
// the engine does not model. Keys are present only when meaningful.
type revisionLegacyMeta struct {
	Note    string `json:"note,omitempty"`
	IsMinor bool   `json:"is_minor,omitempty"`
	// RevertedTo is the old 'reverted' rows' target revision number.
	RevertedTo *int `json:"reverted_to,omitempty"`
	// NullChangedFields marks rows whose source changed_fields was NULL
	// (legacy tri-state): the stored changed_fields carries the derived
	// /diff fallback, and the old wire must keep OMITTING the field.
	NullChangedFields bool `json:"null_changed_fields,omitempty"`
}

func decodeRevisionMeta(raw datatypes.JSON) revisionLegacyMeta {
	var m revisionLegacyMeta
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &m)
	}
	return m
}

// proposalLegacyMeta is edit_proposal.legacy_meta: the exact old PR fields the
// wire must reproduce byte-for-byte for migrated rows. All keys always present.
type proposalLegacyMeta struct {
	Title        string          `json:"title"`
	Message      string          `json:"message"`
	Snapshot     json.RawMessage `json:"snapshot"`
	BaseRevision int             `json:"base_revision"`
}

func decodeProposalMeta(raw datatypes.JSON) (*proposalLegacyMeta, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var m proposalLegacyMeta
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, false
	}
	return &m, true
}
