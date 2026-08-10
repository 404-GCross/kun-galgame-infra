package getchumedia

import (
	"context"
	"fmt"
	"path"
	"strings"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

type candidate struct {
	WorkID        int64
	ContentRating int16
	GetchuIDs     []string
}

type candidateRow struct {
	WorkID        int64  `gorm:"column:work_id"`
	ContentRating int16  `gorm:"column:content_rating"`
	GetchuID      string `gorm:"column:getchu_id"`
}

func loadCandidates(ctx context.Context, db *gorm.DB, source, medium int16) ([]candidate, error) {
	var rows []candidateRow
	err := db.WithContext(ctx).Raw(`
		SELECT DISTINCT w.id AS work_id, w.content_rating, r.external_id AS getchu_id
		FROM catalog_work w
		JOIN catalog_release rel ON rel.work_id = w.id AND rel.deleted_at IS NULL
		JOIN catalog_external_ref r ON r.entity_type = ? AND r.entity_id = rel.id
			AND r.source_id = ? AND r.link_kind = ?
		WHERE w.medium_id = ? AND w.deleted_at IS NULL
			AND NOT EXISTS (SELECT 1 FROM catalog_work_screenshot s
				WHERE s.work_id = w.id AND s.source_id = ?)
		ORDER BY w.id, r.external_id`,
		model.EntityTypeRelease, source, model.LinkKindExact, medium, source).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("load candidates: %w", err)
	}

	var out []candidate
	for i := 0; i < len(rows); {
		c := candidate{WorkID: rows[i].WorkID, ContentRating: rows[i].ContentRating}
		for ; i < len(rows) && rows[i].WorkID == c.WorkID; i++ {
			c.GetchuIDs = append(c.GetchuIDs, rows[i].GetchuID)
		}
		out = append(out, c)
	}
	return out, nil
}

type stagedImage struct {
	GetchuID string
	Ordinal  int
	File     string
	SHA256   string
}

func loadSamples(ctx context.Context, gdb *gorm.DB) (map[string][]stagedImage, error) {
	var rows []struct {
		GetchuID string `gorm:"column:getchu_id"`
		Ordinal  int    `gorm:"column:ordinal"`
		URL      string `gorm:"column:url"`
		SHA256   string `gorm:"column:sha256"`
	}
	err := gdb.WithContext(ctx).Raw(`
		SELECT getchu_id, ordinal, url, coalesce(sha256,'') AS sha256 FROM item_images
		WHERE kind = 'sample' AND local_path IS NOT NULL AND url NOT LIKE '%\_s.jpg'
		ORDER BY getchu_id, ordinal`).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("read staging item_images: %w", err)
	}
	out := map[string][]stagedImage{}
	for _, r := range rows {
		out[r.GetchuID] = append(out[r.GetchuID], stagedImage{
			GetchuID: r.GetchuID, Ordinal: r.Ordinal, File: path.Base(r.URL), SHA256: r.SHA256,
		})
	}
	return out, nil
}

func preloadHashes(ctx context.Context, db *gorm.DB, workIDs []int64) (map[int64]map[string]bool, error) {
	out := map[int64]map[string]bool{}
	if len(workIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		WorkID    int64  `gorm:"column:work_id"`
		ImageHash string `gorm:"column:image_hash"`
	}
	if err := db.WithContext(ctx).
		Raw(`SELECT work_id, image_hash FROM catalog_work_screenshot WHERE work_id IN ?`, workIDs).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		set := out[r.WorkID]
		if set == nil {
			set = map[string]bool{}
			out[r.WorkID] = set
		}
		set[r.ImageHash] = true
	}
	return out, nil
}

func mirrorPath(root, getchuID, file string) string {
	return strings.Join([]string{strings.TrimRight(root, "/"), getchuID, file}, "/")
}
