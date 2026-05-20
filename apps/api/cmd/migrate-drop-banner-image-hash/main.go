// migrate-drop-banner-image-hash retires the legacy
// `galgame.banner_image_hash` column. After PR2 backfilled covers and
// PR5 dropped the Go-side field, this one-shot cleans up:
//
//  1. Patches every galgame_revision.snapshot + galgame_pr.snapshot jsonb
//     that still carries `banner_image_hash`. The hash is flattened into
//     `covers[]` (if it's not already there, inject a fresh sort_order=0
//     entry; if covers[] is empty, that entry IS the whole array). Then
//     the `banner_image_hash` jsonb field is stripped so old code
//     deserialising a new snapshot never gets a misleading non-empty
//     scalar back.
//  2. Drops the physical `galgame.banner_image_hash` column.
//
// Safe to re-run: step 1 detects already-patched snapshots (no
// `banner_image_hash` jsonb key, or hash already present in covers);
// step 2 uses DROP COLUMN IF EXISTS.
//
// Per docs/galgame_wiki/99-final-upgrade-plan.md §5.5 stage 2 + §10 PR5.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
	"api/pkg/config"
	"api/pkg/logger"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "scan + report only; no writes")
	keepColumn := flag.Bool("keep-column", false, "patch snapshots but leave the legacy column (debug only)")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load", "error", err)
		os.Exit(1)
	}
	logger.Init(cfg.Server.Env)

	wikiDB, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		slog.Error("connect galgame wiki db", "error", err)
		os.Exit(1)
	}
	defer wikiDB.Close()
	db := wikiDB.DB()

	slog.Info("starting banner_image_hash retirement", "dry_run", *dryRun, "keep_column", *keepColumn)

	hasColumn := columnExists(db, "galgame", "banner_image_hash")
	slog.Info("legacy column present", "yes", hasColumn)

	ctx := context.Background()

	// ── Step 1: patch snapshots ──
	if err := patchSnapshots(ctx, db, "galgame_revision", *dryRun); err != nil {
		slog.Error("patch galgame_revision snapshots", "error", err)
		os.Exit(1)
	}
	if err := patchSnapshots(ctx, db, "galgame_pr", *dryRun); err != nil {
		slog.Error("patch galgame_pr snapshots", "error", err)
		os.Exit(1)
	}

	// ── Step 2: drop the column ──
	if *dryRun {
		slog.Info("step 2 skipped: dry-run")
	} else if *keepColumn {
		slog.Info("step 2 skipped: --keep-column flag set")
	} else if hasColumn {
		if err := db.Exec(`ALTER TABLE galgame DROP COLUMN IF EXISTS banner_image_hash`).Error; err != nil {
			slog.Error("drop banner_image_hash column", "error", err)
			os.Exit(1)
		}
		slog.Info("step 2 ok: dropped galgame.banner_image_hash column")
	}

	slog.Info("migration complete")
}

func columnExists(db *gorm.DB, table, column string) bool {
	var count int64
	if err := db.Raw(`
		SELECT COUNT(*) FROM information_schema.columns
		WHERE table_schema='public' AND table_name=? AND column_name=?
	`, table, column).Scan(&count).Error; err != nil {
		return false
	}
	return count > 0
}

// patchSnapshots walks every row of `table` whose `snapshot` jsonb
// still carries the legacy `banner_image_hash` field. For each such
// row, it merges the hash into covers[] (when absent), strips the
// banner_image_hash field, and writes back. Skipped rows are
// idempotent — re-runs find no matching rows.
//
// Works on galgame_revision and galgame_pr (same snapshot shape).
func patchSnapshots(ctx context.Context, db *gorm.DB, table string, dryRun bool) error {
	type row struct {
		ID       int
		Snapshot datatypes.JSON
	}
	var rows []row
	// PG jsonb ? operator: true when the top-level object has that key.
	if err := db.WithContext(ctx).Raw(
		`SELECT id, snapshot FROM ` + table + ` WHERE snapshot ? 'banner_image_hash'`,
	).Scan(&rows).Error; err != nil {
		return err
	}
	slog.Info("step 1: patch scope", "table", table, "rows_to_visit", len(rows))

	patched := 0
	for _, r := range rows {
		out, changed, err := flattenBannerHashIntoCovers(r.Snapshot)
		if err != nil {
			slog.Warn("step 1: snapshot parse failed; left untouched", "table", table, "id", r.ID, "error", err)
			continue
		}
		if !changed {
			continue
		}
		if dryRun {
			patched++
			continue
		}
		if err := db.WithContext(ctx).Exec(
			`UPDATE `+table+` SET snapshot = ? WHERE id = ?`, out, r.ID,
		).Error; err != nil {
			return err
		}
		patched++
	}
	slog.Info("step 1 ok: patched", "table", table, "rows_updated", patched)
	return nil
}

// flattenBannerHashIntoCovers takes a snapshot jsonb byte slice and:
//
//  1. Extracts banner_image_hash (if present and non-empty).
//  2. Ensures covers[] exists and contains an entry whose image_hash
//     matches AND has sort_order=0. If covers[] is empty, INSERT one.
//     If a different hash is already at sort_order=0, do NOT overwrite
//     it — the curated multi-cover state wins; the legacy banner_image_hash
//     was just one (now redundant) view of the same data.
//  3. Removes the banner_image_hash field from the snapshot.
//
// Returns (newBytes, changed, err). Unmarshals into map[string]any so
// any future-added fields pass through untouched.
func flattenBannerHashIntoCovers(raw []byte) ([]byte, bool, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, false, err
	}
	rawHash, ok := m["banner_image_hash"]
	if !ok {
		return raw, false, nil
	}
	hash, _ := rawHash.(string)
	delete(m, "banner_image_hash")

	if hash != "" {
		// Promote hash into covers[] only when no sort_order=0 row exists.
		// If somebody's already curated a pinned cover that differs from
		// banner_image_hash, the curated choice wins — don't second-guess.
		covers, _ := m["covers"].([]any)
		hasPinned := false
		alreadyContainsHash := false
		for _, c := range covers {
			cm, _ := c.(map[string]any)
			if cm == nil {
				continue
			}
			so, _ := cm["sort_order"].(float64)
			if int(so) == 0 {
				hasPinned = true
			}
			ih, _ := cm["image_hash"].(string)
			if ih == hash {
				alreadyContainsHash = true
			}
		}
		if !hasPinned {
			// Prepend a fresh pinned cover. Default-everything for the
			// metadata; the historical snapshot didn't carry it either.
			entry := map[string]any{
				"image_hash": hash, "sort_order": 0,
				"sexual": 0, "violence": 0, "source": "", "source_key": "",
			}
			if alreadyContainsHash {
				// Hash is in the array but not at sort_order=0; bump that
				// existing row to sort_order=0 instead of duplicating.
				for i, c := range covers {
					cm, _ := c.(map[string]any)
					if cm != nil && cm["image_hash"] == hash {
						cm["sort_order"] = float64(0)
						covers[i] = cm
						break
					}
				}
				m["covers"] = covers
			} else {
				m["covers"] = append([]any{entry}, covers...)
			}
		}
	}

	out, err := json.Marshal(m)
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}
