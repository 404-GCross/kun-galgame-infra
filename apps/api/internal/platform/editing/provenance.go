package editing

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"api/internal/platform/provenance"

	"gorm.io/gorm"
)

// ProvenanceTarget is the `field_provenance` key a field's value lands in. It
// names the column rather than reusing the field key because the two differ
// (`catalog.label.name` writes `catalog_label.display_name`), and consumers look
// the value up by column.
type ProvenanceTarget struct {
	Table  string
	Column string
	// Rows, when set, makes the stamp land on child rows of the entity instead
	// of the entity's own head row: it resolves the submitted value to those
	// rows' own ids and the stamp is issued as WHERE id IN (…). Nil keeps
	// WHERE id = entityID.
	//
	// It is called BEFORE Apply, and that order carries the whole feature — see
	// ApplyField.
	Rows func(ctx context.Context, tx *gorm.DB, entityID int64, value any) ([]int64, error)
}

// ApplyField writes one field and records the human stamp its spec declares.
// Every write path goes through it — the merge transaction and a family's birth
// path both do. Calling FieldSpec.Apply directly stores the value with no
// stamp, which stays invisible until an importer overwrites the edit.
//
// The Rows resolvers run before Apply because they answer "which rows does this
// submission actually change", which is a question only the pre-write state can
// answer. Run after Apply, every row already holds the submitted value, so a
// resolver returns the empty set, not one stamp is written, and nothing
// anywhere reports an error.
func ApplyField(ctx context.Context, tx *gorm.DB, f *FieldSpec, entityID int64, value any) error {
	rows := make([][]int64, len(f.Provenance))
	for i := range f.Provenance {
		if f.Provenance[i].Rows == nil {
			continue
		}
		ids, err := f.Provenance[i].Rows(ctx, tx, entityID, value)
		if err != nil {
			return fmt.Errorf("editing: resolve provenance rows for %s: %w", f.Key, err)
		}
		rows[i] = ids
	}
	if err := f.Apply(ctx, tx, entityID, value); err != nil {
		return err
	}
	for i := range f.Provenance {
		if err := stampProvenance(ctx, tx, f.Provenance[i], entityID, rows[i]); err != nil {
			return err
		}
	}
	return nil
}

func stampProvenance(ctx context.Context, tx *gorm.DB, target ProvenanceTarget, entityID int64, rowIDs []int64) error {
	entry, err := json.Marshal([]provenance.Entry{{
		Source: provenance.SourceCurated,
		At:     time.Now().UTC().Format(time.RFC3339),
	}})
	if err != nil {
		return err
	}
	where, key := "id = ?", any(entityID)
	if target.Rows != nil {
		if len(rowIDs) == 0 {
			return nil
		}
		where, key = "id IN ?", any(rowIDs)
	}
	res := tx.WithContext(ctx).Exec(
		`UPDATE `+target.Table+` SET field_provenance = jsonb_set(field_provenance, ARRAY[?::text],
			?::jsonb || COALESCE(field_provenance -> ?, '[]'::jsonb))
		 WHERE `+where,
		target.Column, string(entry), target.Column, key)
	if res.Error != nil {
		return fmt.Errorf("editing: stamp provenance on %s.%s: %w", target.Table, target.Column, res.Error)
	}
	if target.Rows == nil && res.RowsAffected == 0 {
		return ErrEntityNotFound
	}
	return nil
}
