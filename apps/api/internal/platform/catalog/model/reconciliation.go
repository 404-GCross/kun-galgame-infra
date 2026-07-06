package model

import (
	"time"

	"gorm.io/datatypes"
)

// Reconciliation infrastructure (doc 17 R7/R8 + doc 10 §6.1/§8): graded
// external-identity assertions, first-class negative knowledge, the match
// candidate queue, the merge-proposal cooling-off carrier, and the
// data-driven survivorship configuration.

// CatalogExternalRef is a graded assertion linking a catalog entity to an
// external identifier (doc 17 R7). Dual anchor points by entity_type:
// SKU-natured IDs (DLsite workno, VNDB release) anchor at release; work-
// natured IDs (Bangumi subject, VNDB vn) anchor at work; person/label/
// character IDs anchor at their entities.
//
// The anti-squatting line (doc 10 invariant 5): a partial unique index in
// the raw-SQL section makes the EXACT tier globally unique per (source_id,
// external_id, entity_type); probable/related tiers coexist freely.
type CatalogExternalRef struct {
	EntityType int16  `gorm:"primaryKey;autoIncrement:false" json:"entity_type"`
	EntityID   int64  `gorm:"primaryKey;autoIncrement:false" json:"entity_id"`
	SourceID   int16  `gorm:"primaryKey;autoIncrement:false;index:idx_catalog_external_ref_source_ext" json:"source_id"`
	ExternalID string `gorm:"primaryKey;index:idx_catalog_external_ref_source_ext" json:"external_id"`
	// LinkKind uses the LinkKind* constants — see constants.go for the tier
	// semantics; `related` NEVER participates in identity decisions.
	LinkKind int16 `gorm:"not null" json:"link_kind"`
	// MatchedBy makes every assertion traceable and revocable per rule:
	// 'rule:<rule-id>' (auto-exact whitelist) / 'human:<uid>' /
	// 'import:<job>'. Write paths set it explicitly, always.
	MatchedBy  string     `gorm:"not null" json:"matched_by"`
	VerifiedBy *int64     `json:"verified_by"`
	VerifiedAt *time.Time `json:"verified_at"`
	CreatedAt  time.Time  `json:"created_at"`

	Source *CatalogSource `gorm:"foreignKey:SourceID" json:"source,omitempty"`
}

func (CatalogExternalRef) TableName() string { return "catalog_external_ref" }

// CatalogMatchRejection is first-class negative knowledge (doc 17 R7,
// Wikidata deprecated-rank precedent): a human-rejected (entity, source,
// external_id) pairing, kept forever so the same mistake is never re-added.
// Import pipelines MUST consult this table: a hit → skip and count; the
// pairing never resurrects. Reason is mandatory (CHECK in the raw-SQL
// section) — the row exists to tell future importers and reviewers WHY not.
type CatalogMatchRejection struct {
	EntityType int16  `gorm:"primaryKey;autoIncrement:false" json:"entity_type"`
	EntityID   int64  `gorm:"primaryKey;autoIncrement:false" json:"entity_id"`
	SourceID   int16  `gorm:"primaryKey;autoIncrement:false" json:"source_id"`
	ExternalID string `gorm:"primaryKey" json:"external_id"`
	Reason     string `gorm:"not null" json:"reason"`
	RejectedBy *int64 `json:"rejected_by"`
	// DB-side DEFAULT now() (doc 17 R7 sketch): unlike the meaningful-zero
	// fields, a zero creation timestamp always means "unset", so the default
	// is the correct fallback for non-GORM writers too.
	RejectedAt time.Time `gorm:"not null;autoCreateTime;default:now()" json:"rejected_at"`
}

func (CatalogMatchRejection) TableName() string { return "catalog_match_rejection" }

// CatalogMatchCandidate is the dedup/identity candidate queue (doc 10 §8):
// rule-generated same-entity suggestions awaiting human decision. Pairs are
// normalized a<b (service side; CHECK pins it at the table layer). Rejected
// rows are kept forever — dedup runs against everything SEEN, not just
// what was confirmed, or rejected pairs resurface every import round.
type CatalogMatchCandidate struct {
	// EntityType joins the queue index AFTER status (composite index order
	// comes from the priority tags, not field order).
	EntityType int16 `gorm:"primaryKey;autoIncrement:false;index:idx_catalog_match_candidate_queue,priority:2" json:"entity_type"`
	AID        int64 `gorm:"primaryKey;autoIncrement:false" json:"a_id"`
	BID        int64 `gorm:"primaryKey;autoIncrement:false" json:"b_id"`
	// Reason uses the CandidateReason* constants.
	Reason int16 `gorm:"not null" json:"reason"`
	// Score is the generator's confidence, informational only (self-reported
	// LLM confidence is systematically overconfident — doc 17 §4).
	Score *float32 `json:"score"`
	// Status uses the CandidateStatus* constants; queue polling reads
	// (status, entity_type).
	Status    int16      `gorm:"not null;index:idx_catalog_match_candidate_queue,priority:1" json:"status"`
	CreatedAt time.Time  `json:"created_at"`
	DecidedBy *int64     `json:"decided_by"`
	DecidedAt *time.Time `json:"decided_at"`
}

func (CatalogMatchCandidate) TableName() string { return "catalog_match_candidate" }

// CatalogMergeProposal is the carrier of destructive operations (doc 10
// §6.1): merges (and future deletes) are proposals with a mandatory
// cooling-off window, never immediate.
type CatalogMergeProposal struct {
	ID         int64 `gorm:"primaryKey;autoIncrement:false;type:bigint GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY;default:(-)" json:"id"`
	EntityType int16 `gorm:"not null;index:idx_catalog_merge_proposal_queue,priority:2" json:"entity_type"`
	// source/target of the merge: source is absorbed into target. Named
	// *_entity_id (not doc 10's source_id/target_id) to avoid colliding
	// with the catalog_source registry vocabulary.
	SourceEntityID int64 `gorm:"not null" json:"source_entity_id"`
	TargetEntityID int64 `gorm:"not null" json:"target_entity_id"`
	// Status uses the ProposalStatus* constants.
	Status int16 `gorm:"not null;index:idx_catalog_merge_proposal_queue,priority:1" json:"status"`
	// FieldResolution holds explicit per-field survivorship overrides chosen
	// by the approver; empty = default rules apply (doc 10 §6.2).
	FieldResolution datatypes.JSON `gorm:"type:jsonb;not null;default:'{}'" json:"field_resolution"`
	ProposedBy      int64          `gorm:"not null" json:"proposed_by"`
	ProposedAt      time.Time      `gorm:"not null;autoCreateTime" json:"proposed_at"`
	// ExecuteAfter = approved_at + >=48h (doc 10 invariant 4): the
	// cooling-off window is mandatory and admins are NOT exempt. Assignment
	// on approval and the execution job are step 05.
	ExecuteAfter *time.Time `json:"execute_after"`
	ExecutedAt   *time.Time `json:"executed_at"`
	Note         string     `gorm:"not null;default:''" json:"note"`
}

func (CatalogMergeProposal) TableName() string { return "catalog_merge_proposal" }

// CatalogSurvivorshipRule is the data-driven per-field source priority
// configuration (doc 17 R8, Informatica/Reltio/Semarchy shape): smaller
// priority wins. The engine (step 08) guarantees the user source always
// outranks every import source regardless of rows here; user-owned field
// values are never overwritten by imports (doc 10 invariant 6). No seeds in
// this step — rules land with the P2a enrichment.
type CatalogSurvivorshipRule struct {
	EntityType int16  `gorm:"primaryKey;autoIncrement:false" json:"entity_type"`
	Field      string `gorm:"primaryKey" json:"field"`
	SourceID   int16  `gorm:"primaryKey;autoIncrement:false" json:"source_id"`
	Priority   int16  `gorm:"not null" json:"priority"`

	Source *CatalogSource `gorm:"foreignKey:SourceID" json:"source,omitempty"`
}

func (CatalogSurvivorshipRule) TableName() string { return "catalog_survivorship_rule" }
