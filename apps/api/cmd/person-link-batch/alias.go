package main

import (
	"fmt"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

func orderedPair(a, b int64) [2]int64 {
	if a > b {
		return [2]int64{b, a}
	}
	return [2]int64{a, b}
}

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
