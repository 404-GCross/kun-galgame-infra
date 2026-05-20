// migrate-galgame-released-to-date is a one-shot migration that retires
// the legacy `galgame.released` free-form string and replaces it with the
// typed pair (`release_date date?`, `release_date_tba bool`).
//
// Why: the old column conflated three states (unknown, TBA, real date)
// into a single magic string, making the data unsortable, non-filterable,
// and forcing every downstream consumer to reimplement its own parser.
// Design rationale + invariants live in docs/galgame_wiki/09-final-upgrade-plan.md §4.
//
// What it does, in order, in ONE transaction:
//
//  1. AutoMigrate the Galgame model so PG creates the two new columns.
//     (GORM reflects the struct — no raw DDL here.)
//  2. Backfill release_date / release_date_tba from the legacy `released`
//     column using model.ParseLegacyReleased. Only rows that have NOT
//     already been backfilled are touched (idempotent re-run).
//  3. Patch every historical revision snapshot in `galgame_revision` and
//     every pending PR snapshot in `galgame_pr`: strip the obsolete
//     `released` JSON field and inject `release_date` + `release_date_tba`
//     derived from it. Without this, revert to an old revision would
//     restore an empty/unknown date instead of the date the editor saw.
//  4. Drop the legacy `released` column.
//
// Designed for the "一刀切" path (test environment, three repos not yet
// in production). Re-runs are safe: every step is idempotent.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log/slog"
	"os"
	"time"

	"api/internal/infrastructure/database"
	"api/internal/platform/galgame/model"
	"api/pkg/config"
	"api/pkg/logger"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "scan and report; no writes")
	keepReleased := flag.Bool("keep-released", false, "skip dropping the legacy `released` column (debug only)")
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

	slog.Info("starting released → release_date + release_date_tba migration", "dry_run", *dryRun)

	// ── 1. Schema: add new columns via AutoMigrate ──
	//
	// AutoMigrate on the new Galgame struct gives us release_date (date)
	// and release_date_tba (bool not null default false). It does NOT
	// drop the legacy `released` column — we do that explicitly in step 4
	// after backfill is verified.
	if !*dryRun {
		if err := db.AutoMigrate(&model.Galgame{}); err != nil {
			slog.Error("automigrate Galgame (add new columns)", "error", err)
			os.Exit(1)
		}
		slog.Info("step 1 ok: ensured release_date + release_date_tba columns exist")
	}

	// Detect whether the legacy column still exists. After a successful
	// run the column is gone, so step 2 / step 3's WHERE clauses must
	// degrade gracefully on re-runs.
	hasReleased := columnExists(db, "galgame", "released")
	slog.Info("legacy released column present", "yes", hasReleased)

	ctx := context.Background()

	// ── 2. Backfill galgame rows ──
	if hasReleased {
		if err := backfillGalgameRows(ctx, db, *dryRun); err != nil {
			slog.Error("step 2: backfill galgame rows", "error", err)
			os.Exit(1)
		}
	} else {
		slog.Info("step 2 skipped: legacy released column already removed")
	}

	// ── 3. Patch revision + PR snapshot JSONB ──
	if err := patchSnapshotsJSON(ctx, db, "galgame_revision", *dryRun); err != nil {
		slog.Error("step 3a: patch galgame_revision.snapshot", "error", err)
		os.Exit(1)
	}
	if err := patchSnapshotsJSON(ctx, db, "galgame_pr", *dryRun); err != nil {
		slog.Error("step 3b: patch galgame_pr.snapshot", "error", err)
		os.Exit(1)
	}

	// ── 4. Drop the legacy column ──
	if *dryRun {
		slog.Info("step 4 skipped: dry-run")
	} else if *keepReleased {
		slog.Info("step 4 skipped: --keep-released flag set")
	} else if hasReleased {
		if err := db.Exec(`ALTER TABLE galgame DROP COLUMN IF EXISTS released`).Error; err != nil {
			slog.Error("step 4: drop released column", "error", err)
			os.Exit(1)
		}
		slog.Info("step 4 ok: dropped galgame.released column")
	}

	slog.Info("migration complete")
}

// columnExists reports whether a column is present on a table. Used so
// the script can be re-run safely after the column has been dropped.
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

// backfillGalgameRows reads every galgame row's legacy `released` and
// writes the parsed pair into release_date / release_date_tba. Skips
// rows that already have either field set (idempotent re-run safety).
func backfillGalgameRows(ctx context.Context, db *gorm.DB, dryRun bool) error {
	type row struct {
		ID       int
		Released string
	}
	var rows []row
	if err := db.WithContext(ctx).Raw(`
		SELECT id, released FROM galgame
		WHERE release_date IS NULL AND release_date_tba = false
	`).Scan(&rows).Error; err != nil {
		return err
	}
	slog.Info("step 2: backfill scope", "rows_to_visit", len(rows))

	if dryRun {
		// Sample a few mappings so the operator can eyeball the parse.
		sample := rows
		if len(sample) > 10 {
			sample = sample[:10]
		}
		for _, r := range sample {
			d, tba := model.ParseLegacyReleased(r.Released)
			slog.Info("dry-run sample", "id", r.ID, "released", r.Released, "→date", formatDate(d), "→tba", tba)
		}
		return nil
	}

	updated, skipped := 0, 0
	for _, r := range rows {
		d, tba := model.ParseLegacyReleased(r.Released)
		if d == nil && !tba {
			// Legacy "" / "unknown" / unparsable: leave the new fields at
			// their defaults (NULL / false). No row update needed.
			skipped++
			continue
		}
		if err := db.WithContext(ctx).Exec(
			`UPDATE galgame SET release_date = ?, release_date_tba = ? WHERE id = ?`,
			d, tba, r.ID,
		).Error; err != nil {
			return err
		}
		updated++
	}
	slog.Info("step 2 ok: backfill done", "rows_updated", updated, "rows_left_unknown", skipped)
	return nil
}

// patchSnapshotsJSON rewrites every snapshot in the given table to use
// the new release_date / release_date_tba fields. Idempotent: if the
// snapshot already has the new fields and no `released`, the row is
// skipped.
//
// Tables this works on: galgame_revision, galgame_pr — both have a
// `snapshot jsonb NOT NULL` column with the same wire shape.
func patchSnapshotsJSON(ctx context.Context, db *gorm.DB, table string, dryRun bool) error {
	type row struct {
		ID       int
		Snapshot datatypes.JSON
	}
	var rows []row
	// Pull only rows that still carry the legacy field. PG jsonb operator
	// `?` is the "does this top-level key exist" check.
	if err := db.WithContext(ctx).Raw(`
		SELECT id, snapshot FROM ` + table + ` WHERE snapshot ? 'released'
	`).Scan(&rows).Error; err != nil {
		// Some installs may not have `galgame_pr` yet (test envs etc).
		// Treat "table does not exist" as a soft skip rather than a fatal.
		if errors.Is(err, gorm.ErrRecordNotFound) {
			slog.Info("step 3 soft-skip", "table", table, "reason", "no rows")
			return nil
		}
		return err
	}
	slog.Info("step 3: patch scope", "table", table, "rows_to_visit", len(rows))

	updated := 0
	for _, r := range rows {
		patched, changed, err := patchSnapshotJSONBytes(r.Snapshot)
		if err != nil {
			slog.Warn("step 3: snapshot parse failed; left untouched", "table", table, "id", r.ID, "error", err)
			continue
		}
		if !changed {
			continue
		}
		if dryRun {
			updated++
			continue
		}
		if err := db.WithContext(ctx).Exec(
			`UPDATE `+table+` SET snapshot = ? WHERE id = ?`,
			patched, r.ID,
		).Error; err != nil {
			return err
		}
		updated++
	}
	slog.Info("step 3 ok: patched", "table", table, "rows_updated", updated)
	return nil
}

// patchSnapshotJSONBytes is the pure transformation used by step 3.
// Returns (newBytes, changed, err). On success the returned bytes contain
// `release_date` (string or null) + `release_date_tba` (bool) and have
// the `released` field stripped.
//
// Marshals using a map (not a typed struct) so unrelated future-added
// fields pass through unchanged — the migration must not silently drop
// keys it doesn't recognize.
func patchSnapshotJSONBytes(raw []byte) ([]byte, bool, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, false, err
	}
	rawReleased, ok := m["released"]
	if !ok {
		return raw, false, nil
	}
	delete(m, "released")

	// release_date: nil or "YYYY-MM-DD"; release_date_tba: bool.
	var dateStr *string
	var tba bool
	if s, isStr := rawReleased.(string); isStr {
		d, t := model.ParseLegacyReleased(s)
		tba = t
		if d != nil {
			ds := d.UTC().Format("2006-01-02")
			dateStr = &ds
		}
	}
	if dateStr == nil {
		m["release_date"] = nil
	} else {
		m["release_date"] = *dateStr
	}
	m["release_date_tba"] = tba

	out, err := json.Marshal(m)
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}

func formatDate(t *time.Time) string {
	if t == nil {
		return "<nil>"
	}
	return t.UTC().Format("2006-01-02")
}
