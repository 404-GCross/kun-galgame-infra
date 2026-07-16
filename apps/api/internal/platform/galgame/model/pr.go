package model

import (
	"encoding/json"

	"gorm.io/datatypes"
)

// GalgamePR represents a pull request (edit request) for a galgame
type GalgamePR struct {
	ID        int `gorm:"primaryKey;autoIncrement" json:"id"`
	GalgameID int `gorm:"column:galgame_id;not null;index" json:"galgame_id"`
	UserID    int `gorm:"column:user_id;not null;index" json:"user_id"`
	Status    int `gorm:"default:0;check:status IN (0,1,2)" json:"status"` // 0=pending, 1=merged, 2=declined
	// Title + Message are the PR's human description (the wiki UI shows two
	// inputs "PR 标题" + "变更说明" and renders pr.title / pr.message). The
	// retired single `note` column is left in the DB for historical rows; new
	// PRs write title/message. (Prod backfill if needed:
	// UPDATE galgame_pr SET message = note WHERE message = '' AND note <> '';)
	Title         string         `gorm:"type:text;default:''" json:"title"`
	Message       string         `gorm:"type:text;default:''" json:"message"`
	BaseRevision  int            `gorm:"column:base_revision;not null" json:"base_revision"`
	Snapshot      datatypes.JSON `gorm:"type:jsonb;not null" json:"snapshot"`
	CompletedBy   *int           `gorm:"column:completed_by" json:"completed_by,omitempty"`
	CompletedTime *Timestamp     `gorm:"column:completed_time" json:"completed_time,omitempty"`
	RevisionID    *int           `gorm:"column:revision_id" json:"revision_id,omitempty"`
	Created       Timestamp      `gorm:"autoCreateTime" json:"created"`
	Updated       Timestamp      `gorm:"autoUpdateTime" json:"updated"`
}

func (GalgamePR) TableName() string { return "galgame_pr" }

// GalgameRevision represents a version snapshot of a galgame
type GalgameRevision struct {
	ID        int `gorm:"primaryKey;autoIncrement" json:"id"`
	GalgameID int `gorm:"not null;uniqueIndex:idx_galgame_revision" json:"galgame_id"`
	Revision  int `gorm:"not null;uniqueIndex:idx_galgame_revision" json:"revision"`
	UserID    int `gorm:"not null;index" json:"user_id"`
	// Full action set produced by the galgame services. NOTE: GORM's
	// AutoMigrate only CREATES this CHECK on a fresh table — it never
	// ALTERs an existing one. When you add an action here you MUST also
	// bump the explicit DROP/ADD in cmd/migrate-catalog/galgame.go, or
	// existing wiki DBs keep the stale constraint and INSERTs 23514.
	// created/updated/merged/reverted/declined: revision_service,
	// galgame_service. claimed/edited_pending: submission_service.
	// approved/banned/unbanned/status_changed: admin_service.
	Action     string         `gorm:"size:20;not null;check:action IN ('created','updated','merged','reverted','declined','claimed','edited_pending','approved','banned','unbanned','status_changed')" json:"action"`
	Note       string         `gorm:"type:text;default:''" json:"note"`
	Snapshot   datatypes.JSON `gorm:"type:jsonb;not null" json:"snapshot"`
	IsMinor    bool           `gorm:"default:false" json:"is_minor"`
	RevertedTo *int           `gorm:"column:reverted_to" json:"reverted_to,omitempty"`
	// ChangedFields is the set of snapshot keys this revision actually changed
	// relative to the live state at edit time, stored as a jsonb array of
	// strings. It is RECORDED at write time (from ChangedKeys(live_before,
	// new)) rather than re-derived by /diff comparing adjacent snapshots —
	// which over-reported whenever VNDB enrichment mutated the live tables
	// without minting a revision, leaving an adjacent snapshot stale (see
	// docs/integration/galgame_wiki/02-revisions-and-prs.md). Mirrors
	// taxonomy_revision.changed_fields.
	//
	// Tri-state on purpose (NO default, so the column is nullable):
	//   NULL        → legacy revision written before this column existed;
	//                 /diff falls back to whole-snapshot ChangedKeys.
	//   []          → recorded, but no editable field changed (claim, admin
	//                 status transition — status is not a snapshot field).
	//   ["screenshots", …] → the recorded change set.
	// Use ChangedFieldsList / SetChangedFields / HasChangedFields on the Go
	// side; never read the raw bytes.
	ChangedFields datatypes.JSON `gorm:"column:changed_fields;type:jsonb" json:"changed_fields,omitempty"`
	Created       Timestamp      `gorm:"autoCreateTime" json:"created"`
}

func (GalgameRevision) TableName() string { return "galgame_revision" }

// HasChangedFields reports whether changed_fields was recorded (column is
// non-NULL). False = legacy revision → callers fall back to snapshot diff.
func (r *GalgameRevision) HasChangedFields() bool { return len(r.ChangedFields) > 0 }

// ChangedFieldsList decodes ChangedFields jsonb into a Go []string.
// NULL / parse error → nil (callers don't need to nil-check).
func (r *GalgameRevision) ChangedFieldsList() []string {
	if len(r.ChangedFields) == 0 {
		return nil
	}
	var out []string
	_ = json.Unmarshal(r.ChangedFields, &out)
	return out
}

// SetChangedFields encodes a []string into the ChangedFields column. Pass an
// empty (non-nil) slice for "recorded, nothing changed"; this still writes a
// non-NULL `[]` so /diff knows the set was recorded, not legacy.
func (r *GalgameRevision) SetChangedFields(fields []string) {
	if fields == nil {
		fields = []string{}
	}
	b, _ := json.Marshal(fields)
	r.ChangedFields = datatypes.JSON(b)
}

// GalgameHistory is the legacy history table (kept for migration, will be removed later)
type GalgameHistory struct {
	ID        int       `gorm:"primaryKey;autoIncrement" json:"id"`
	Action    string    `gorm:"default:''" json:"action"`
	Type      string    `gorm:"default:''" json:"type"`
	Content   string    `gorm:"size:1007;default:''" json:"content"`
	GalgameID int       `gorm:"column:galgame_id;not null;index" json:"galgame_id"`
	UserID    int       `gorm:"column:user_id;not null;index" json:"user_id"`
	Created   Timestamp `gorm:"autoCreateTime" json:"created"`
	Updated   Timestamp `gorm:"autoUpdateTime" json:"updated"`
}

func (GalgameHistory) TableName() string { return "galgame_history" }
