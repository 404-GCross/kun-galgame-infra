package wikirescue

import (
	"context"
	"fmt"
	"time"

	"api/internal/platform/catalog/repository"

	"gorm.io/gorm"
)

// stepScreenshotProject projects the WHOLE galgame_screenshot table into
// catalog_work_screenshot (W2, refs/plans/10-data-layer-retirement/01-w2-image-bytes.md).
//
// Step "b" already rescued the 528 non-VNDB rows, because those were the ones
// with no upstream. This step adds the other 66,863 — the VNDB screenshots —
// whose ROWS are indeed regenerable but whose BYTES are not: the June migration
// pulled them into the image service, so they are hashes like any other, and
// galgame_screenshot is their last reference. See stepCoverProject for the full
// argument; the two steps are the same rescue on the two byte-bearing tables.
//
// The step-b rows are re-planned here and skipped by the (work_id, image_hash)
// unique key. A row whose wiki source string maps to a different catalog_source
// than step b stamped (b filed everything under galgame_wiki) keeps the EXISTING
// attribution — ON CONFLICT DO NOTHING never overwrites — which is the right
// outcome: the landed row is the one the read face has been serving.
//
// Unlike covers, this facet's read face already unions bridge and native lanes
// (refs/proj/125), so these rows become visible to claimed works the moment they
// land. That is why this wave also dedupes the union by (work_id, image_hash)
// with the bridge leading: without it every projected screenshot would show up
// twice until W1 retires the bridge. See loadWorkScreenshots.
func (r *Runner) stepScreenshotProject(ctx context.Context) (Stats, error) {
	st := Stats{Step: "n"}

	srcIDs, err := r.mediaSourceIDs(ctx)
	if err != nil {
		return st, err
	}

	type shot struct {
		GalgameID int64
		ImageHash string
		SortOrder int
		Caption   string
		Sexual    int16
		Violence  int16
		Source    string
	}
	var shots []shot
	if err := r.galgame.WithContext(ctx).Raw(
		`SELECT galgame_id, btrim(image_hash) AS image_hash, sort_order,
		        coalesce(caption, '') AS caption, sexual, violence,
		        coalesce(source, '') AS source
		 FROM galgame_screenshot
		 ORDER BY galgame_id, sort_order, image_hash`).Scan(&shots).Error; err != nil {
		return st, fmt.Errorf("read galgame_screenshot: %w", err)
	}
	st.Source = len(shots)

	anchors, err := r.anchorMap(ctx)
	if err != nil {
		return st, err
	}

	now := time.Now().UTC()
	rows := make([][]any, 0, len(shots))
	parked := make([]parkedScreenshot, 0)
	seen := make(map[[2]any]struct{}, len(shots))
	for _, s := range shots {
		workID, ok := anchors[s.GalgameID]
		if !ok {
			parked = append(parked, parkedScreenshot{
				GalgameID: s.GalgameID, ImageHash: s.ImageHash, SortOrder: s.SortOrder,
				Caption: s.Caption, Sexual: s.Sexual, Violence: s.Violence,
			})
			continue
		}
		key := [2]any{workID, s.ImageHash}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		rows = append(rows, []any{
			workID, s.ImageHash, s.SortOrder, s.Caption, s.Sexual, s.Violence,
			mediaSourceID(srcIDs, s.Source), now, now,
		})
	}
	st.Anchored = len(shots) - len(parked)
	st.Parked = len(parked)
	st.Planned = len(rows)

	if err := r.park("n-screenshots", parked); err != nil {
		return st, err
	}
	if !r.opts.Apply {
		return st, nil
	}

	err = r.catalog.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		landed, err := insertReturning(tx, "catalog_work_screenshot",
			[]string{"work_id", "image_hash", "sort_order", "caption", "sexual", "violence",
				"source_id", "created_at", "updated_at"},
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
