package service

import (
	"context"
	"encoding/json"

	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/vndb"

	"gorm.io/gorm"
)

// SyncVndbLinks reconciles a galgame's VNDB-sourced links to the current VNDB
// truth — adding store/official links, dropping ones VNDB no longer lists —
// while keeping genuinely user-added links. Legacy UNMARKED imports (dumped as
// source="" by old syncs) are recognized by host (matches a fresh link's host,
// or a known info/stats host) and replaced, so the result is clean.
//
// Write strategy is APPROACH B (matching the intro cleanup): it rewrites just
// galgame_link and jsonb-patches the latest revision's snapshot so live ==
// latest snapshot (§1.5 #4), WITHOUT minting a new revision — a ~50k-row
// backfill must not create 50k revisions. (The future create-time path will use
// the ApplySnapshot+revision flow instead, where volume is tiny.)
//
// Returns whether anything changed and the freshly-fetched VNDB link set (for
// reporting). No-op for galgames without a canonical vndb_id. apply=false
// fetches + diffs but writes nothing.
func SyncVndbLinks(ctx context.Context, db *gorm.DB, vc *vndb.Client, galgameID int, apply bool) (bool, []model.SnapshotLink, error) {
	full, err := loadGalgameWithRelations(db.WithContext(ctx), galgameID)
	if err != nil {
		return false, nil, err
	}
	if !vndbIDRegex.MatchString(full.VNDBID) {
		return false, nil, nil // no canonical vndb_id → nothing to sync
	}

	fresh, err := vc.FetchGameLinks(full.VNDBID)
	if err != nil {
		return false, nil, err
	}

	cur := model.TakeSnapshot(full)
	next := *cur

	// VNDB sync OWNS the store/official/info link space. Drop anything it
	// manages — old vndb-marked links (replaced by fresh), plus legacy UNMARKED
	// imports recognized by host — and keep the rest as user links.
	managed := make(map[string]bool, len(fresh))
	for _, l := range fresh {
		if h := vndb.Host(l.Link); h != "" {
			managed[h] = true
		}
	}
	userLinks := make([]model.SnapshotLink, 0, len(cur.Links))
	for _, l := range cur.Links {
		if l.Source == "vndb" {
			continue
		}
		h := vndb.Host(l.Link)
		if managed[h] || vndb.IsInfoHost(h) {
			continue
		}
		userLinks = append(userLinks, l)
	}
	next.Links = append(userLinks, fresh...)

	if len(model.ChangedKeys(cur, &next)) == 0 {
		return false, fresh, nil
	}
	if !apply {
		return true, fresh, nil
	}

	linksJSON, err := json.Marshal(next.Links)
	if err != nil {
		return false, fresh, err
	}
	userID := full.UserID
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("galgame_id = ?", galgameID).Delete(&model.GalgameLink{}).Error; err != nil {
			return err
		}
		rows := make([]model.GalgameLink, 0, len(next.Links))
		for _, l := range next.Links {
			rows = append(rows, model.GalgameLink{
				GalgameID: galgameID, UserID: userID,
				Name: l.Name, Link: l.Link, Source: l.Source, SourceKey: l.SourceKey,
			})
		}
		if len(rows) > 0 {
			if err := tx.Create(&rows).Error; err != nil {
				return err
			}
		}
		// Keep the latest revision snapshot == DB (§1.5 #4) without a new revision.
		return tx.Exec(`
			UPDATE galgame_revision
			SET snapshot = jsonb_set(snapshot, '{links}', ?::jsonb, true)
			WHERE galgame_id = ?
			  AND revision = (SELECT max(revision) FROM galgame_revision WHERE galgame_id = ?)
		`, string(linksJSON), galgameID, galgameID).Error
	})
	if err != nil {
		return false, fresh, err
	}
	return true, fresh, nil
}
