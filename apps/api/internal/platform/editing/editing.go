// Package editing is the unified, media-agnostic editing engine (doc 21 /
// the edit_proposal primitive): field-level proposals, maintainer-edit
// (amend-then-merge), field-level permissions, and an append-only revision
// log with diff/revert — written ONCE, consumed by every entity family.
//
// Lifetime pillars (doc 21 §1.6):
//
//  1. Single write path — every user edit is a proposal; a direct edit is a
//     proposal + automerge sugar. There is no second write chain.
//  2. Append-only revision log + materialized current rows — revisions are
//     immutable; the entity row is a projection.
//  3. Eternal field-key contract (§2.2b) — patches, snapshots, and diffs
//     address REGISTERED FIELD KEYS (lowercase dotted strings such as
//     "catalog.work.display_name"), never Go fields or DB columns. A key is
//     never renamed or reused; the registry is the only key→column
//     translation layer, so physical models can be refactored forever
//     without migrating history.
//  4. Media-agnostic registry — the engine has ZERO knowledge of any entity
//     family. Field knowledge is injected: each family registers an
//     EntityTypeSpec (fields + apply/load closures carrying the family's own
//     DB pool) at its assembly point (charter ruling 1). This package must
//     never import a family package (galgame, catalog, ...); the
//     depsguard test pins that.
//
// Engine tables (edit_proposal / edit_proposal_amendment / edit_revision)
// live on the catalog DB pool (charter ruling 2) regardless of where the
// entity bodies live — the revision log is a single query surface.
//
// Scope boundary (charter ruling 4): the engine covers USER edits. System
// writes — importers, syncs, and the catalog merge machine (an identity
// operation, doc 21 §2.6) — do not pass through it.
package editing

// Entity families and types are opaque strings to the engine; the registry
// validates their shape (family is the first dotted segment of the type,
// e.g. family "catalog" / type "catalog.work").

// EntityRef addresses one entity row: (family, entity_type, entity_id).
// Family is derived from the registered spec, so wire surfaces only carry
// (entity_type, entity_id).
type EntityRef struct {
	Family string
	Type   string
	ID     int64
}

// Proposal status. Persisted values — NEVER renumber. The status column is
// `not null` WITHOUT a gorm default: 0 (open) is a meaningful value and the
// service layer always writes it explicitly (default-tag zero-value trap).
const (
	StatusOpen      int16 = 0
	StatusMerged    int16 = 1
	StatusDeclined  int16 = 2
	StatusWithdrawn int16 = 3
)

// StatusName maps a proposal status to its wire string. The S2S face speaks
// strings (self-documenting for the schema-driven UI); the DB stores ints.
var StatusName = map[int16]string{
	StatusOpen:      "open",
	StatusMerged:    "merged",
	StatusDeclined:  "declined",
	StatusWithdrawn: "withdrawn",
}

// Revision actions. Persisted values — NEVER renumber.
//
//   - created:  the entity row itself was created through the engine
//     (reserved; E0 pilot entities are created by their product write path).
//   - merged:   a reviewer merged an open proposal.
//   - direct:   the automerge sugar — proposal created and merged in one
//     atomic step by its proposer.
//   - reverted: a revert produced this revision (a reverse patch through the
//     SAME merge path — history is never deleted).
const (
	ActionCreated  int16 = 0
	ActionMerged   int16 = 1
	ActionDirect   int16 = 2
	ActionReverted int16 = 3
)

// ActionName maps a revision action to its wire string.
var ActionName = map[int16]string{
	ActionCreated:  "created",
	ActionMerged:   "merged",
	ActionDirect:   "direct",
	ActionReverted: "reverted",
}

// TrustedTier is the trust tier (doc 18, Discourse-style TL0-TL4 ladder) at
// which the "trusted" propose/automerge policy rules pass: TL2 (member) and
// above. D6 pins "trusted起步" for proposal rights; site overlays relax
// per-field from there.
const TrustedTier int16 = 2

// PolicyContext is the caller identity the policy rules evaluate against
// (charter ruling 6). The engine never resolves roles or trust itself — the
// assembly point injects a HasPerm closure (real RBAC resolver in
// production, a fake in tests) and the product backend asserts the trust
// tier it already knows.
type PolicyContext struct {
	UserID    int64
	Site      string
	TrustTier int16
	// HasPerm reports whether the caller holds a permission key (e.g.
	// "edit.catalog.work"). A nil HasPerm grants nothing (fail-closed).
	HasPerm func(key string) bool
}

func (pc PolicyContext) hasPerm(key string) bool {
	return pc.HasPerm != nil && pc.HasPerm(key)
}
