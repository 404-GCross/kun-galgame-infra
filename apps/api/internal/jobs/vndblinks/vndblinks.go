// Package vndblinks is the shared logic behind the sync-vndb-links CLI and the
// scheduled "sync-vndb-links" job: reconcile curated VNDB store/official links
// (source="vndb") onto published galgames, idempotently, via
// service.ReconcileVndbLinks (approach B — rewrite galgame_link + jsonb-patch the
// latest revision snapshot, no new revision). The CLI and the scheduler run
// identical code from here, matching the cmd/sync-vndb → internal/jobs/vndbsync
// pattern.
package vndblinks

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"api/internal/infrastructure/database"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/service"
	"api/internal/platform/galgame/vndb"
	"api/pkg/config"

	"gorm.io/gorm"
)

const batchSize = 100

// Opts selects which published galgames get their VNDB links reconciled.
type Opts struct {
	Apply       bool          // false = dry run (fetch + diff, no writes)
	Gap         time.Duration // min delay between VNDB API calls (default 2s)
	OnlyMissing bool          // only games with no source="vndb" link yet (new/claimed) — cheap
	IDs         []int         // process exactly these ids (any status); overrides OnlyMissing/Limit/Offset
	Limit       int           // 0 = no limit
	Offset      int           // skip this many (for chunking a full pass)
	Samples     int           // per-game link previews to log
}

// Run reconciles VNDB links for the selected galgames and returns a summary
// ({candidates, processed, changed, failed}). apply=false fetches + diffs but
// writes nothing.
func Run(ctx context.Context, cfg *config.Config, opts Opts) (map[string]any, error) {
	if opts.Gap <= 0 {
		opts.Gap = 2 * time.Second
	}
	db, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		return nil, fmt.Errorf("connect galgame db (%s): %w", cfg.GalgameDatabase.DBName, err)
	}
	defer db.Close()

	cands, err := candidates(db.DB(), opts)
	if err != nil {
		return nil, fmt.Errorf("list candidates: %w", err)
	}
	slog.Info("sync-vndb-links start", "candidates", len(cands), "batch", batchSize,
		"apply", opts.Apply, "only_missing", opts.OnlyMissing, "gap", opts.Gap.String())

	vc := vndb.New(opts.Gap)
	var changed, failed, shown int
	for start := 0; start < len(cands); start += batchSize {
		if err := ctx.Err(); err != nil {
			return summary(len(cands), start, changed, failed), err
		}
		end := min(start+batchSize, len(cands))
		batch := cands[start:end]

		ids := make([]string, 0, len(batch))
		for _, c := range batch {
			ids = append(ids, c.VNDBID)
		}
		linksByVNDB, err := vc.FetchGameLinksBatch(ids)
		if err != nil {
			failed += len(batch)
			slog.Error("fetch batch", "from", batch[0].ID, "to", batch[len(batch)-1].ID, "error", err)
			continue
		}
		for _, c := range batch {
			fresh := linksByVNDB[c.VNDBID]
			didChange, err := service.ReconcileVndbLinks(ctx, db.DB(), c.ID, fresh, opts.Apply)
			if err != nil {
				failed++
				slog.Error("reconcile galgame", "id", c.ID, "error", err)
				continue
			}
			if didChange {
				changed++
				if shown < opts.Samples {
					shown++
					printPreview(c.ID, fresh)
				}
			}
		}
		slog.Info("progress", "processed", end, "of", len(cands), "changed", changed, "failed", failed)
	}

	slog.Info("sync-vndb-links done", "processed", len(cands), "changed", changed, "failed", failed, "applied", opts.Apply)
	if !opts.Apply {
		slog.Info("DRY RUN — nothing written; re-run with Apply")
	}
	return summary(len(cands), len(cands), changed, failed), nil
}

type candidate struct {
	ID     int
	VNDBID string `gorm:"column:vndb_id"`
}

func candidates(db *gorm.DB, opts Opts) ([]candidate, error) {
	q := db.Model(&model.Galgame{}).
		Select("id", "vndb_id").
		Where("vndb_id ~ '^v[0-9]+$'").
		Order("id")
	switch {
	case len(opts.IDs) > 0:
		// Targeted: exactly these ids, any status.
		q = q.Where("id IN ?", opts.IDs)
	default:
		q = q.Where("status = 0")
		if opts.OnlyMissing {
			// Only games not yet enriched (new / freshly-claimed) — the cheap daily pass.
			q = q.Where("NOT EXISTS (SELECT 1 FROM galgame_link l WHERE l.galgame_id = galgame.id AND l.source = 'vndb')")
		}
		q = q.Offset(opts.Offset)
		if opts.Limit > 0 {
			q = q.Limit(opts.Limit)
		}
	}
	var cands []candidate
	return cands, q.Scan(&cands).Error
}

func summary(candidates, processed, changed, failed int) map[string]any {
	return map[string]any{
		"candidates": candidates,
		"processed":  processed,
		"changed":    changed,
		"failed":     failed,
	}
}

func printPreview(id int, links []model.SnapshotLink) {
	slog.Info("would set vndb links", "galgame", id, "count", len(links))
	for _, l := range links {
		slog.Info("  link", "site", l.SourceKey, "name", l.Name, "url", l.Link)
	}
}
