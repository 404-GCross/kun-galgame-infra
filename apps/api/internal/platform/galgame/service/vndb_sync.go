package service

import (
	"context"
	"encoding/json"

	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/vndb"

	"gorm.io/gorm"
)

// ReconcileVndbLinks reconciles one galgame's links to a freshly-fetched VNDB
// link set — adding store/official links, dropping ones VNDB no longer lists —
// while keeping genuinely user-added links. Legacy UNMARKED imports (dumped as
// source="" by old syncs) are recognized by host (matches a fresh link's host,
// or a known info/stats host) and replaced, so the result is clean.
//
// The caller fetches `fresh` (batched, see vndb.FetchGameLinksBatch) so a bulk
// backfill makes ~one VNDB call per 100 games instead of two per game.
//
// Write strategy is APPROACH B (matching the intro cleanup): it rewrites just
// galgame_link and jsonb-patches the latest revision's snapshot so live ==
// latest snapshot (§1.5 #4), WITHOUT minting a new revision — a ~50k-row
// backfill must not create 50k revisions. (The future create-time path will use
// the ApplySnapshot+revision flow instead, where volume is tiny.)
//
// Returns whether anything changed. apply=false diffs but writes nothing.
func ReconcileVndbLinks(ctx context.Context, db *gorm.DB, galgameID int, fresh []model.SnapshotLink, apply bool) (bool, error) {
	full, err := loadGalgameWithRelations(db.WithContext(ctx), galgameID)
	if err != nil {
		return false, err
	}

	cur := model.TakeSnapshot(full)
	next := *cur
	// VNDB sync OWNS the store/official/info link space: replace the old
	// vndb-marked set (and legacy unmarked imports recognized by host) with
	// `fresh`, keeping genuine user links. See mergeUserAndVndbLinks.
	next.Links = mergeUserAndVndbLinks(cur.Links, fresh)

	if len(model.ChangedKeys(cur, &next)) == 0 {
		return false, nil
	}
	if !apply {
		return true, nil
	}

	linksJSON, err := json.Marshal(next.Links)
	if err != nil {
		return false, err
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
		return false, err
	}
	return true, nil
}

// vndbManagedLinks returns the sync-owned (source="vndb") subset of a link set.
func vndbManagedLinks(links []model.SnapshotLink) []model.SnapshotLink {
	out := make([]model.SnapshotLink, 0, len(links))
	for _, l := range links {
		if l.Source == "vndb" {
			out = append(out, l)
		}
	}
	return out
}

// mergeUserAndVndbLinks combines user-supplied links with the sync-managed VNDB
// link set, keeping the two disjoint by host: a user link is dropped when it
// lands on a host a VNDB link already covers (the client echoing a managed link
// back) or on a known info/stats host. Any source="vndb" entries among the user
// candidates are ignored — vndbLinks is the authoritative managed set. The
// result is ordered [user…, vndb…], the one canonical order shared by the sync
// (ReconcileVndbLinks) and the edit path (overlayUpdate) so re-syncs and
// no-op edits stay change-free.
func mergeUserAndVndbLinks(userCandidates, vndbLinks []model.SnapshotLink) []model.SnapshotLink {
	managed := make(map[string]bool, len(vndbLinks))
	for _, l := range vndbLinks {
		if h := vndb.Host(l.Link); h != "" {
			managed[h] = true
		}
	}
	out := make([]model.SnapshotLink, 0, len(userCandidates)+len(vndbLinks))
	for _, l := range userCandidates {
		if l.Source == "vndb" {
			continue
		}
		h := vndb.Host(l.Link)
		if managed[h] || vndb.IsInfoHost(h) {
			continue
		}
		out = append(out, l)
	}
	return append(out, vndbLinks...)
}
