package wikirescue

import (
	"context"
	"fmt"
	"time"

	"api/internal/platform/catalog/repository"

	"gorm.io/gorm"
)

// parkedCover is one galgame_cover row whose work never got an anchor, kept in
// full so the reference is recoverable after the table is dropped. Unlike W0's
// parked artifacts this one is expected to stay EMPTY: W2 runs after the claim
// reconciler, so every wiki row should project.
type parkedCover struct {
	GalgameID      int64  `json:"galgame_id"`
	ImageHash      string `json:"image_hash"`
	SortOrder      int    `json:"sort_order"`
	Kind           string `json:"kind"`
	PortraitPinned bool   `json:"portrait_pinned"`
	Sexual         int16  `json:"sexual"`
	Violence       int16  `json:"violence"`
	Source         string `json:"source"`
}

// stepCoverProject projects the WHOLE galgame_cover table into
// catalog_work_cover (W2, refs/plans/10-data-layer-retirement/01-w2-image-bytes.md).
//
// W0 rescued only the rows with no upstream to regenerate them from. W2 projects
// everything, for a different reason: galgame_cover is one of the two tables
// keeping 174,843 image-service hashes referenced, and reference is what keeps
// the BYTES alive. The image GC soft-deletes anything unreferenced for 365 days,
// and the only thing pinging these hashes is a sweep that reads the wiki tables.
// Drop the tables with no catalog-side rows in place and the fuse relights. The
// rows are cheap to re-project from VNDB; the bytes are not re-uploadable at all
// (the hash keys the ORIGINAL upload, whose bytes no longer exist — only the
// transcoded webp does), so the projection is the byte rescue.
//
// Fill-missing on (work_id, image_hash), like every step here: the 6,560 native
// cover rows that already exist were measured to have ZERO overlap with the wiki
// set, so this is a pure fill, but ON CONFLICT DO NOTHING keeps the guarantee
// rather than the measurement.
//
// The claimed read face is deliberately untouched: covers keep the whole-facet
// XOR, so a claimed work still reads galgame_cover through the bridge and these
// native rows stay invisible until W1 retires the bridge. That is the hand-off
// mechanism, not a defect — the rows are in place BEFORE they are needed.
//
// created_at/updated_at are the run's own clock, not galgame_cover.created (which
// is non-null on all 110,881 rows and therefore was a real option). The catalog
// column means "when this catalog row came to exist", the same as every other
// facet writer in the repo, and updated_at drives the changes feed — backdating
// it would file a brand-new row behind the feed's watermark and hide the whole
// wave from incremental consumers. The wiki timestamp survives in the W1 archival
// dump, which is where provenance of the dropped table belongs.
func (r *Runner) stepCoverProject(ctx context.Context) (Stats, error) {
	st := Stats{Step: "m"}

	srcIDs, err := r.mediaSourceIDs(ctx)
	if err != nil {
		return st, err
	}

	type cover struct {
		GalgameID      int64
		ImageHash      string
		SortOrder      int
		Kind           string
		PortraitPinned bool
		Sexual         int16
		Violence       int16
		Source         string
	}
	var covers []cover
	if err := r.galgame.WithContext(ctx).Raw(
		`SELECT galgame_id, btrim(image_hash) AS image_hash, sort_order,
		        coalesce(kind, '') AS kind, portrait_pinned, sexual, violence,
		        coalesce(source, '') AS source
		 FROM galgame_cover
		 ORDER BY galgame_id, sort_order, image_hash`).Scan(&covers).Error; err != nil {
		return st, fmt.Errorf("read galgame_cover: %w", err)
	}
	st.Source = len(covers)

	anchors, err := r.anchorMap(ctx)
	if err != nil {
		return st, err
	}

	now := time.Now().UTC()
	rows := make([][]any, 0, len(covers))
	parked := make([]parkedCover, 0)
	// Two wiki works can share one catalog work, so the batch itself can carry a
	// duplicate (work_id, image_hash). The read ordering above puts the pinned
	// cover (sort_order 0) first, so first-wins keeps the pin.
	seen := make(map[[2]any]struct{}, len(covers))
	for _, c := range covers {
		workID, ok := anchors[c.GalgameID]
		if !ok {
			parked = append(parked, parkedCover{
				GalgameID: c.GalgameID, ImageHash: c.ImageHash, SortOrder: c.SortOrder,
				Kind: c.Kind, PortraitPinned: c.PortraitPinned,
				Sexual: c.Sexual, Violence: c.Violence, Source: c.Source,
			})
			continue
		}
		key := [2]any{workID, c.ImageHash}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		rows = append(rows, []any{
			workID, c.ImageHash, c.SortOrder, c.Kind, c.PortraitPinned,
			c.Sexual, c.Violence, mediaSourceID(srcIDs, c.Source), now, now,
		})
	}
	st.Anchored = len(covers) - len(parked)
	st.Parked = len(parked)
	st.Planned = len(rows)

	if err := r.park("m-covers", parked); err != nil {
		return st, err
	}
	if !r.opts.Apply {
		return st, nil
	}

	err = r.catalog.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		landed, err := insertReturning(tx, "catalog_work_cover",
			[]string{"work_id", "image_hash", "sort_order", "kind", "portrait_pinned",
				"sexual", "violence", "source_id", "created_at", "updated_at"},
			"work_id", rows)
		if err != nil {
			return err
		}
		st.Written = len(landed)
		if len(landed) > 0 {
			if err := repository.TouchWorks(ctx, tx, landed); err != nil {
				return fmt.Errorf("touch works: %w", err)
			}
			st.Touched = distinctCount(landed)
		}
		return nil
	})
	return st, err
}
