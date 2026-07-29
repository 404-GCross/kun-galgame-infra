// Package vndbenrich is the shared logic behind the sync-vndb-enrich CLI and the
// scheduled "sync-vndb-enrich" job: reconcile a published galgame's VNDB-sourced
// links + tags + developers(officials) from ONE combined fetch per 100 VNs (one
// /vn for tags+developers + paginated /release for store links), via the
// approach-B reconcilers in the service package. The source="vndb" subset is
// sync-managed and reconciled idempotently; user-curated links/tags/officials
// are preserved. CLI and scheduler run identical code from here (the cmd/sync-vndb
// → internal/jobs/vndbsync pattern), replacing the retired sync-vndb-relations
// bypass and the links-only Phase 1 job.
package vndbenrich

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"api/internal/infrastructure/database"
	"api/internal/jobs/galgametouch"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/service"
	"api/internal/platform/galgame/vndb"
	"api/internal/platform/galgame/vndbresolve"
	"api/pkg/config"

	"gorm.io/gorm"
)

const batchSize = 100

// Opts selects which published galgames get reconciled.
type Opts struct {
	Apply       bool          // false = dry run (fetch + diff, no writes; resolves lookup-only)
	Gap         time.Duration // min delay between VNDB API calls (default 2s)
	OnlyMissing bool          // only games with no source="vndb" link yet (new/claimed) — cheap
	IDs         []int         // process exactly these ids (any status); overrides OnlyMissing/Limit/Offset
	Limit       int
	Offset      int
	TagMapPath  string // defaults to vndbresolve.DefaultTagMapPath()
}

// Run reconciles links + tags + officials for the selected galgames and returns
// a summary. apply=false fetches + diffs but writes nothing (and resolves
// tags/officials lookup-only, so a preview never creates wiki entities).
func Run(ctx context.Context, cfg *config.Config, opts Opts) (map[string]any, error) {
	if opts.Gap <= 0 {
		opts.Gap = 2 * time.Second
	}
	if opts.TagMapPath == "" {
		opts.TagMapPath = vndbresolve.DefaultTagMapPath()
	}
	tagMap, err := vndbresolve.ParseTagMap(opts.TagMapPath)
	if err != nil {
		return nil, fmt.Errorf("parse tagMap %q: %w", opts.TagMapPath, err)
	}
	if len(tagMap) < 100 {
		return nil, fmt.Errorf("tagMap too small (%d entries), check path %q", len(tagMap), opts.TagMapPath)
	}

	db, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		return nil, fmt.Errorf("connect galgame db (%s): %w", cfg.GalgameDatabase.DBName, err)
	}
	defer db.Close()

	// Reconciling links/tags/officials moves a claimed work's public body, so the
	// changes feed has to learn about it. Opened only when applying — a dry run
	// keeps a nil Toucher, which is a no-op, so a preview can never stamp a work
	// even though reconcileOne still reports the diffs it WOULD write.
	var toucher *galgametouch.Toucher
	if opts.Apply {
		toucher, err = galgametouch.Open(cfg)
		if err != nil {
			return nil, err
		}
		defer toucher.Close()
	}

	resolver := vndbresolve.New(tagMap)
	if err := resolver.LoadCaches(db.DB()); err != nil {
		return nil, fmt.Errorf("load tag/official caches: %w", err)
	}

	cands, err := candidates(db.DB(), opts)
	if err != nil {
		return nil, fmt.Errorf("list candidates: %w", err)
	}
	slog.Info("sync-vndb-enrich start", "candidates", len(cands), "batch", batchSize,
		"apply", opts.Apply, "only_missing", opts.OnlyMissing, "gap", opts.Gap.String())

	vc := vndb.New(opts.Gap)
	var changed, failed int
	for start := 0; start < len(cands); start += batchSize {
		if err := ctx.Err(); err != nil {
			return summary(len(cands), start, changed, failed, toucher.Count(), resolver), err
		}
		end := min(start+batchSize, len(cands))
		batch := cands[start:end]

		ids := make([]string, 0, len(batch))
		for _, c := range batch {
			ids = append(ids, c.VNDBID)
		}
		// One batched /release + one /vn per 100 VNs. On error (VNDB down, or a
		// rare out-of-range vndb_id), skip the batch and retry next run — the
		// failed count + log surface it; we don't hammer the API per-id.
		links, err := vc.FetchGameLinksBatch(ctx, ids)
		if err != nil {
			failed += len(batch)
			slog.Error("fetch links batch", "from", batch[0].ID, "to", batch[len(batch)-1].ID, "error", err)
			continue
		}
		meta, err := vc.FetchVNMetaBatch(ctx, ids)
		if err != nil {
			failed += len(batch)
			slog.Error("fetch meta batch", "from", batch[0].ID, "to", batch[len(batch)-1].ID, "error", err)
			continue
		}

		// Only galgames whose reconcile produced ≥1 real write reach the touch set:
		// a zero-diff pass (the steady state for an already-enriched game) must
		// leave the feed alone. A failed reconcile never lands here either.
		var changedIDs []int
		for _, c := range batch {
			didChange, err := reconcileOne(ctx, db.DB(), resolver, c.ID, links[c.VNDBID], meta[c.VNDBID], opts.Apply)
			if err != nil {
				failed++
				slog.Error("reconcile galgame", "id", c.ID, "error", err)
				continue
			}
			if didChange {
				changed++
				changedIDs = append(changedIDs, c.ID)
			}
		}
		// Batch-level stamping: the writes above are already committed, so an
		// interrupted run keeps the watermarks it has earned so far. A failure
		// here is loud but not fatal — the reconciled data itself is sound.
		if err := toucher.Touch(ctx, changedIDs); err != nil {
			slog.Error("touch claimed works", "from", batch[0].ID, "to", batch[len(batch)-1].ID, "error", err)
		}
		slog.Info("progress", "processed", end, "of", len(cands), "changed", changed,
			"failed", failed, "touched", toucher.Count())
	}

	slog.Info("sync-vndb-enrich done", "processed", len(cands), "changed", changed, "failed", failed,
		"touched", toucher.Count(), "new_tags", resolver.NewTags(), "new_officials", resolver.NewOfficials(),
		"applied", opts.Apply)
	if !opts.Apply {
		slog.Info("DRY RUN — nothing written; re-run with Apply")
	}
	return summary(len(cands), len(cands), changed, failed, toucher.Count(), resolver), nil
}

// reconcileOne reconciles one galgame's links, tags and officials. Returns true
// if any of the three changed.
func reconcileOne(ctx context.Context, db *gorm.DB, resolver *vndbresolve.Resolver, galgameID int, links []model.SnapshotLink, meta vndb.VNMeta, apply bool) (bool, error) {
	// Resolve VNDB tags/developers to wiki ids. A resolution ERROR (genuine
	// create failure) aborts this galgame — never reconcile a PARTIAL desired
	// set, or ReconcileVndb* would delete the relations that failed to resolve.
	desiredTags := make(map[int]int, len(meta.Tags)) // wiki tag_id → spoiler
	for _, t := range meta.Tags {
		rt := vndbresolve.Tag{ID: t.ID, Name: t.Name, Category: t.Category, Rating: t.Rating, Spoiler: t.Spoiler, Lie: t.Lie}
		if vndbresolve.TagSkipped(rt) {
			continue
		}
		id, err := resolveTagID(db, resolver, rt, apply)
		if err != nil {
			return false, fmt.Errorf("resolve tag %q: %w", rt.Name, err)
		}
		if id != 0 { // 0 = dry-run lookup miss (tag not yet created) — skip from preview
			desiredTags[id] = t.Spoiler
		}
	}
	desiredOfficials := make([]int, 0, len(meta.Developers))
	for _, d := range meta.Developers {
		rd := vndbresolve.Developer{ID: d.ID, Name: d.Name, Original: d.Original, Type: d.Type, Lang: d.Lang}
		id, err := resolveOfficialID(db, resolver, rd, apply)
		if err != nil {
			return false, fmt.Errorf("resolve official %q: %w", rd.Name, err)
		}
		if id != 0 {
			desiredOfficials = append(desiredOfficials, id)
		}
	}

	changedL, err := service.ReconcileVndbLinks(ctx, db, galgameID, links, apply)
	if err != nil {
		return false, fmt.Errorf("links: %w", err)
	}
	changedT, err := service.ReconcileVndbTags(ctx, db, galgameID, desiredTags, apply)
	if err != nil {
		return false, fmt.Errorf("tags: %w", err)
	}
	changedO, err := service.ReconcileVndbOfficials(ctx, db, galgameID, desiredOfficials, apply)
	if err != nil {
		return false, fmt.Errorf("officials: %w", err)
	}
	return changedL || changedT || changedO, nil
}

// resolveTagID creates-if-missing on apply, lookup-only on dry-run (so a preview
// never writes new wiki tags). Dry-run returns (0, nil) for an as-yet-uncreated
// tag — not an error.
func resolveTagID(db *gorm.DB, resolver *vndbresolve.Resolver, t vndbresolve.Tag, apply bool) (int, error) {
	if apply {
		return resolver.ResolveTag(db, t)
	}
	id, _ := resolver.LookupTag(t)
	return id, nil
}

func resolveOfficialID(db *gorm.DB, resolver *vndbresolve.Resolver, d vndbresolve.Developer, apply bool) (int, error) {
	if apply {
		return resolver.ResolveOfficial(db, d)
	}
	id, _ := resolver.LookupOfficial(d)
	return id, nil
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
		q = q.Where("id IN ?", opts.IDs)
	default:
		q = q.Where("status = 0")
		if opts.OnlyMissing {
			// "Never enriched" = no source="vndb" link yet (every enriched game
			// gets at least the vndb.org self-link, so this converges).
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

func summary(candidates, processed, changed, failed, touched int, r *vndbresolve.Resolver) map[string]any {
	return map[string]any{
		"candidates":    candidates,
		"processed":     processed,
		"changed":       changed,
		"failed":        failed,
		"touched":       touched,
		"new_tags":      r.NewTags(),
		"new_officials": r.NewOfficials(),
	}
}
