package orglabels

import "gorm.io/gorm"

// loadLabelWorks returns work_id → the labels attributed to it (distinct
// catalog_work_label edges). This is W(L) inverted, the index the co-occurrence
// tally walks: for each work in W(O) it accumulates a share for every label on
// that work.
func loadLabelWorks(db *gorm.DB) (map[int64][]int64, error) {
	var rows []struct {
		WorkID  int64 `gorm:"column:work_id"`
		LabelID int64 `gorm:"column:label_id"`
	}
	if err := db.Raw(
		`SELECT DISTINCT work_id, label_id FROM catalog_work_label`,
	).Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[int64][]int64, len(rows))
	for _, r := range rows {
		m[r.WorkID] = append(m[r.WorkID], r.LabelID)
	}
	return m, nil
}

// loadLabelNorms returns the NFKC-folded name → label ids index used for the
// name-equality branch of the grader. It unions the display_name fold with
// every label alias fold, so a source name matching either resolves the label.
// Both sides use Postgres lower(normalize(x,NFKC)) (the *_norm generated
// columns) so equality is byte-identical (bgmtype4gated precedent).
func loadLabelNorms(db *gorm.DB) (map[string][]int64, error) {
	var rows []struct {
		Norm    string `gorm:"column:norm"`
		LabelID int64  `gorm:"column:label_id"`
	}
	if err := db.Raw(`
		SELECT display_name_norm AS norm, id AS label_id FROM catalog_label
		    WHERE display_name_norm <> ''
		UNION
		SELECT name_norm AS norm, label_id FROM catalog_label_alias
		    WHERE name_norm <> ''
	`).Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[string][]int64, len(rows))
	for _, r := range rows {
		m[r.Norm] = append(m[r.Norm], r.LabelID)
	}
	return m, nil
}

// existingAnchors holds the idempotency index for one source: which external
// ids already carry a label anchor, and which labels are already identity-
// claimed (exact OR probable) by this source. A label claimed by either tier
// must not be re-anchored by a second org (裁定4 "同源一 label 双锚非法") — this
// is what makes a second --apply run write zero.
type existingAnchors struct {
	byExtID        map[string]int16 // external_id → link_kind (present = already anchored)
	claimedByLabel map[int64]bool   // label_id → an identity anchor (exact/probable) from this source exists
}

// loadExistingAnchors preloads the source's entity_type=label refs. Only the
// identity tiers (exact=0 / probable=1) seed the claim index; related=2 links
// (E2b) never claim identity.
func loadExistingAnchors(db *gorm.DB, source int16) (*existingAnchors, error) {
	var rows []struct {
		ExternalID string `gorm:"column:external_id"`
		EntityID   int64  `gorm:"column:entity_id"`
		LinkKind   int16  `gorm:"column:link_kind"`
	}
	if err := db.Raw(`
		SELECT external_id, entity_id, link_kind FROM catalog_external_ref
		    WHERE entity_type = 3 AND source_id = ? AND link_kind IN (0, 1)`, source,
	).Scan(&rows).Error; err != nil {
		return nil, err
	}
	ea := &existingAnchors{
		byExtID:        make(map[string]int16, len(rows)),
		claimedByLabel: make(map[int64]bool),
	}
	for _, r := range rows {
		ea.byExtID[r.ExternalID] = r.LinkKind
		ea.claimedByLabel[r.EntityID] = true
	}
	return ea, nil
}
