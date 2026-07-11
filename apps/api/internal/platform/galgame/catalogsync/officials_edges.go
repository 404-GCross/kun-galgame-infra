package catalogsync

import (
	"api/internal/platform/catalog/model"

	"gorm.io/gorm/clause"
)

// The name-collision candidate phase and the Brand attribution-edge phase of
// the official→label wave (the mint phase lives in officials.go — mirroring the
// package's claim/eg/bangumi phase-per-file split).

// writeCandidates surfaces name-norm collisions between each minted official
// (its display name + original + aliases) and pre-wave labels as pending
// match_candidates — candidate only, never an auto-merge (R8: name equality is
// candidate-grade; the merge machine converges true duplicates). Dry-run counts
// the would-be candidates (labelID nil).
func (r *OfficialRegistrar) writeCandidates(toMint []officialRow, aliasIdx map[int64][]officialAliasRow, labelID map[int64]int64, normIndex map[string][]int64, st *OfficialStats) error {
	var cands []model.CatalogMatchCandidate
	for _, o := range toMint {
		var newID int64
		if labelID != nil {
			newID = labelID[o.ID]
		}
		matched := map[int64]bool{}
		for _, n := range lhsNormsFor(o, aliasIdx[o.ID]) {
			for _, existing := range normIndex[n] {
				matched[existing] = true
			}
		}
		for existing := range matched {
			// A fresh mint always outranks every pre-wave id, so a<b holds with
			// a=existing; the swap is defensive.
			a, b := newID, existing
			if a > b {
				a, b = b, a
			}
			cands = append(cands, model.CatalogMatchCandidate{
				EntityType: model.EntityTypeLabel, AID: a, BID: b,
				Reason: model.CandidateReasonNameNormEqual, Status: model.CandidateStatusPending,
			})
		}
	}
	if r.dryRun {
		st.NameCandidates = len(cands)
		return nil
	}
	written := 0
	for start := 0; start < len(cands); start += officialChunk {
		end := min(start+officialChunk, len(cands))
		chunk := cands[start:end]
		res := r.catalog.Clauses(clause.OnConflict{DoNothing: true}).Create(&chunk)
		if res.Error != nil {
			return res.Error
		}
		written += int(res.RowsAffected)
	}
	st.NameCandidates = written
	return nil
}

// writeEdges upserts one Brand attribution edge per official→galgame relation
// whose galgame is a claimed catalog_work. Idempotent (composite PK ON CONFLICT
// DO NOTHING) and backfilling: emitting for ALL mapped officials every run picks
// up works claimed after an official was first minted. source_id is NULL — the
// wiki relations are first-party user-curated data, not an import assertion.
func (r *OfficialRegistrar) writeEdges(claimed map[int64]int64, st *OfficialStats) error {
	var rels []struct {
		OfficialID int64 `gorm:"column:official_id"`
		GalgameID  int64 `gorm:"column:galgame_id"`
	}
	if err := r.wiki.Raw(`SELECT official_id, galgame_id FROM galgame_official_relation`).Scan(&rels).Error; err != nil {
		return err
	}
	if r.dryRun {
		// Every official will map on apply, so count by galgame claim state only.
		for _, rel := range rels {
			if _, ok := claimed[rel.GalgameID]; ok {
				st.EdgesWritten++
			} else {
				st.EdgesSkippedUnclaimed++
			}
		}
		return nil
	}
	labelByOfficial, err := r.loadOfficialLabelMap()
	if err != nil {
		return err
	}
	var edges []model.CatalogWorkLabel
	for _, rel := range rels {
		lid, ok := labelByOfficial[rel.OfficialID]
		if !ok {
			continue // official not yet mapped (only under --limit)
		}
		wid, ok := claimed[rel.GalgameID]
		if !ok {
			st.EdgesSkippedUnclaimed++
			continue
		}
		edges = append(edges, model.CatalogWorkLabel{
			WorkID: wid, LabelID: lid, Kind: model.WorkLabelKindBrand, SourceID: nil,
		})
	}
	written := 0
	for start := 0; start < len(edges); start += officialChunk {
		end := min(start+officialChunk, len(edges))
		chunk := edges[start:end]
		res := r.catalog.Clauses(clause.OnConflict{DoNothing: true}).Create(&chunk)
		if res.Error != nil {
			return res.Error
		}
		written += int(res.RowsAffected)
	}
	st.EdgesWritten = written
	st.EdgesAlready = len(edges) - written
	return nil
}

// loadOfficialLabelMap returns official id → catalog label id for every mapped
// official (the edge-phase resolver).
func (r *OfficialRegistrar) loadOfficialLabelMap() (map[int64]int64, error) {
	var rows []struct {
		ID      int64 `gorm:"column:id"`
		LabelID int64 `gorm:"column:catalog_label_id"`
	}
	if err := r.wiki.Raw(`SELECT id, catalog_label_id FROM galgame_official WHERE catalog_label_id IS NOT NULL`).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[int64]int64, len(rows))
	for _, row := range rows {
		m[row.ID] = row.LabelID
	}
	return m, nil
}
