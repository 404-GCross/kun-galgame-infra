package wikirescue

import (
	"context"
	"fmt"
	"time"

	"api/internal/platform/catalog/repository"

	"gorm.io/gorm"
)

// parkedScreenshot is one user-uploaded screenshot whose work never got an
// anchor. The image_hash is the whole payload — the bytes stay in the image
// service, this record is how to find them again.
type parkedScreenshot struct {
	GalgameID int64  `json:"galgame_id"`
	ImageHash string `json:"image_hash"`
	SortOrder int    `json:"sort_order"`
	Caption   string `json:"caption"`
	Sexual    int16  `json:"sexual"`
	Violence  int16  `json:"violence"`
}

// stepScreenshot rescues the non-VNDB screenshots (charter ruling 3). VNDB
// screenshots are regenerable by sync-vndb-screenshots and are discarded with
// the table; the rest are user uploads with no upstream, so they project onto
// catalog_work_screenshot verbatim.
//
// BYTE OWNERSHIP CAVEAT: catalog_work_screenshot's contract says its bytes live
// in the CATALOG image scope, but these rows carry hashes whose bytes live in
// the galgame_wiki scope. That mismatch is the charter's hard-bone #2 (image-byte
// retirement, wave W2) and is deliberately NOT resolved here — this step moves
// the database rows only.
func (r *Runner) stepScreenshot(ctx context.Context) (Stats, error) {
	st := Stats{Step: "b"}

	type shot struct {
		GalgameID int64
		ImageHash string
		SortOrder int
		Caption   string
		Sexual    int16
		Violence  int16
	}
	var shots []shot
	if err := r.galgame.WithContext(ctx).Raw(
		`SELECT galgame_id, btrim(image_hash) AS image_hash, sort_order,
		        coalesce(caption, '') AS caption, sexual, violence
		 FROM galgame_screenshot
		 WHERE coalesce(source, '') <> 'vndb'
		 ORDER BY galgame_id, sort_order`).Scan(&shots).Error; err != nil {
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
		rows = append(rows, []any{workID, s.ImageHash, s.SortOrder, s.Caption, s.Sexual, s.Violence, r.wikiSrc, now, now})
	}
	st.Anchored = len(shots) - len(parked)
	st.Parked = len(parked)
	st.Planned = len(rows)

	if err := r.park("b-screenshots", parked); err != nil {
		return st, err
	}
	if !r.opts.Apply {
		return st, nil
	}

	err = r.catalog.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		landed, err := insertReturning(tx, "catalog_work_screenshot",
			[]string{"work_id", "image_hash", "sort_order", "caption", "sexual", "violence", "source_id", "created_at", "updated_at"},
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
