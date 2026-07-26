package service

// Platform read face (step 96, refs/proj/96). A work's explicit platform rows
// live in catalog_work_platform — the bgm lane's landing surface (Bangumi
// carries platforms on the SUBJECT and its bodyless works mostly have no
// releases). The vndb/dlsite faces keep platforms on RELEASES
// (catalog_release.platform + extra.platforms); consumers union the two
// grains as needed. Catalog-native — no claimed bridge.

import (
	"context"
)

// WorkPlatformRow is one explicit work-level platform assertion, carrying its
// provenance (source_id, §8.C). Platform is a catalog_platform registry code.
type WorkPlatformRow struct {
	Platform string `gorm:"column:platform"`
	SourceID int16  `gorm:"column:source_id"`
}

// loadWorkPlatforms reads the explicit platform rows for a set of works in ONE
// query, ordered (work, platform) for determinism. A work with no rows is
// absent from the map (the caller renders []).
func (s *ReadService) loadWorkPlatforms(ctx context.Context, subjects []claimSubject) (map[int64][]WorkPlatformRow, error) {
	out := make(map[int64][]WorkPlatformRow, len(subjects))
	if len(subjects) == 0 {
		return out, nil
	}
	workIDs := make([]int64, 0, len(subjects))
	for _, sub := range subjects {
		workIDs = append(workIDs, sub.WorkID)
	}
	var rows []struct {
		WorkID   int64  `gorm:"column:work_id"`
		Platform string `gorm:"column:platform"`
		SourceID int16  `gorm:"column:source_id"`
	}
	if err := s.db.WithContext(ctx).Raw(`
		SELECT work_id, platform, source_id
		FROM catalog_work_platform
		WHERE work_id IN ?
		ORDER BY work_id, platform, source_id`, workIDs).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.WorkID] = append(out[r.WorkID], WorkPlatformRow{Platform: r.Platform, SourceID: r.SourceID})
	}
	return out, nil
}
