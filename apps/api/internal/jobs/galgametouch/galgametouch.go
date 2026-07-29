// Package galgametouch stamps galgame-family writes onto the catalog changes
// feed (refs/proj/119).
//
// A wiki galgame's body lives in the galgame_* tables, but its identity — and
// the watermark GET /v1/catalog/changes walks — lives on the claimed
// catalog_work row. The daily sync-vndb-* writers only ever touched the
// galgame side, so every cover, screenshot, tag and score they landed was
// invisible to downstream mirrors. This helper closes that gap: a writer hands
// over the galgame ids it REALLY changed, and the matching claimed works get
// their updated_at bumped.
//
// Two rules the callers depend on:
//
//   - The mapping runs on the CATALOG pool, never on the galgame handle. The
//     two families sharing one physical database is a deployment posture, not
//     a code contract, and catalog_work belongs to the catalog domain.
//   - A nil *Toucher is a working no-op. Dry runs simply never open one, so
//     "Apply=false touches nothing" is structural rather than a branch every
//     call site has to remember.
package galgametouch

import (
	"context"
	"fmt"

	"api/internal/infrastructure/database"
	"api/internal/platform/catalog/repository"
	"api/pkg/config"

	"gorm.io/gorm"
)

// siteGalgameWiki is the ecosystem TENANT key a wiki-claimed work carries in
// catalog_work.site — NOT the medium key "galgame" (mediumGalgame in
// catalogsync). Getting the tenant context wrong here fails silently: the
// mapping would match nothing and every touch would quietly become a no-op.
const siteGalgameWiki = "galgame_wiki"

// mapChunk bounds one mapping SELECT's bind parameters; galgame ids are its
// only arguments, so this stays far under Postgres' 65,535 limit.
const mapChunk = 2000

// Toucher resolves galgame ids to their claimed catalog_work rows and bumps
// them. Not safe for concurrent use — callers collect per batch and flush from
// the batch loop.
type Toucher struct {
	conn    *database.PostgresDB // non-nil only when Open dialed the pool
	db      *gorm.DB
	touched int
}

// Open dials cfg.CatalogDatabase for a run's touch bookkeeping. Callers open it
// ONLY when applying, so a dry run holds a nil *Toucher and cannot write.
func Open(cfg *config.Config) (*Toucher, error) {
	conn, err := database.NewPostgresDB(cfg.CatalogDatabase)
	if err != nil {
		return nil, fmt.Errorf("connect catalog db (%s): %w", cfg.CatalogDatabase.DBName, err)
	}
	return &Toucher{conn: conn, db: conn.DB()}, nil
}

// New wraps an already-open catalog pool (tests).
func New(db *gorm.DB) *Toucher { return &Toucher{db: db} }

// Close releases the pool Open dialed.
func (t *Toucher) Close() {
	if t == nil || t.conn == nil {
		return
	}
	_ = t.conn.Close()
}

// Count is how many claimed works this run has stamped — the `touched` summary
// figure, and the first real measurement of what these jobs add to the feed's
// daily volume.
func (t *Toucher) Count() int {
	if t == nil {
		return 0
	}
	return t.touched
}

// Touch bumps the claimed work behind each of the given galgames. Callers must
// pass ONLY galgames they actually changed — an idempotent re-run that writes
// nothing must move no watermark, or 62k claimed works would flood the feed
// every night. Galgames with no claim (a fresh sync-vndb draft, say) are simply
// absent from the mapping: no touch, no error.
func (t *Toucher) Touch(ctx context.Context, galgameIDs []int) error {
	if t == nil || len(galgameIDs) == 0 {
		return nil
	}
	workIDs, err := t.resolve(ctx, galgameIDs)
	if err != nil {
		return err
	}
	if err := repository.TouchWorks(ctx, t.db, workIDs); err != nil {
		return fmt.Errorf("touch claimed works: %w", err)
	}
	t.touched += len(workIDs)
	return nil
}

// resolve maps galgame ids to claimed catalog_work ids. deleted_at IS NULL
// mirrors the changes feed's own universe (public_works_list.go Changes) — a
// soft-deleted work is never served, so bumping it would be pure noise. The
// (medium, site, product_work_id) unique key makes this at most one work per
// galgame, so the deduped input bounds the returned set.
func (t *Toucher) resolve(ctx context.Context, galgameIDs []int) ([]int64, error) {
	seen := make(map[int64]struct{}, len(galgameIDs))
	ids := make([]int64, 0, len(galgameIDs))
	for _, id := range galgameIDs {
		if id <= 0 {
			continue
		}
		v := int64(id)
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		ids = append(ids, v)
	}
	var workIDs []int64
	for start := 0; start < len(ids); start += mapChunk {
		end := min(start+mapChunk, len(ids))
		var page []int64
		if err := t.db.WithContext(ctx).Raw(
			`SELECT id FROM catalog_work
			 WHERE site = ? AND product_work_id IN (?) AND deleted_at IS NULL`,
			siteGalgameWiki, ids[start:end]).Scan(&page).Error; err != nil {
			return nil, fmt.Errorf("map galgame ids to claimed works: %w", err)
		}
		workIDs = append(workIDs, page...)
	}
	return workIDs, nil
}
