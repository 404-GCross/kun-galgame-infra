// migrate-tag-names localizes the wiki tag set: every galgame_tag whose name is
// an English tagMap key is converted to its Chinese (tagMap) name. This cleans up
// the historical mix where the draft sync created English tag names while the old
// relations bypass created Chinese ones, leaving both in prod.
//
// Per tag (name is an English tagMap key, mapped Chinese ≠ the name itself):
//   - RENAME — no tag with the Chinese name exists yet: UPDATE galgame_tag.name.
//     The id is unchanged, so revision snapshots (which store tag_ids) need no
//     patch; only the displayed name changes.
//   - MERGE  — a Chinese-named tag already exists (a historical English+Chinese
//     duplicate): repoint galgame_tag_relation onto the Chinese tag (dedup,
//     keeping spoiler_level/source), jsonb-patch each affected game's LATEST
//     revision snapshot tag_ids to live membership (APPROACH B — no new revision,
//     matching ReconcileVndbTags / normalize-galgame-intro), drop the English
//     tag's aliases, then delete the English tag.
//
// Tags whose tagMap value equals the name (kept abbreviations like NTR/BDSM) are
// skipped. cnt is a query-time COUNT (tag_repository), so it self-corrects.
//
// Idempotent: after a full apply no English-named tagMap-key tags remain, so a
// re-run plans 0 actions.
//
//	go run ./cmd/migrate-tag-names                 # dry run (report only)
//	go run ./cmd/migrate-tag-names --apply --limit 5   # apply a tiny test batch
//	go run ./cmd/migrate-tag-names --apply         # perform all
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"sort"

	"api/internal/infrastructure/database"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/vndbresolve"
	"api/pkg/config"
	"api/pkg/logger"

	"gorm.io/gorm"
)

type action struct {
	EnID   int
	EnName string
	Target string // Chinese name
	ZhID   int    // >0 = planned merge into this id; 0 = planned rename (display only)
}

func main() {
	apply := flag.Bool("apply", false, "perform the changes (default: dry run, nothing written)")
	tagmapPath := flag.String("tagmap", "", "path to tagMap.ts (default: $KUN_VNDB_TAGMAP_PATH, else docs/tagMap.ts)")
	samples := flag.Int("samples", 30, "number of planned actions to print")
	limit := flag.Int("limit", 0, "cap the number of actions applied (0 = all); for a safe test batch")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	// Resolve the tagMap path AFTER config.Load so .env's KUN_VNDB_TAGMAP_PATH is
	// honored (a flag default would be evaluated before .env is loaded).
	tmPath := *tagmapPath
	if tmPath == "" {
		tmPath = vndbresolve.DefaultTagMapPath()
	}
	tagMap, err := vndbresolve.ParseTagMap(tmPath)
	if err != nil {
		slog.Error("parse tagMap", "error", err, "path", tmPath)
		os.Exit(1)
	}
	fmt.Printf("tagMap entries: %d (path %s)\n", len(tagMap), tmPath)

	db, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		slog.Error("connect galgame db", "error", err, "dbname", cfg.GalgameDatabase.DBName)
		os.Exit(1)
	}
	defer db.Close()
	gdb := db.DB()

	var tags []model.GalgameTag
	if err := gdb.Select("id", "name").Find(&tags).Error; err != nil {
		slog.Error("load tags", "error", err)
		os.Exit(1)
	}
	sort.Slice(tags, func(i, j int) bool { return tags[i].ID < tags[j].ID })

	// willExist: Chinese name → id, seeded with every current tag name and updated
	// as planned renames create names, so a synonym (two English keys → one
	// Chinese) after the first rename is planned as a merge, not a colliding rename.
	willExist := make(map[string]int, len(tags))
	for _, t := range tags {
		willExist[t.Name] = t.ID
	}

	var plan []action
	renames, merges := 0, 0
	for _, t := range tags {
		target, ok := tagMap[t.Name]
		if !ok || target == t.Name {
			continue // not an English tagMap key, or a kept abbreviation (NTR→NTR)
		}
		if zhID, exists := willExist[target]; exists && zhID != t.ID {
			plan = append(plan, action{t.ID, t.Name, target, zhID})
			merges++
		} else {
			plan = append(plan, action{t.ID, t.Name, target, 0})
			willExist[target] = t.ID
			renames++
		}
	}

	fmt.Printf("\n==================== PLAN ====================\n")
	fmt.Printf("English-named tags that are tagMap keys: %d\n", len(plan))
	fmt.Printf("  · renames (Chinese name free):     %d\n", renames)
	fmt.Printf("  · merges  (Chinese twin exists):   %d\n", merges)
	for i, a := range plan {
		if i >= *samples {
			fmt.Printf("  … (+%d more)\n", len(plan)-*samples)
			break
		}
		if a.ZhID > 0 {
			fmt.Printf("  MERGE  #%d %q → #%d %q\n", a.EnID, a.EnName, a.ZhID, a.Target)
		} else {
			fmt.Printf("  RENAME #%d %q → %q\n", a.EnID, a.EnName, a.Target)
		}
	}

	if !*apply {
		fmt.Printf("\nDRY RUN — nothing written. Re-run with --apply to perform.\n")
		return
	}

	toApply := plan
	if *limit > 0 && *limit < len(toApply) {
		toApply = toApply[:*limit]
		fmt.Printf("\n--limit %d: applying only the first %d actions\n", *limit, len(toApply))
	}
	fmt.Printf("\nAPPLYING %d actions (approach B for merges: repoint relations + patch latest snapshot, no new revision)…\n", len(toApply))

	var didRename, didMerge, patchedGames int
	for _, a := range toApply {
		if err := gdb.Transaction(func(tx *gorm.DB) error {
			// Resolve the target id live — an earlier rename in this run may have
			// just created it (synonyms), and prod state is authoritative.
			var zh model.GalgameTag
			if err := tx.Where("name = ?", a.Target).Limit(1).Find(&zh).Error; err != nil {
				return err
			}
			if zh.ID == 0 {
				didRename++
				return tx.Exec(`UPDATE galgame_tag SET name = ?, updated = NOW() WHERE id = ?`, a.Target, a.EnID).Error
			}
			if zh.ID == a.EnID {
				return nil // already merged (idempotent)
			}
			// MERGE a.EnID → zh.ID
			var gids []int
			if err := tx.Model(&model.GalgameTagRelation{}).
				Where("tag_id = ?", a.EnID).Pluck("galgame_id", &gids).Error; err != nil {
				return err
			}
			// Repoint relations onto the Chinese tag, keeping spoiler/source; a game
			// already tagged with the Chinese tag keeps that row (DO NOTHING).
			if err := tx.Exec(`
				INSERT INTO galgame_tag_relation (galgame_id, tag_id, spoiler_level, source, created, updated)
				SELECT galgame_id, ?, spoiler_level, source, created, NOW()
				FROM galgame_tag_relation WHERE tag_id = ?
				ON CONFLICT (galgame_id, tag_id) DO NOTHING
			`, zh.ID, a.EnID).Error; err != nil {
				return err
			}
			if err := tx.Exec(`DELETE FROM galgame_tag_relation WHERE tag_id = ?`, a.EnID).Error; err != nil {
				return err
			}
			if err := tx.Exec(`DELETE FROM galgame_tag_alias WHERE galgame_tag_id = ?`, a.EnID).Error; err != nil {
				return err
			}
			if err := tx.Exec(`DELETE FROM galgame_tag WHERE id = ?`, a.EnID).Error; err != nil {
				return err
			}
			// Keep each affected game's latest revision snapshot == live tag set.
			for _, gid := range gids {
				var ids []int
				if err := tx.Model(&model.GalgameTagRelation{}).
					Where("galgame_id = ?", gid).Order("tag_id").Pluck("tag_id", &ids).Error; err != nil {
					return err
				}
				idsJSON, err := json.Marshal(ids)
				if err != nil {
					return err
				}
				if err := tx.Exec(`
					UPDATE galgame_revision
					SET snapshot = jsonb_set(snapshot, '{tag_ids}', ?::jsonb, true)
					WHERE galgame_id = ?
					  AND revision = (SELECT max(revision) FROM galgame_revision WHERE galgame_id = ?)
				`, string(idsJSON), gid, gid).Error; err != nil {
					return err
				}
			}
			didMerge++
			patchedGames += len(gids)
			return nil
		}); err != nil {
			slog.Error("apply action failed", "en", a.EnName, "target", a.Target, "error", err)
			os.Exit(1)
		}
	}

	fmt.Printf("\nDONE. renamed=%d merged=%d (snapshots patched for %d game-relations).\n", didRename, didMerge, patchedGames)
}
