package service

import (
	"context"
	"encoding/json"
	"sort"

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
// back) or on a known info/stats host, and exact-duplicate user URLs collapse to
// one. Any source="vndb" entries among the user candidates are ignored —
// vndbLinks is the authoritative managed set. The result is ordered
// [user…, vndb…], the one canonical order shared by the sync
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
	seen := make(map[string]bool, len(userCandidates)) // collapse duplicate user URLs
	for _, l := range userCandidates {
		if l.Source == "vndb" {
			continue
		}
		h := vndb.Host(l.Link)
		if managed[h] || vndb.IsInfoHost(h) || seen[l.Link] {
			continue
		}
		seen[l.Link] = true
		out = append(out, l)
	}
	return append(out, vndbLinks...)
}

// ReconcileVndbTags reconciles a galgame's source="vndb" tag relations to the
// freshly-resolved VNDB set, preserving user (source="") tags. `desired` maps
// wiki tag_id → spoiler level (the caller resolved VNDB tags via vndbresolve).
//
// Like ReconcileVndbLinks this is APPROACH B: rewrite the relation rows +
// jsonb-patch the latest revision snapshot's tag_ids, NO new revision. Provenance
// (source) is a DB column only, never in the snapshot — reconcileSet (the
// create/edit/revert path) is delta-based and preserves it on kept rows. The
// first run over a game is the classification: tags VNDB lists become
// source="vndb" (overlapping user rows convert), the rest stay user. Idempotent.
//
// Returns whether anything changed. apply=false diffs but writes nothing.
func ReconcileVndbTags(ctx context.Context, db *gorm.DB, galgameID int, desired map[int]int, apply bool) (bool, error) {
	var rels []model.GalgameTagRelation
	if err := db.WithContext(ctx).Where("galgame_id = ?", galgameID).Find(&rels).Error; err != nil {
		return false, err
	}
	type meta struct {
		source  string
		spoiler int
	}
	current := make(map[int]meta, len(rels))
	for _, r := range rels {
		current[r.TagID] = meta{r.Source, r.SpoilerLevel}
	}
	// Target = user tags not in the VNDB set (kept as-is) + the VNDB set (vndb-owned).
	target := make(map[int]meta, len(current))
	for id, m := range current {
		if m.source != "vndb" {
			if _, isVndb := desired[id]; !isVndb {
				target[id] = m
			}
		}
	}
	for id, spoiler := range desired {
		target[id] = meta{"vndb", spoiler}
	}

	if len(current) == len(target) {
		same := true
		for id, m := range current {
			if t, ok := target[id]; !ok || t != m {
				same = false
				break
			}
		}
		if same {
			return false, nil
		}
	}
	if !apply {
		return true, nil
	}

	ids := make([]int, 0, len(target))
	for id := range target {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	idsJSON, err := json.Marshal(ids)
	if err != nil {
		return false, err
	}

	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("DELETE FROM galgame_tag_relation WHERE galgame_id = ? AND source = 'vndb'", galgameID).Error; err != nil {
			return err
		}
		for id, spoiler := range desired {
			if err := tx.Exec(`
				INSERT INTO galgame_tag_relation (galgame_id, tag_id, spoiler_level, source, created, updated)
				VALUES (?, ?, ?, 'vndb', NOW(), NOW())
				ON CONFLICT (galgame_id, tag_id) DO UPDATE SET source = 'vndb', spoiler_level = EXCLUDED.spoiler_level, updated = NOW()
			`, galgameID, id, spoiler).Error; err != nil {
				return err
			}
		}
		return patchSnapshotIDs(tx, galgameID, "tag_ids", idsJSON)
	})
	if err != nil {
		return false, err
	}
	return true, nil
}

// ReconcileVndbOfficials is ReconcileVndbTags for officials (developers) —
// source-only, no spoiler. `desired` is the resolved wiki official_id set.
func ReconcileVndbOfficials(ctx context.Context, db *gorm.DB, galgameID int, desired []int, apply bool) (bool, error) {
	var rels []model.GalgameOfficialRelation
	if err := db.WithContext(ctx).Where("galgame_id = ?", galgameID).Find(&rels).Error; err != nil {
		return false, err
	}
	want := make(map[int]bool, len(desired))
	for _, id := range desired {
		want[id] = true
	}
	current := make(map[int]string, len(rels)) // official_id → source
	for _, r := range rels {
		current[r.OfficialID] = r.Source
	}
	target := make(map[int]string, len(current))
	for id, src := range current {
		if src != "vndb" && !want[id] {
			target[id] = src
		}
	}
	for id := range want {
		target[id] = "vndb"
	}

	if len(current) == len(target) {
		same := true
		for id, src := range current {
			if t, ok := target[id]; !ok || t != src {
				same = false
				break
			}
		}
		if same {
			return false, nil
		}
	}
	if !apply {
		return true, nil
	}

	ids := make([]int, 0, len(target))
	for id := range target {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	idsJSON, err := json.Marshal(ids)
	if err != nil {
		return false, err
	}

	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("DELETE FROM galgame_official_relation WHERE galgame_id = ? AND source = 'vndb'", galgameID).Error; err != nil {
			return err
		}
		for id := range want {
			if err := tx.Exec(`
				INSERT INTO galgame_official_relation (galgame_id, official_id, source, created, updated)
				VALUES (?, ?, 'vndb', NOW(), NOW())
				ON CONFLICT (galgame_id, official_id) DO UPDATE SET source = 'vndb', updated = NOW()
			`, galgameID, id).Error; err != nil {
				return err
			}
		}
		return patchSnapshotIDs(tx, galgameID, "official_ids", idsJSON)
	})
	if err != nil {
		return false, err
	}
	return true, nil
}

// patchSnapshotIDs jsonb-patches one int-array field of the latest revision
// snapshot so live == latest snapshot (§1.5 #4) without minting a revision.
func patchSnapshotIDs(tx *gorm.DB, galgameID int, field string, idsJSON []byte) error {
	return tx.Exec(`
		UPDATE galgame_revision
		SET snapshot = jsonb_set(snapshot, ?, ?::jsonb, true)
		WHERE galgame_id = ?
		  AND revision = (SELECT max(revision) FROM galgame_revision WHERE galgame_id = ?)
	`, "{"+field+"}", string(idsJSON), galgameID, galgameID).Error
}
