// Package imagerefs is the ONE registry of every catalog row that holds an
// image hash.
//
// A new image column that is not added to the registry below is invisible to
// three separate lanes at once:
//
//   - catalog-image-refping, the daily keep-alive sweep. Nothing fails when you
//     forget: uploads succeed, the read face renders, and the bytes are
//     collected a year later once the image service's TTL elapses (>365d
//     unreferenced → soft-delete, +30d → physical delete). The failure is
//     silent and arrives a year late — this is the "refping site-scope GC fuse"
//     class that once froze 66k galgame images.
//   - image-ref-audit, the daily reconcile that catches the opposite direction
//     (bytes deleted out from under a live catalog row); a column missing here
//     is a column whose blank galleries nobody notices until a user complains.
//   - the admin console's pre-delete reference check, which asks this package
//     who points at a hash before an operator destroys it. A missing column
//     means the console reports "no references" for an image that has some,
//     which is how the operator is talked into the deletion.
//
// So: adding an image column anywhere in catalog scope means adding it HERE in
// the same change.
//
// Two posture rules the registry encodes and the SQL must keep:
//
//   - Work covers and screenshots are taken in FULL — no claim/shadow filter
//     (§8.B shadow-never-delete). A bodyless media row that a later claim
//     shadowed still owns bytes in the catalog scope, so it must keep being
//     pinged; missing it = GC eats a live image.
//   - Rows on soft-deletable entities (character / label / person) count only
//     while the entity is live: the image is referenced iff a live row still
//     points at it, and a soft-deleted entity's image may legitimately age out.
package imagerefs

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// Kind identifiers, stable on the wire (the admin console and the audit's
// per-kind counters both read them).
const (
	KindWorkCover       = "work_cover"
	KindWorkScreenshot  = "work_screenshot"
	KindCharacterBust   = "character_bust"
	KindCharacterFigure = "character_figure"
	KindLabelLogo       = "label_logo"
	KindPersonPhoto     = "person_photo"
)

// kindSpec describes one image-bearing column: where it lives, which entity
// owns it, how a human names that entity, and what "no image" means for it.
// Every query in this package is generated from these, so a seventh image
// column is one entry rather than three near-identical UNION blocks.
type kindSpec struct {
	Kind         string
	Table        string
	HashColumn   string
	EntityColumn string
	// LabelTable/LabelJoinOn are empty when the human-readable name lives on
	// Table itself (the entity IS the row); otherwise the label is LEFT
	// JOINed so a dangling FK never drops a reference from the answer.
	LabelTable  string
	LabelJoinOn string
	LabelColumn string
	// Filter is the extra predicate beyond "the hash column is populated".
	Filter string
	// DetachSet is the SQL literal written back to release the reference;
	// the empty string means the row itself is the reference and is DELETEd.
	DetachSet string
}

// specs is the registry. Read the package doc before adding to it.
var specs = []kindSpec{
	{
		Kind: KindWorkCover, Table: "catalog_work_cover",
		HashColumn: "image_hash", EntityColumn: "work_id",
		LabelTable: "catalog_work", LabelJoinOn: "l.id = t.work_id", LabelColumn: "display_name",
		// No live-filter: shadow-never-delete (see the package doc).
		Filter: "", DetachSet: "",
	},
	{
		Kind: KindWorkScreenshot, Table: "catalog_work_screenshot",
		HashColumn: "image_hash", EntityColumn: "work_id",
		LabelTable: "catalog_work", LabelJoinOn: "l.id = t.work_id", LabelColumn: "display_name",
		Filter: "", DetachSet: "",
	},
	{
		// The BUST (256x360 cover crop): VNDB portrait wave + the Getchu bust
		// backfill. Nullable, so NULL is the "no image" value.
		Kind: KindCharacterBust, Table: "catalog_character",
		HashColumn: "image_hash", EntityColumn: "id", LabelColumn: "display_name",
		Filter: "t.deleted_at IS NULL", DetachSet: "NULL",
	},
	{
		// Characters carry TWO independent images: the bust above and this
		// full-body figure (立绘 / tachi-e). Different asset, different render
		// preset, its own column — and therefore its own registry entry.
		Kind: KindCharacterFigure, Table: "catalog_character",
		HashColumn: "figure_hash", EntityColumn: "id", LabelColumn: "display_name",
		Filter: "t.deleted_at IS NULL", DetachSet: "NULL",
	},
	{
		// Label brand logos (wave 170). Stored NOT NULL DEFAULT '', so the
		// empty string — not NULL — is the "no logo" value.
		Kind: KindLabelLogo, Table: "catalog_label",
		HashColumn: "logo_hash", EntityColumn: "id", LabelColumn: "display_name",
		Filter: "t.deleted_at IS NULL", DetachSet: "''",
	},
	{
		// Person photographs (wave 172), NOT NULL DEFAULT '' exactly like the
		// label logo above.
		Kind: KindPersonPhoto, Table: "catalog_person",
		HashColumn: "photo_hash", EntityColumn: "id", LabelColumn: "display_name",
		Filter: "t.deleted_at IS NULL", DetachSet: "''",
	},
}

// Ref is one catalog row that points at an image hash, carrying the owning
// entity so a hash can be reported as the work/character a user would see it on.
type Ref struct {
	Hash     string `gorm:"column:hash"`
	Kind     string `gorm:"column:kind"`
	EntityID int64  `gorm:"column:entity_id"`
	// Label is the owning entity's display name. Empty in the full sweep,
	// which deliberately skips the joins.
	Label string `gorm:"column:label"`
}

// Kinds returns the registered kind identifiers in registry order.
func Kinds() []string {
	out := make([]string, 0, len(specs))
	for _, s := range specs {
		out = append(out, s.Kind)
	}
	return out
}

// hashPresent is the "this row actually holds an image" predicate, applied to
// every kind: nullable columns use NULL and NOT NULL DEFAULT '' columns use the
// empty string, so both halves are always required.
func (s kindSpec) hashPresent() string {
	return fmt.Sprintf("t.%s IS NOT NULL AND t.%s <> ''", s.HashColumn, s.HashColumn)
}

func (s kindSpec) where(extra ...string) string {
	parts := []string{s.hashPresent()}
	if s.Filter != "" {
		parts = append(parts, s.Filter)
	}
	parts = append(parts, extra...)
	return strings.Join(parts, " AND ")
}

// Collect returns every catalog row that references an image, WITHOUT the label
// joins: the daily audit runs this over ~390k rows and only needs hash + owner.
func Collect(ctx context.Context, db *gorm.DB) ([]Ref, error) {
	branches := make([]string, 0, len(specs))
	for _, s := range specs {
		branches = append(branches, fmt.Sprintf(
			"SELECT t.%s AS hash, '%s' AS kind, t.%s AS entity_id, '' AS label FROM %s t WHERE %s",
			s.HashColumn, s.Kind, s.EntityColumn, s.Table, s.where()))
	}
	var refs []Ref
	if err := db.WithContext(ctx).Raw(strings.Join(branches, "\nUNION ALL\n")).Scan(&refs).Error; err != nil {
		return nil, err
	}
	return refs, nil
}

// CollectByHash returns the references to ONE hash, with the owning entity's
// display name — what the admin console shows an operator before they delete
// the bytes. Small by construction, so the label joins are affordable here.
func CollectByHash(ctx context.Context, db *gorm.DB, hash string) ([]Ref, error) {
	branches := make([]string, 0, len(specs))
	args := make([]any, 0, len(specs))
	for _, s := range specs {
		label := fmt.Sprintf("COALESCE(t.%s, '')", s.LabelColumn)
		join := ""
		if s.LabelTable != "" {
			label = fmt.Sprintf("COALESCE(l.%s, '')", s.LabelColumn)
			join = fmt.Sprintf(" LEFT JOIN %s l ON %s", s.LabelTable, s.LabelJoinOn)
		}
		branches = append(branches, fmt.Sprintf(
			"SELECT t.%s AS hash, '%s' AS kind, t.%s AS entity_id, %s AS label FROM %s t%s WHERE %s",
			s.HashColumn, s.Kind, s.EntityColumn, label, s.Table, join,
			s.where(fmt.Sprintf("t.%s = ?", s.HashColumn))))
		args = append(args, hash)
	}
	q := strings.Join(branches, "\nUNION ALL\n") + "\nORDER BY kind, entity_id"
	var refs []Ref
	if err := db.WithContext(ctx).Raw(q, args...).Scan(&refs).Error; err != nil {
		return nil, err
	}
	return refs, nil
}

// DistinctHashes returns the deduped, sorted set of every catalog-scope image
// hash — the universe catalog-image-refping keeps alive.
func DistinctHashes(ctx context.Context, db *gorm.DB) ([]string, error) {
	branches := make([]string, 0, len(specs))
	for _, s := range specs {
		branches = append(branches, fmt.Sprintf(
			"SELECT t.%s AS hash FROM %s t WHERE %s", s.HashColumn, s.Table, s.where()))
	}
	q := "SELECT DISTINCT hash FROM (\n" + strings.Join(branches, "\nUNION ALL\n") + "\n) u ORDER BY hash"
	var hashes []string
	if err := db.WithContext(ctx).Raw(q).Scan(&hashes).Error; err != nil {
		return nil, err
	}
	return hashes, nil
}

// Detach releases every catalog reference to a hash and reports the rows
// affected per kind. One transaction: a half-detached image would leave the
// console's next reference check disagreeing with what the operator just saw.
//
// Detaching does NOT delete the bytes — it is the catalog half of a deletion,
// run before the image service is asked to let go.
func Detach(ctx context.Context, db *gorm.DB, hash string) (map[string]int64, error) {
	removed := make(map[string]int64, len(specs))
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, s := range specs {
			var q string
			// Both statements alias the target as `t` because the shared
			// predicate builder writes qualified column references.
			if s.DetachSet == "" {
				q = fmt.Sprintf("DELETE FROM %s AS t WHERE %s", s.Table, s.where(fmt.Sprintf("t.%s = ?", s.HashColumn)))
			} else {
				q = fmt.Sprintf("UPDATE %s AS t SET %s = %s WHERE %s",
					s.Table, s.HashColumn, s.DetachSet,
					s.where(fmt.Sprintf("t.%s = ?", s.HashColumn)))
			}
			res := tx.Exec(q, hash)
			if res.Error != nil {
				return fmt.Errorf("detach %s: %w", s.Kind, res.Error)
			}
			removed[s.Kind] = res.RowsAffected
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return removed, nil
}
