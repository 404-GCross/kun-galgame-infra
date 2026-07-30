package catalogsync

import (
	"log/slog"

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
// The label id always goes through loadOfficialLabelMap's redirect resolution,
// so an edge is only ever written to a canonical, live label.
func (r *OfficialRegistrar) writeEdges(claimed map[int64]int64, st *OfficialStats) error {
	var rels []struct {
		OfficialID int64 `gorm:"column:official_id"`
		GalgameID  int64 `gorm:"column:galgame_id"`
	}
	if err := r.wiki.Raw(`SELECT official_id, galgame_id FROM galgame_official_relation`).Scan(&rels).Error; err != nil {
		return err
	}
	if r.dryRun {
		// Nothing is minted yet, so there is no id to resolve: count by galgame
		// claim state only. The apply run may still drop a few of these on a
		// dead-label bridge (EdgesSkippedDeadLabel) — the dry plan is an upper
		// bound on the writes, as it always has been for unmapped officials.
		for _, rel := range rels {
			if _, ok := claimed[rel.GalgameID]; ok {
				st.EdgesWritten++
			} else {
				st.EdgesSkippedUnclaimed++
			}
		}
		return nil
	}
	labelByOfficial, droppedOfficials, err := r.loadOfficialLabelMap()
	if err != nil {
		return err
	}
	var edges []model.CatalogWorkLabel
	for _, rel := range rels {
		lid, ok := labelByOfficial[rel.OfficialID]
		if !ok {
			if droppedOfficials[rel.OfficialID] {
				st.EdgesSkippedDeadLabel++
			}
			continue // official not yet mapped (only under --limit) or dropped
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

// loadOfficialLabelMap returns official id → CANONICAL catalog label id for
// every mapped official, plus the set of officials whose bridge id no longer
// resolves to a live label (the edge-phase resolver).
//
// galgame_official.catalog_label_id is a plain bridge column that the label
// merge path does NOT repoint: when a label is merged away the official keeps
// pointing at the now soft-deleted source id. Projecting that id verbatim is
// how a rerun wrote thousands of brand edges to dead labels, which the work
// page then rendered beside the surviving twin (duplicate same-name company).
// So every id is resolved through catalog_redirect before it becomes an edge,
// and an id that still lands on a soft-deleted label with no redirect is
// dropped rather than written.
func (r *OfficialRegistrar) loadOfficialLabelMap() (map[int64]int64, map[int64]bool, error) {
	var rows []struct {
		ID      int64 `gorm:"column:id"`
		LabelID int64 `gorm:"column:catalog_label_id"`
	}
	if err := r.wiki.Raw(`SELECT id, catalog_label_id FROM galgame_official WHERE catalog_label_id IS NOT NULL`).
		Scan(&rows).Error; err != nil {
		return nil, nil, err
	}
	res, err := r.loadLabelResolver()
	if err != nil {
		return nil, nil, err
	}
	m := make(map[int64]int64, len(rows))
	dropped := map[int64]bool{}
	redirected := 0
	for _, row := range rows {
		canonical, live := res.resolve(row.LabelID)
		if !live {
			dropped[row.ID] = true
			continue
		}
		if canonical != row.LabelID {
			redirected++
		}
		m[row.ID] = canonical
	}
	if redirected > 0 || len(dropped) > 0 {
		slog.Warn("official→label bridge is stale",
			"redirected", redirected, "dropped_dead_label", len(dropped),
			"hint", "run cmd/heal-label-redirects to repoint galgame_official.catalog_label_id")
	}
	return m, dropped, nil
}

// labelResolver maps a label id onto its canonical live id: the redirect chain
// left by successive merges, plus the soft-deleted set. Both tables are small
// (redirects are one row per merge, deleted labels a small tail), so each is
// loaded whole once per run instead of queried per id.
type labelResolver struct {
	redirects map[int64]int64
	deleted   map[int64]bool
}

func (r *OfficialRegistrar) loadLabelResolver() (*labelResolver, error) {
	res := &labelResolver{redirects: map[int64]int64{}, deleted: map[int64]bool{}}
	var reds []struct {
		OldID     int64 `gorm:"column:old_id"`
		CurrentID int64 `gorm:"column:current_id"`
	}
	if err := r.catalog.Raw(`SELECT old_id, current_id FROM catalog_redirect WHERE entity_type = ?`,
		model.EntityTypeLabel).Scan(&reds).Error; err != nil {
		return nil, err
	}
	for _, red := range reds {
		res.redirects[red.OldID] = red.CurrentID
	}
	var deleted []int64
	if err := r.catalog.Raw(`SELECT id FROM catalog_label WHERE deleted_at IS NOT NULL`).
		Scan(&deleted).Error; err != nil {
		return nil, err
	}
	for _, id := range deleted {
		res.deleted[id] = true
	}
	return res, nil
}

// resolve follows the redirect chain to its fixpoint and reports whether the
// destination is a live label. Merges are supposed to flatten chains to length
// one (doc 10 invariant 3), but successive merges of the same id space have
// produced chains before, so the walk iterates — with a seen set so a cycle
// (which would be corrupt data) terminates instead of hanging.
func (res *labelResolver) resolve(id int64) (int64, bool) {
	seen := map[int64]bool{id: true}
	for {
		next, ok := res.redirects[id]
		if !ok || seen[next] {
			break
		}
		seen[next] = true
		id = next
	}
	if res.deleted[id] {
		return 0, false
	}
	return id, true
}
