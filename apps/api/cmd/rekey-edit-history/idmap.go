package main

import (
	"context"
	"fmt"
	"strconv"

	catmodel "api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// idMaps are the four wiki→catalog id spaces this migration needs. Every one
// of them already exists in catalog_external_ref, put there by the wikirescue
// address-map steps (i/j/k/l) precisely so the wiki id spaces survive their
// tables: gid→work (`wiki:gid`), oid→label (`wiki:oid`), tid→tag (`wiki:tid`),
// eid→engine (`wiki:eid`). This tool READS them and invents none.
//
// The rows are selected by MATCHED_BY, not by the source key, and that is a
// correctness requirement rather than a style choice. The literal
// "galgame_wiki" names two unrelated things — the claim site on catalog_work
// and the catalog_source key behind source 12 — and P5 renames the second to
// `curated` (id unchanged) in the same window this tool runs in. A source-key
// lookup therefore breaks the moment P5's seed lands, which is exactly what
// happened in rehearsal. matched_by is the address maps' own provenance stamp:
// it is what identifies these rows, it is never renamed, and it is exclusive —
// across the whole table the four `wiki:{gid,oid,tid,eid}` stamps occur only on
// source 12 and only on one entity type each (`wiki:link`, a different stamp on
// other sources, does not collide).
//
// There is deliberately no series map: catalog_series holds dlsite series only
// and galgame_series was never mirrored, so `series_id` is retired in place
// (keymap.go, STOP item 3).
type idMaps struct {
	Work   map[int64]int64
	Label  map[int64]int64
	Tag    map[int64]int64
	Engine map[int64]int64

	// Redirects folded per space, reported so a run can say how much of the
	// mapping went through a merge rather than a direct anchor.
	Redirected map[string]int
}

// loadIDMaps reads the four spaces, each resolved to its redirect fixpoint.
// Resolving is not optional: catalog_redirect is how a merged entity keeps its
// old id addressable, and the 147 incident is the record of what reading a
// merged id literally costs (1,618 dead label edges).
func loadIDMaps(ctx context.Context, db *gorm.DB) (*idMaps, error) {
	m := &idMaps{Redirected: map[string]int{}}
	for _, space := range []struct {
		name       string
		matchedBy  string
		entityType int16
		into       *map[int64]int64
	}{
		{"work", matchedByGID, catmodel.EntityTypeWork, &m.Work},
		{"label", matchedByOID, catmodel.EntityTypeLabel, &m.Label},
		{"tag", matchedByTID, catmodel.EntityTypeTag, &m.Tag},
		{"engine", matchedByEID, catmodel.EntityTypeEngine, &m.Engine},
	} {
		redirects, err := loadRedirects(ctx, db, space.entityType)
		if err != nil {
			return nil, fmt.Errorf("load %s redirects: %w", space.name, err)
		}
		var rows []struct {
			ExternalID string `gorm:"column:external_id"`
			EntityID   int64  `gorm:"column:entity_id"`
		}
		if err := db.WithContext(ctx).Raw(
			`SELECT external_id, entity_id FROM catalog_external_ref
			 WHERE matched_by = ? AND entity_type = ? AND link_kind = ?`,
			space.matchedBy, space.entityType, catmodel.LinkKindExact).Scan(&rows).Error; err != nil {
			return nil, fmt.Errorf("load %s refs: %w", space.name, err)
		}
		out := make(map[int64]int64, len(rows))
		for _, row := range rows {
			wikiID, err := strconv.ParseInt(row.ExternalID, 10, 64)
			if err != nil {
				continue // a non-numeric external id is not a wiki id
			}
			target := resolveRedirect(redirects, row.EntityID)
			if target != row.EntityID {
				m.Redirected[space.name]++
			}
			if prior, dup := out[wikiID]; dup && prior != target {
				return nil, fmt.Errorf("%s id space is not 1:1: wiki id %d maps to both %d and %d",
					space.name, wikiID, prior, target)
			}
			out[wikiID] = target
		}
		*space.into = out
	}

	// The work space must be injective as well as functional: two gids landing
	// on one work would collide on uq_edit_revision_entity_seq, and silently
	// interleaving two histories is worse than refusing.
	inverse := make(map[int64]int64, len(m.Work))
	for gid, workID := range m.Work {
		if prior, dup := inverse[workID]; dup {
			return nil, fmt.Errorf("work id space is not injective: gids %d and %d both map to work %d",
				prior, gid, workID)
		}
		inverse[workID] = gid
	}
	return m, nil
}

func loadRedirects(ctx context.Context, db *gorm.DB, entityType int16) (map[int64]int64, error) {
	var rows []struct {
		OldID     int64 `gorm:"column:old_id"`
		CurrentID int64 `gorm:"column:current_id"`
	}
	if err := db.WithContext(ctx).Raw(
		`SELECT old_id, current_id FROM catalog_redirect WHERE entity_type = ?`, entityType).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]int64, len(rows))
	for _, row := range rows {
		out[row.OldID] = row.CurrentID
	}
	return out, nil
}

// resolveRedirect walks a redirect chain to its fixpoint, bounded so a cycle
// in the data cannot hang the migration.
func resolveRedirect(redirects map[int64]int64, id int64) int64 {
	for i := 0; i < 16; i++ {
		next, ok := redirects[id]
		if !ok || next == id {
			return id
		}
		id = next
	}
	return id
}

// The wikirescue address-map steps' provenance stamps (jobs/wikirescue:
// gidmap.go, officialmap.go, tagmap.go, enginemap.go). These strings are the
// migration's key into catalog_external_ref.
const (
	matchedByGID = "wiki:gid"
	matchedByOID = "wiki:oid"
	matchedByTID = "wiki:tid"
	matchedByEID = "wiki:eid"
)
