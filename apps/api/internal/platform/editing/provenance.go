package editing

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"api/internal/platform/provenance"

	"gorm.io/gorm"
)

// ProvenanceTarget is the head-table `field_provenance` key a field's value
// lands in. It names the column rather than reusing the field key because the
// two differ (`catalog.label.name` writes `catalog_label.display_name`), and
// consumers look the value up by column.
type ProvenanceTarget struct {
	Table  string
	Column string
}

// ApplyField writes one field and records the human stamp its spec declares.
// Every write path goes through it — the merge transaction and a family's birth
// path both do. Calling FieldSpec.Apply directly stores the value with no
// stamp, which stays invisible until an importer overwrites the edit.
func ApplyField(ctx context.Context, tx *gorm.DB, f *FieldSpec, entityID int64, value any) error {
	if err := f.Apply(ctx, tx, entityID, value); err != nil {
		return err
	}
	if f.Provenance == nil {
		return nil
	}
	return stampProvenance(ctx, tx, *f.Provenance, entityID)
}

func stampProvenance(ctx context.Context, tx *gorm.DB, target ProvenanceTarget, entityID int64) error {
	entry, err := json.Marshal([]provenance.Entry{{
		Source: provenance.SourceCurated,
		At:     time.Now().UTC().Format(time.RFC3339),
	}})
	if err != nil {
		return err
	}
	res := tx.WithContext(ctx).Exec(
		`UPDATE `+target.Table+` SET field_provenance = jsonb_set(field_provenance, ARRAY[?::text],
			?::jsonb || COALESCE(field_provenance -> ?, '[]'::jsonb))
		 WHERE id = ?`,
		target.Column, string(entry), target.Column, entityID)
	if res.Error != nil {
		return fmt.Errorf("editing: stamp provenance on %s.%s: %w", target.Table, target.Column, res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrEntityNotFound
	}
	return nil
}
