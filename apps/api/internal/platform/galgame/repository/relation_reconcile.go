package repository

import "gorm.io/gorm"

// reconcileSet brings the child rows linked to one parent (fkCol = fkVal) into
// exact agreement with `desired`, writing ONLY the delta: rows whose key is no
// longer desired are deleted, rows for newly-desired keys are batch-inserted,
// and unchanged rows (with their row identity, created timestamp and any
// columns not tracked by the snapshot) are left untouched.
//
// It replaces the historical "DELETE all + INSERT one-per-row in a loop"
// rebuild used for set-valued relations. The END STATE is identical to a full
// rebuild because galgame relations are order-independent SETS — TakeSnapshot
// canonical-sorts them (revision design §1.5 #6), so physical row order and row
// identity never reach a snapshot. So this stays a COMPLETE, authoritative
// write of the whole set (not a partial-field bypass — §1.5 #2): once it
// returns, the child set equals `desired`. The only thing that changes versus a
// full rebuild is write volume — editing an unrelated field no longer churns
// every relation row (no dead tuples, no WAL amplification, no N round-trips),
// which is the whole point for bulk tag/official/engine edits.
//
// Only use this for pure set membership (the row IS the membership, e.g. the
// *_id join tables and the alias name tables). Relations whose order matters or
// that carry mutable per-row payload not keyed by `keyCol` (links, covers,
// screenshots) must keep the clear-and-rebuild path.
//
// fkCol / keyCol are fixed internal column names, never user input. Key is the
// natural-key column type: int for the *_id join tables, string for alias names.
func reconcileSet[Row any, Key comparable](
	tx *gorm.DB,
	fkCol string, fkVal int,
	keyCol string, desired []Key,
	newRow func(Key) Row,
) error {
	var existing []Key
	if err := tx.Model(new(Row)).
		Where(fkCol+" = ?", fkVal).
		Pluck(keyCol, &existing).Error; err != nil {
		return err
	}

	desiredSet := make(map[Key]struct{}, len(desired))
	for _, k := range desired {
		desiredSet[k] = struct{}{}
	}
	existingSet := make(map[Key]struct{}, len(existing))
	var toRemove []Key
	for _, k := range existing {
		existingSet[k] = struct{}{}
		if _, keep := desiredSet[k]; !keep {
			toRemove = append(toRemove, k)
		}
	}
	var toAdd []Row
	for k := range desiredSet {
		if _, have := existingSet[k]; !have {
			toAdd = append(toAdd, newRow(k))
		}
	}

	if len(toRemove) > 0 {
		if err := tx.Where(fkCol+" = ? AND "+keyCol+" IN ?", fkVal, toRemove).
			Delete(new(Row)).Error; err != nil {
			return err
		}
	}
	if len(toAdd) > 0 {
		if err := tx.Create(&toAdd).Error; err != nil {
			return err
		}
	}
	return nil
}
