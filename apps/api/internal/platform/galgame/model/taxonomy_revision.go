package model

import (
	"encoding/json"
	"time"

	"gorm.io/datatypes"
)

// Taxonomy entity discriminators. These string values appear in
// taxonomy_revision.entity and are the only legal values (DB-enforced
// CHECK constraint). Adding a fifth entity = add a new constant +
// extend the CHECK + add a Go Snapshot struct + Apply/Take helper;
// NO new table required.
const (
	TaxonomyEntityTag      = "tag"
	TaxonomyEntityOfficial = "official"
	TaxonomyEntityEngine   = "engine"
	TaxonomyEntitySeries   = "series"
)

// Taxonomy revision actions. Set:
//   - created  : entity first appeared (initial state in Snapshot)
//   - updated  : scalar / aliases edit (Snapshot = post-edit state)
//   - deleted  : entity hard-deleted (Snapshot = LAST state before deletion,
//                kept so future "undo delete" can replay it)
//   - reverted : roll back to a historical snapshot (Snapshot = the state
//                we just rolled to, also = current DB state post-revert)
const (
	TaxonomyActionCreated  = "created"
	TaxonomyActionUpdated  = "updated"
	TaxonomyActionDeleted  = "deleted"
	TaxonomyActionReverted = "reverted"
)

// TaxonomyRevision is the single polymorphic full-snapshot audit/revision
// table covering tag / official / engine / series. Schema is identical
// across entities — the entity column is the discriminator and the
// snapshot jsonb's interpretation depends on it.
//
// Design rationale lives in docs/galgame_wiki/09-final-upgrade-plan.md
// §6.1; the gist: one table beats four near-identical ones at this
// scale, with the explicitly accepted trade-off (logged in §9.1) of
// losing DB-level target_id referential integrity.
//
// Concurrency: writes MUST first `SELECT FOR UPDATE` the corresponding
// main entity row (galgame_tag / galgame_official / galgame_engine /
// galgame_series) so two simultaneous edits on the same entity can't
// allocate the same Revision number. See docs §2 (concurrency
// declaration).
type TaxonomyRevision struct {
	ID int `gorm:"primaryKey;autoIncrement" json:"id"`

	// Entity + TargetID + Revision form the per-entity revision sequence.
	// idx_taxonomy_revision enforces uniqueness so NextTaxonomyRevision
	// can safely use max(revision)+1 under the main-row lock.
	Entity   string `gorm:"size:16;not null;uniqueIndex:idx_taxonomy_revision,priority:1;check:entity IN ('tag','official','engine','series')" json:"entity"`
	TargetID int    `gorm:"column:target_id;not null;uniqueIndex:idx_taxonomy_revision,priority:2" json:"target_id"`
	Revision int    `gorm:"not null;uniqueIndex:idx_taxonomy_revision,priority:3" json:"revision"`

	Action string `gorm:"size:16;not null;check:action IN ('created','updated','deleted','reverted')" json:"action"`

	UserID   int `gorm:"column:user_id;not null;index" json:"user_id"`
	UserRole int `gorm:"column:user_role;not null" json:"user_role"`

	// Snapshot is the full editable state of the entity at this
	// revision. The Go struct it deserialises to depends on Entity
	// (TagSnapshot / OfficialSnapshot / EngineSnapshot / SeriesSnapshot).
	Snapshot datatypes.JSON `gorm:"type:jsonb;not null" json:"snapshot"`

	// ChangedFields lists field names that differ from the prior
	// revision, stored as a jsonb array of strings. Semantics per §6.5:
	//   created  → all field names of the snapshot
	//   updated  → only the changed field names
	//   deleted  → empty array (entire row gone, not "fields changed")
	//   reverted → diff between target snapshot and prior state
	// Future field-level RBAC reads this directly; not a sentinel.
	//
	// Stored as jsonb (not Postgres text[]) to match the project's
	// existing precedent (model.GalgameEngine.Alias) and avoid pulling
	// in lib/pq just for an array column. Use ChangedFieldsList /
	// SetChangedFields helpers on the Go side.
	ChangedFields datatypes.JSON `gorm:"type:jsonb;default:'[]'" json:"changed_fields"`

	// RefCount is only meaningful for action='deleted': how many galgame
	// rows referenced this entity at the moment of deletion. Helps
	// admin understand the blast radius post-hoc.
	RefCount int `gorm:"not null;default:0" json:"ref_count"`

	// AffectedGalgameIDs is only set for action='deleted': the IDs of
	// galgame rows whose relation rows were purged by the force-delete,
	// stored as a jsonb array of ints. Future "undo delete" UI shows
	// these to the admin as "the tag was previously used in these N
	// works; pick which to re-attach". The list is captured for free
	// during force-purge (we already SELECT those gids to write
	// per-galgame revisions); persisting it here turns it into durable
	// audit data.
	AffectedGalgameIDs datatypes.JSON `gorm:"column:affected_galgame_ids;type:jsonb;default:'[]'" json:"affected_galgame_ids,omitempty"`

	Note    string    `gorm:"type:text;default:''" json:"note"`
	Created time.Time `gorm:"autoCreateTime" json:"created"`
}

func (TaxonomyRevision) TableName() string { return "taxonomy_revision" }

// ChangedFieldsList decodes ChangedFields jsonb into a Go []string.
// Empty bytes / parse error → empty slice (callers don't need to nil-check).
func (r *TaxonomyRevision) ChangedFieldsList() []string {
	if len(r.ChangedFields) == 0 {
		return nil
	}
	var out []string
	_ = json.Unmarshal(r.ChangedFields, &out)
	return out
}

// SetChangedFields encodes a []string into the ChangedFields column.
// Pass nil or empty for the "no fields changed" representation
// (used by created/deleted per §6.5).
func (r *TaxonomyRevision) SetChangedFields(fields []string) {
	if fields == nil {
		fields = []string{}
	}
	b, _ := json.Marshal(fields)
	r.ChangedFields = datatypes.JSON(b)
}

// AffectedGalgameIDList decodes AffectedGalgameIDs jsonb into a Go []int.
// Same lenient semantics as ChangedFieldsList.
func (r *TaxonomyRevision) AffectedGalgameIDList() []int {
	if len(r.AffectedGalgameIDs) == 0 {
		return nil
	}
	var out []int
	_ = json.Unmarshal(r.AffectedGalgameIDs, &out)
	return out
}

// SetAffectedGalgameIDs encodes a []int into the AffectedGalgameIDs column.
func (r *TaxonomyRevision) SetAffectedGalgameIDs(ids []int) {
	if ids == nil {
		ids = []int{}
	}
	b, _ := json.Marshal(ids)
	r.AffectedGalgameIDs = datatypes.JSON(b)
}

// TagSnapshot is the full editable state of a tag at a point in time.
// Mirrors model.GalgameTag's editable surface (id and timestamps are
// not editable, so excluded). Aliases canonical-sort applied in
// TakeTagSnapshot so two semantically-equal snapshots serialise
// identically (ChangedKeys-stable).
type TagSnapshot struct {
	Name        string   `json:"name"`
	Category    string   `json:"category"`
	Description string   `json:"description"`
	Aliases     []string `json:"aliases"`
}

// OfficialSnapshot — same shape language as TagSnapshot, plus Original
// (native-script name), Link (homepage URL), Lang (region code).
type OfficialSnapshot struct {
	Name        string   `json:"name"`
	Original    string   `json:"original"`
	Link        string   `json:"link"`
	Category    string   `json:"category"`
	Lang        string   `json:"lang"`
	Description string   `json:"description"`
	Aliases     []string `json:"aliases"`
}

// EngineSnapshot — engine has no separate alias table; aliases live in
// a jsonb column on galgame_engine itself. Snapshot still presents them
// as a sorted string slice so the Snapshot/diff layer is uniform
// across all four entities.
type EngineSnapshot struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Aliases     []string `json:"aliases"`
}

// SeriesSnapshot — series only carries Name + Description. Membership
// (which galgame_id values belong to this series) is the FK
// galgame.series_id, which is owned by the galgame side and recorded
// in galgame_revision when changed. Putting GalgameIDs here would
// double-bookkeep that relation, so it is intentionally absent —
// see §7.5 of the upgrade plan.
type SeriesSnapshot struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}
