package main

import (
	"log/slog"
	"os"

	"api/internal/infrastructure/database"
	"api/internal/platform/galgame/model"
)

// migrateGalgame applies the galgame (wiki-family) schema. This is the FULL
// migration body of the retired cmd/migrate-galgame (wiki-retirement W5,
// charter ruling 6 — single migration entry point), preserved verbatim:
// AutoMigrate + the guarded raw-SQL section below are idempotent, so the
// per-deploy re-run is a fast no-op. Failure style matches the rest of this
// binary: log + os.Exit(1) (a migration must never half-succeed silently).
func migrateGalgame(db *database.PostgresDB) {
	slog.Info("running galgame wiki migrations...")

	if err := db.AutoMigrate(
		// Independent tables first (no FK to galgame)
		&model.GalgameSeries{},
		&model.GalgameTag{},
		&model.GalgameTagAlias{},
		&model.GalgameOfficial{},
		&model.GalgameOfficialAlias{},
		&model.GalgameEngine{},

		// Core galgame table (FK to galgame_series)
		&model.Galgame{},

		// Tables with FK to galgame
		&model.GalgameAlias{},
		&model.GalgameTagRelation{},
		&model.GalgameOfficialRelation{},
		&model.GalgameEngineRelation{},
		&model.GalgameLink{},
		&model.GalgameCover{},
		&model.GalgameScreenshot{},
		&model.GalgamePR{},
		&model.GalgameRevision{},
		// Polymorphic taxonomy revision (tag/official/engine/series) —
		// see docs/galgame_wiki/09-final-upgrade-plan.md §6.1.
		&model.TaxonomyRevision{},
		&model.GalgameHistory{}, // Legacy, kept for migration
		&model.GalgameContributor{},

		// Migration audit trail (mirrors auth/model.UserMigration). Records
		// (source_db, source_id) → galgame_id mapping for idempotent re-runs
		// of migrate-moyu-galgame and for future scripts that need to reverse-
		// look up legacy ids.
		&model.GalgameMigration{},

		// Submission / review event log. Consumed by:
		//   - admin web UI (queue: WHERE type='submitted'/'edited_pending')
		//   - end users via /messages/mine (approved/declined notifications)
		//   - kungal/moyu cron via /messages/feed
		// Read state is owned by each consumer, NOT this table.
		&model.GalgameMessage{},

		// Bangumi score/rank narrow table (bid-anchored enrichment, kept out
		// of the main galgame table; refreshed by cmd/enrich-bangumi).
		&model.GalgameBangumiMeta{},

		// VNDB rating/votecount narrow table (vndb-anchored enrichment, kept
		// out of the main galgame table; refreshed by cmd/sync-vndb-scores).
		&model.GalgameVNDBMeta{},

		// EG (ErogameScape) median/votecount narrow table (EG-anchored
		// enrichment via the catalog exact work anchor; refreshed by
		// cmd/enrich-eg-scores).
		&model.GalgameEGMeta{},

		// DLsite rating + popularity narrow table (DLsite-anchored enrichment
		// via the catalog exact RELEASE anchor; refreshed by
		// cmd/enrich-dlsite-meta).
		&model.GalgameDlsiteMeta{},

		// Cross-source statistics snapshot table (release-year / score-histogram
		// / yearly-average / coverage aggregates; rebuilt by
		// cmd/build-galgame-stats).
		&model.GalgameStats{},
	); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}

	// Post-AutoMigrate raw SQL:
	//
	// (1) vndb_id partial unique. Originally vndb_id had a plain unique index;
	// once user-submitted entries are allowed without VNDB (vndb_id = ''),
	// plain unique would block N entries from sharing the empty value. The
	// partial index enforces uniqueness only when vndb_id is non-empty.
	//
	// We must remove the previous plain unique BEFORE inserting any
	// vndb_id='' rows. Scan pg_indexes for ANY unique constraint touching
	// only the vndb_id column (whether it was named idx_*, uni_*, or
	// something user-defined) and drop them — except our partial unique,
	// which we then create idempotently.
	const partialUniqueName = "uq_galgame_vndb_id_nonempty"
	type indexRow struct {
		IndexName string `gorm:"column:indexname"`
	}
	var stale []indexRow
	if err := db.DB().Raw(`
		SELECT i.indexname
		FROM pg_indexes i
		JOIN pg_class c ON c.relname = i.indexname
		JOIN pg_index x ON x.indexrelid = c.oid
		JOIN pg_attribute a ON a.attrelid = x.indrelid AND a.attnum = ANY(x.indkey)
		WHERE i.schemaname = 'public'
		  AND i.tablename = 'galgame'
		  AND x.indisunique
		  AND array_length(x.indkey, 1) = 1
		  AND a.attname = 'vndb_id'
		  AND i.indexname <> ?
	`, partialUniqueName).Scan(&stale).Error; err != nil {
		slog.Error("scan stale vndb_id unique indexes", "error", err)
		os.Exit(1)
	}
	for _, idx := range stale {
		stmt := `DROP INDEX IF EXISTS "` + idx.IndexName + `"`
		slog.Info("dropping legacy unique index on vndb_id", "name", idx.IndexName)
		if err := db.DB().Exec(stmt).Error; err != nil {
			slog.Error("drop stale unique index failed", "stmt", stmt, "error", err)
			os.Exit(1)
		}
	}
	if err := db.DB().Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS ` + partialUniqueName + `
		    ON galgame(vndb_id) WHERE vndb_id <> ''
	`).Error; err != nil {
		slog.Error("create partial unique on vndb_id failed", "error", err)
		os.Exit(1)
	}

	// (2) galgame_revision.action CHECK. GORM's AutoMigrate creates this
	// constraint from the model tag only on a fresh table; it NEVER alters
	// an existing one. The submission/claim/admin-review flows added new
	// action values (claimed/edited_pending/approved/banned/unbanned/
	// status_changed) on top of the original created/updated/merged/
	// reverted/declined set, so existing wiki DBs still carry the stale
	// 5-value constraint and every POST /galgame/:gid/claim 23514s.
	//
	// Drop-then-add by the GORM default name. Idempotent: DROP IF EXISTS
	// tolerates first run / re-run; the new set is a strict superset of
	// the old so ADD CONSTRAINT validates all existing rows. Keep this
	// list in sync with the model tag in galgame/model/pr.go.
	if err := db.DB().Exec(`
		ALTER TABLE galgame_revision
		    DROP CONSTRAINT IF EXISTS chk_galgame_revision_action
	`).Error; err != nil {
		slog.Error("drop stale chk_galgame_revision_action failed", "error", err)
		os.Exit(1)
	}
	if err := db.DB().Exec(`
		ALTER TABLE galgame_revision
		    ADD CONSTRAINT chk_galgame_revision_action
		    CHECK (action IN (
		        'created','updated','merged','reverted','declined',
		        'claimed','edited_pending','approved','banned',
		        'unbanned','status_changed'
		    ))
	`).Error; err != nil {
		slog.Error("recreate chk_galgame_revision_action failed", "error", err)
		os.Exit(1)
	}

	// (3) galgame_cover partial unique index. Enforces "at most one
	// pinned cover (sort_order=0) per galgame". Without this, two cover
	// rows could share sort_order=0 and the effective-banner query
	// becomes ambiguous (ORDER BY created ASC silently picks the older
	// one, causing the classic "I pinned a new banner but it didn't
	// take effect" bug). The application "pin-new-banner" flow must
	// demote the existing sort_order=0 row in the same transaction.
	if err := db.DB().Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS idx_galgame_cover_pinned
		    ON galgame_cover(galgame_id) WHERE sort_order = 0
	`).Error; err != nil {
		slog.Error("create idx_galgame_cover_pinned failed", "error", err)
		os.Exit(1)
	}

	// (3b) galgame_cover portrait-pin partial unique index. The vertical
	// companion to idx_galgame_cover_pinned: enforces "at most one pinned
	// PORTRAIT cover (portrait_pinned = true) per galgame". The portrait_pinned
	// column itself is added by AutoMigrate (bool NOT NULL DEFAULT false — the
	// DB default backfills existing rows and keeps the out-of-band raw cover
	// inserts, which omit this column, valid). The pin tool
	// (cmd/pin-portrait-covers) demotes the existing portrait_pinned row in the
	// same transaction before promoting the new one, exactly like the
	// sort_order=0 pin flow.
	if err := db.DB().Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS idx_galgame_cover_portrait_pinned
		    ON galgame_cover(galgame_id) WHERE portrait_pinned
	`).Error; err != nil {
		slog.Error("create idx_galgame_cover_portrait_pinned failed", "error", err)
		os.Exit(1)
	}

	// (4) Performance indexes for the wiki tag-listing + tag-count queries.
	// Without them the tag detail page (galgames-by-tag, ORDER BY
	// resource_update_time DESC) parallel-seq-scans + disk-sorts every published
	// galgame per request, dumping GBs of Postgres temp files under load
	// (measured: a single page = ~4.5 MB external-merge sort; tens of GB temp_bytes
	// cumulatively). With these two indexes the common case becomes an
	// index-ordered scan + index-only tag probe (~11 ms + disk → ~0.3 ms, no temp).
	//
	//   - (tag_id, galgame_id): the PK is (galgame_id, tag_id), so filtering /
	//     GROUP BY on tag_id had no usable leading-column index → full scans.
	//   - (status, resource_update_time DESC): serves "WHERE status=0 ORDER BY
	//     resource_update_time DESC" as an index-ordered scan → no disk sort.
	//
	// IF NOT EXISTS = idempotent. CONCURRENTLY is intentionally NOT used here:
	// migrate-catalog is a one-off job (not against live traffic), so a brief lock
	// on these small tables is fine. On an already-live DB, add them manually with
	// CREATE INDEX CONCURRENTLY to avoid locking.
	for _, stmt := range []string{
		`CREATE INDEX IF NOT EXISTS idx_galgame_tag_relation_tag
		    ON galgame_tag_relation(tag_id, galgame_id)`,
		`CREATE INDEX IF NOT EXISTS idx_galgame_status_resource_update
		    ON galgame(status, resource_update_time DESC)`,
	} {
		if err := db.DB().Exec(stmt).Error; err != nil {
			slog.Error("create wiki perf index failed", "stmt", stmt, "error", err)
			os.Exit(1)
		}
	}

	// (Old step 4 — backfill galgame_cover from banner_image_hash —
	// removed in PR5: the legacy column was retired by migrate-drop-
	// banner-image-hash. That migration also patched historical
	// galgame_revision/galgame_pr snapshot jsonb to embed the value
	// into covers[], so no data is lost.)

	// (5) galgame.vndb_id format CHECK. vndb_id must be '' (user-submitted
	// original with no VNDB entry) or a canonical VNDB visual-novel id
	// (`v` + digits). Historically, legacy imports / one-off scripts slipped
	// in VNDB *release* ids (`r12345`) and slash-prefixed ids (`/v46506`).
	// Those bypass the service-layer `^v\d+$` regex (Create/Submit/Update)
	// AND the partial-unique index (exact-match, so format variants don't
	// collide) — producing DUPLICATE galgames for the same VN. This DB-level
	// CHECK is the path-independent backstop: no code path, admin tool,
	// sync/import, or raw INSERT can reintroduce a non-canonical vndb_id.
	//
	// Drop-then-add by a stable name (same idempotent style as step 2). The
	// galgame table was cleaned of all non-canonical rows before this was
	// introduced, so ADD CONSTRAINT validates every existing row. For a
	// 60k-row table the validation scan is sub-second; the brief
	// ACCESS EXCLUSIVE lock is acceptable for this one-off job.
	if err := db.DB().Exec(`
		ALTER TABLE galgame
		    DROP CONSTRAINT IF EXISTS chk_galgame_vndb_id_format
	`).Error; err != nil {
		slog.Error("drop stale chk_galgame_vndb_id_format failed", "error", err)
		os.Exit(1)
	}
	if err := db.DB().Exec(`
		ALTER TABLE galgame
		    ADD CONSTRAINT chk_galgame_vndb_id_format
		    CHECK (vndb_id = '' OR vndb_id ~ '^v[0-9]+$')
	`).Error; err != nil {
		slog.Error("create chk_galgame_vndb_id_format failed", "error", err)
		os.Exit(1)
	}

	// (6) galgame.release_precision CHECK + the release-calendar index.
	// release_precision records how precise release_date is (day/month/year/
	// tba/unknown) — the single source of truth for partial release dates. The
	// column itself is added by AutoMigrate (default 'unknown', which is in the
	// allowed set so ADD CONSTRAINT validates existing rows). Idempotent
	// drop-then-add, same style as steps 2/5. The calendar index serves the
	// month-range query (release_date half-open range) AND covers its ORDER BY
	// (release_date, release_precision, id). The calendar surfaces PUBLISHED (0)
	// games AND unclaimed VNDB DRAFTS (2) so the 新作月表 is comprehensive, so the
	// partial predicate is status IN (0, 2) (excludes banned/pending/declined).
	// Renamed from the old status=0-only idx_galgame_calendar: drop the legacy one
	// + create the superset under a new name = idempotent (vs rebuilding a
	// same-named index every deploy). See docs/galgame_wiki/06-release-calendar-design.md.
	if err := db.DB().Exec(`
		ALTER TABLE galgame
		    DROP CONSTRAINT IF EXISTS chk_galgame_release_precision
	`).Error; err != nil {
		slog.Error("drop stale chk_galgame_release_precision failed", "error", err)
		os.Exit(1)
	}
	if err := db.DB().Exec(`
		ALTER TABLE galgame
		    ADD CONSTRAINT chk_galgame_release_precision
		    CHECK (release_precision IN ('day','month','year','tba','unknown'))
	`).Error; err != nil {
		slog.Error("create chk_galgame_release_precision failed", "error", err)
		os.Exit(1)
	}
	if err := db.DB().Exec(`DROP INDEX IF EXISTS idx_galgame_calendar`).Error; err != nil {
		slog.Error("drop legacy idx_galgame_calendar failed", "error", err)
		os.Exit(1)
	}
	if err := db.DB().Exec(`
		CREATE INDEX IF NOT EXISTS idx_galgame_calendar_pubdraft
		    ON galgame(release_date, release_precision, id) WHERE status IN (0, 2)
	`).Error; err != nil {
		slog.Error("create idx_galgame_calendar_pubdraft failed", "error", err)
		os.Exit(1)
	}

	// (7) galgame (updated, id) index — the changes-stream keyset for the NextMoe
	// open-API public face (GET /v1/galgame/changes, step 02). Serves
	// "ORDER BY updated ASC, id ASC" + the (updated, id) > (?, ?) keyset as an
	// index-ordered scan, so an incremental-sync consumer never triggers a full
	// scan + sort. IF NOT EXISTS = idempotent; not partial (the changes feed
	// filters status/content_limit at read time, both cheap post-index rechecks).
	if err := db.DB().Exec(`
		CREATE INDEX IF NOT EXISTS idx_galgame_updated_id
		    ON galgame(updated, id)
	`).Error; err != nil {
		slog.Error("create idx_galgame_updated_id failed", "error", err)
		os.Exit(1)
	}

	slog.Info("galgame wiki migration completed successfully")
}
