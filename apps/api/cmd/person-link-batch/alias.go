package main

import (
	"fmt"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// orderedPair normalizes a candidate pair to a<b (candidates are stored that
// way; the helper keeps set lookups robust).
func orderedPair(a, b int64) [2]int64 {
	if a > b {
		return [2]int64{b, a}
	}
	return [2]int64{a, b}
}

// aliasClassifier judges the alias_declared candidates with a SECOND line of
// evidence beyond the declaration:
//   - A3: the two names are co-credited on the same work (loadCoCreditPairs);
//   - A4: the declaration is bidirectional — each side's ingested search-hint
//     aliases (step 25) name the other side's whole folded name.
//
// Everything is precomputed in two bulk queries so the per-candidate closure is
// allocation-free.
func aliasClassifier(db *gorm.DB, rows []candidateRow) (func(candidateRow) string, error) {
	coCredit, err := loadCoCreditPairs(db)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(rows)*2)
	for _, r := range rows {
		ids = append(ids, r.AID, r.BID)
	}
	aliasFolds, err := loadAliasFolds(db, ids)
	if err != nil {
		return nil, err
	}
	return func(r candidateRow) string {
		if coCredit[orderedPair(r.AID, r.BID)] {
			return ruleA3
		}
		if aliasFolds[r.AID][foldName(r.BName)] && aliasFolds[r.BID][foldName(r.AName)] {
			return ruleA4
		}
		return ""
	}, nil
}

// loadCoCreditPairs returns the alias_declared candidate pairs whose two names
// share a credit on the same work — the "collaborated on a title" evidence.
func loadCoCreditPairs(db *gorm.DB) (map[[2]int64]bool, error) {
	var rows []struct {
		AID int64 `gorm:"column:a_id"`
		BID int64 `gorm:"column:b_id"`
	}
	if err := db.Raw(`
		SELECT c.a_id, c.b_id
		FROM catalog_match_candidate c
		WHERE c.entity_type = ? AND c.reason = ? AND c.status = ?
		  AND EXISTS (
		    SELECT 1 FROM catalog_credit x
		    JOIN catalog_credit y ON y.work_id = x.work_id
		    WHERE x.credit_name_id = c.a_id AND y.credit_name_id = c.b_id)`,
		model.EntityTypeCreditName, model.CandidateReasonAliasDeclared, model.CandidateStatusPending,
	).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load co-credit pairs: %w", err)
	}
	set := make(map[[2]int64]bool, len(rows))
	for _, r := range rows {
		set[orderedPair(r.AID, r.BID)] = true
	}
	return set, nil
}

// loadAliasFolds maps each credit_name id to the folded forms of its ingested
// aliases (catalog_name_alias, step 25's search hints).
func loadAliasFolds(db *gorm.DB, ids []int64) (map[int64]map[string]bool, error) {
	out := map[int64]map[string]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []struct {
		Owner int64  `gorm:"column:credit_name_id"`
		Name  string `gorm:"column:name"`
	}
	if err := db.Raw(`SELECT credit_name_id, normalize(name, NFKC) AS name
		FROM catalog_name_alias WHERE credit_name_id IN ?`, ids).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load alias folds: %w", err)
	}
	for _, r := range rows {
		f := foldName(r.Name)
		if f == "" {
			continue
		}
		if out[r.Owner] == nil {
			out[r.Owner] = map[string]bool{}
		}
		out[r.Owner][f] = true
	}
	return out, nil
}
