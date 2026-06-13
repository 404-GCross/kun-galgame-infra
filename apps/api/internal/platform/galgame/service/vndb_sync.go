package service

import (
	"context"

	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/repository"
	"api/internal/platform/galgame/vndb"

	"gorm.io/gorm"
)

// SyncVndbLinks reconciles a galgame's VNDB-sourced links (Source="vndb") to the
// current VNDB truth — adding new store/official links, dropping ones VNDB no
// longer lists — while leaving user-added links (Source="") completely
// untouched. It commits through the canonical ApplySnapshot path as a MINOR
// system revision, so there is no snapshot drift (§1.5 #4) and the change is
// auditable / revertible like any edit.
//
// Returns whether anything changed and the freshly-fetched VNDB link set (for
// reporting). No-op (no fetch side effects aside) for galgames without a
// canonical vndb_id. When apply is false it fetches + diffs but writes nothing.
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
	// imports that earlier syncs dumped as source="" — recognized by host
	// (matches a fresh link's host, or a known info/stats host). What remains is
	// genuinely user-authored (forum topics, misc) and is kept untouched.
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

	userID := full.UserID
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := repository.ApplySnapshot(tx, galgameID, userID, &next); err != nil {
			return err
		}
		nextRev, err := repository.NextRevision(tx, galgameID)
		if err != nil {
			return err
		}
		after, err := loadGalgameWithRelations(tx, galgameID)
		if err != nil {
			return err
		}
		snap, err := model.TakeSnapshot(after).ToJSON()
		if err != nil {
			return err
		}
		return tx.Create(&model.GalgameRevision{
			GalgameID: galgameID,
			Revision:  nextRev,
			UserID:    userID,
			Action:    "updated",
			Note:      "vndb link sync",
			Snapshot:  snap,
			IsMinor:   true,
		}).Error
	})
	if err != nil {
		return false, fresh, err
	}
	return true, fresh, nil
}
