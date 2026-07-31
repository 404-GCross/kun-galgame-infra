package editing

import (
	"context"
	"encoding/json"

	"gorm.io/gorm"
)

// override.go — the CURATED-OVERRIDE query (03 定案 §0 line 2, second half).
//
// Multi-valued facets separate humans from importers by lane: a human writes
// curated-source rows, an importer never touches them. A SCALAR column has no
// lane to write in — there is one olang, one content_rating — so the separation
// has to be expressed the other way round: an importer asks whether a human has
// ever edited this field on this entity, and if so leaves it alone.
//
// The ledger is edit_revision, which already records exactly that (entity +
// changed_fields per merge) and needs NO new column, NO new table and no flag
// anyone could forget to set: every write through the engine — direct edit,
// reviewer merge, revert — appends one.
//
// Semantics chosen deliberately:
//
//   - ANY revision that changed the field counts, including one that later got
//     reverted. The reverting revision changed the field too, so the entity is
//     in the set either way — which is right: a human deciding the upstream
//     value was correct after all is still a human decision the importer must
//     not silently re-litigate.
//   - the check is BATCH by construction. A per-row EXISTS in an importer loop
//     is how a 200k-row job becomes a 200k-query job.

// overrideChunk bounds one IN list.
const overrideChunk = 2000

// EditedEntities returns the subset of entityIDs whose fieldKey a human has
// edited through the engine — the ids an importer or sync lane must not
// overwrite. An empty input returns an empty set without touching the database.
//
// db is the pool holding edit_revision (the catalog pool). entityType is the
// registered type ("catalog.work"); fieldKey is one of its eternal field keys.
func EditedEntities(ctx context.Context, db *gorm.DB, entityType, fieldKey string, entityIDs []int64) (map[int64]bool, error) {
	out := make(map[int64]bool)
	if len(entityIDs) == 0 {
		return out, nil
	}
	// changed_fields is a JSON array of field keys, so containment is the exact
	// question ("does this revision's list include the key") and it rides the
	// jsonb operator rather than a LIKE over serialized text.
	needle, err := json.Marshal([]string{fieldKey})
	if err != nil {
		return nil, err
	}
	for start := 0; start < len(entityIDs); start += overrideChunk {
		end := min(start+overrideChunk, len(entityIDs))
		var ids []int64
		if err := db.WithContext(ctx).Raw(`
			SELECT DISTINCT entity_id FROM edit_revision
			WHERE entity_type = ? AND entity_id IN (?) AND changed_fields @> ?::jsonb`,
			entityType, entityIDs[start:end], string(needle)).Scan(&ids).Error; err != nil {
			return nil, err
		}
		for _, id := range ids {
			out[id] = true
		}
	}
	return out, nil
}
