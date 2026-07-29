package repository

import (
	"context"

	"gorm.io/gorm"
)

// touchChunk bounds one UPDATE's bind parameters. catalog_work ids are the only
// arguments, so this is far under Postgres' 65,535 limit while still keeping the
// statement text small enough to plan cheaply.
const touchChunk = 2000

// TouchWorks bumps catalog_work.updated_at for the given works.
//
// The public changes feed (GET /v1/catalog/changes) walks catalog_work on an
// (updated_at, id) keyset, and CatalogWork.UpdatedAt only moves when something
// writes the work row itself. Facet importers write side tables — tags, intros,
// covers, credits, relations — so without this the feed stays blind to them and
// downstream mirrors never learn that a work is worth re-pulling.
//
// Callers must pass ONLY works they actually mutated. A rerun that writes
// nothing must touch nothing, otherwise every idempotent job would shove its
// whole candidate set through the feed on every pass. Duplicates and an empty
// set are handled here; the empty case does not go to the database at all.
func TouchWorks(ctx context.Context, db *gorm.DB, workIDs []int64) error {
	if len(workIDs) == 0 {
		return nil
	}
	seen := make(map[int64]struct{}, len(workIDs))
	ids := make([]int64, 0, len(workIDs))
	for _, id := range workIDs {
		if id == 0 {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	for start := 0; start < len(ids); start += touchChunk {
		end := min(start+touchChunk, len(ids))
		if err := db.WithContext(ctx).
			Exec(`UPDATE catalog_work SET updated_at = now() WHERE id IN (?)`, ids[start:end]).
			Error; err != nil {
			return err
		}
	}
	return nil
}
