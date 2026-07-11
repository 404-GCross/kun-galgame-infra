package main

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	"api/internal/platform/galgame/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Join constants — the ONLY legal path from an EG score to a wiki galgame.
// Pinned seed/enum/tenant values, mirrored from
// internal/platform/galgame/catalogsync/reconcile.go and
// internal/platform/catalog/model/constants.go. Getting the tenant key wrong
// fails silently (doc 17 §0), so they live here explicitly with references.
const (
	egSourceID     int16  = 5              // catalog_source seed {id:5, key:"n"} = erogamespace
	entityTypeWork int16  = 5              // catalog/model.EntityTypeWork
	linkKindExact  int16  = 0              // catalog/model.LinkKindExact — identity assertion only
	wikiSite       string = "galgame_wiki" // catalog_work.site tenant key (NOT the medium key "galgame")
)

// Options controls an enrich-eg-scores run.
type Options struct {
	Apply bool // false = dry run (resolve + count, no writes)
	Limit int  // cap works processed (0 = all); for a quick sampled trial
}

// Stats is the run summary. The identity: anchors - multiAnchor = matched +
// missingInMirror = written + skippedNoGalgame + missingInMirror.
type Stats struct {
	Anchors          int `json:"anchors"`            // EG exact work anchors resolving to a claimed galgame_wiki work (raw join rows)
	MultiAnchor      int `json:"multi_anchor"`       // extra anchors collapsed because a work carried >1 EG exact anchor
	Matched          int `json:"matched"`            // distinct target galgames whose EG game exists in the EG mirror
	Written          int `json:"written"`            // rows upserted into galgame_eg_meta
	MissingInMirror  int `json:"missing_in_mirror"`  // EG game id (post-collapse) absent from the EG mirror
	SkippedNoGalgame int `json:"skipped_no_galgame"` // target galgame absent from the local wiki (snapshot drift / FK guard)
}

// egData is one EG mirror games row (median is nullable, count2 → vote count).
type egData struct {
	median *int
	votes  int
}

// Run enriches galgame_eg_meta from the EG mirror via the catalog exact work
// anchor. catalogDB reads catalog_external_ref + catalog_work; egDB reads the
// EG mirror `games`; wikiDB is the write side (existence guard + upsert). In
// production these are three distinct databases; the integration test points
// all three at one schema-isolated fixture DB.
func Run(wikiDB, catalogDB, egDB *gorm.DB, opts Options) (*Stats, error) {
	stats := &Stats{}

	// 1. Anchors: EG exact work refs whose work is a claimed galgame_wiki row.
	// external_id is the EG game id; product_work_id is the wiki galgame.id.
	type anchorRow struct {
		ExternalID string `gorm:"column:external_id"`
		GalgameID  int64  `gorm:"column:galgame_id"`
	}
	q := catalogDB.Table("catalog_external_ref AS er").
		Select("er.external_id AS external_id, w.product_work_id AS galgame_id").
		Joins("JOIN catalog_work AS w ON w.id = er.entity_id").
		Where("er.source_id = ? AND er.entity_type = ? AND er.link_kind = ?", egSourceID, entityTypeWork, linkKindExact).
		Where("w.site = ? AND w.product_work_id IS NOT NULL AND w.deleted_at IS NULL", wikiSite).
		Order("w.product_work_id")
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit)
	}
	var anchors []anchorRow
	if err := q.Scan(&anchors).Error; err != nil {
		return nil, fmt.Errorf("load EG exact work anchors: %w", err)
	}
	stats.Anchors = len(anchors)

	// Group EG game ids by target galgame. A non-numeric external_id can never
	// match the EG mirror (integer PK) — bucket it under a sentinel -1 so it is
	// counted as missing_in_mirror below rather than silently dropped.
	byGalgame := map[int64][]int64{}
	for _, a := range anchors {
		egID, err := strconv.ParseInt(a.ExternalID, 10, 64)
		if err != nil {
			egID = -1
		}
		byGalgame[a.GalgameID] = append(byGalgame[a.GalgameID], egID)
	}

	// 2. EG mirror rows for every referenced EG game id.
	egIDset := map[int64]bool{}
	for _, ids := range byGalgame {
		for _, id := range ids {
			if id >= 0 {
				egIDset[id] = true
			}
		}
	}
	egMap, err := loadEG(egDB, keysOf(egIDset))
	if err != nil {
		return nil, fmt.Errorf("load EG mirror games: %w", err)
	}

	// 3. Which target galgames actually exist in the local wiki (EXISTS guard:
	// the FK protection + resilience to any snapshot id-frontier drift).
	galgameIDs := make([]int64, 0, len(byGalgame))
	for gid := range byGalgame {
		galgameIDs = append(galgameIDs, gid)
	}
	existing, err := loadExistingGalgames(wikiDB, galgameIDs)
	if err != nil {
		return nil, fmt.Errorf("load existing galgames: %w", err)
	}

	// 4. One decision per distinct target galgame (deterministic order).
	sort.Slice(galgameIDs, func(i, j int) bool { return galgameIDs[i] < galgameIDs[j] })
	now := time.Now()
	for _, gid := range galgameIDs {
		egIDs := byGalgame[gid]
		chosen := pickBest(egIDs, egMap)
		if len(egIDs) > 1 {
			stats.MultiAnchor += len(egIDs) - 1
		}
		eg, ok := egMap[chosen]
		if !ok {
			stats.MissingInMirror++
			continue
		}
		stats.Matched++
		if !existing[gid] {
			stats.SkippedNoGalgame++
			continue
		}
		if opts.Apply {
			if err := upsertEGMeta(wikiDB, model.GalgameEGMeta{
				GalgameID: int(gid),
				EGGameID:  int(chosen),
				Median:    eg.median,
				VoteCount: eg.votes,
				SyncedAt:  now,
			}); err != nil {
				return nil, fmt.Errorf("upsert eg meta galgame %d: %w", gid, err)
			}
		}
		stats.Written++
	}
	return stats, nil
}

// pickBest chooses the representative EG game for a work with several exact
// anchors (theoretically none — the exact unique is on (source, external_id,
// entity_type), but one work MAY map to several EG ids): most-voted wins
// (biggest count2 = most reliable median), tie-break higher median, then id.
// Ids absent from the mirror sort last so a present anchor always wins.
func pickBest(egIDs []int64, egMap map[int64]egData) int64 {
	best := egIDs[0]
	for _, id := range egIDs[1:] {
		if less(best, id, egMap) {
			best = id
		}
	}
	return best
}

// less reports whether a is a WORSE representative than b.
func less(a, b int64, egMap map[int64]egData) bool {
	da, oka := egMap[a]
	db, okb := egMap[b]
	if oka != okb {
		return !oka // present beats absent
	}
	if !oka {
		return a < b
	}
	if da.votes != db.votes {
		return da.votes < db.votes
	}
	ma, mb := deref(da.median), deref(db.median)
	if ma != mb {
		return ma < mb
	}
	return a < b
}

func deref(p *int) int {
	if p == nil {
		return -1
	}
	return *p
}

// loadEG batch-loads EG mirror games (median nullable, count2 → 0 when NULL).
func loadEG(egDB *gorm.DB, ids []int64) (map[int64]egData, error) {
	out := map[int64]egData{}
	type row struct {
		ID     int64 `gorm:"column:id"`
		Median *int  `gorm:"column:median"`
		Count2 *int  `gorm:"column:count2"`
	}
	for start := 0; start < len(ids); start += 1000 {
		end := min(start+1000, len(ids))
		var batch []row
		if err := egDB.Table("games").Select("id, median, count2").
			Where("id IN ?", ids[start:end]).Scan(&batch).Error; err != nil {
			return nil, err
		}
		for _, r := range batch {
			votes := 0
			if r.Count2 != nil {
				votes = *r.Count2
			}
			out[r.ID] = egData{median: r.Median, votes: votes}
		}
	}
	return out, nil
}

// loadExistingGalgames batch-loads which of the target galgame ids exist.
func loadExistingGalgames(wikiDB *gorm.DB, ids []int64) (map[int64]bool, error) {
	out := map[int64]bool{}
	for start := 0; start < len(ids); start += 1000 {
		end := min(start+1000, len(ids))
		var found []int64
		if err := wikiDB.Table("galgame").Where("id IN ?", ids[start:end]).
			Pluck("id", &found).Error; err != nil {
			return nil, err
		}
		for _, id := range found {
			out[id] = true
		}
	}
	return out, nil
}

func upsertEGMeta(db *gorm.DB, row model.GalgameEGMeta) error {
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "galgame_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"eg_game_id", "median", "vote_count", "synced_at"}),
	}).Create(&row).Error
}

func keysOf(m map[int64]bool) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
