package main

import (
	"context"
	"fmt"
	"strconv"

	catmodel "api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

type idMaps struct {
	Work   map[int64]int64
	Label  map[int64]int64
	Tag    map[int64]int64
	Engine map[int64]int64

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
				continue
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

const (
	matchedByGID = "wiki:gid"
	matchedByOID = "wiki:oid"
	matchedByTID = "wiki:tid"
	matchedByEID = "wiki:eid"
)
