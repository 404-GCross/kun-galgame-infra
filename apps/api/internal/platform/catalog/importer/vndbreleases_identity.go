package importer

import (
	"strings"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

type releaseAnchorItem struct {
	releaseID int64
	rid       string
}

type releaseExactHolder struct {
	releaseID int64
	workID    int64
	retired   bool
}

func (im *Importer) backfillReleaseProbableRefs() (int, error) {
	const where = `
		FROM catalog_release rel
		WHERE rel.extra->>'vndb_id' IS NOT NULL
		  AND NOT EXISTS (
		      SELECT 1 FROM catalog_external_ref r
		      WHERE r.entity_type = ? AND r.entity_id = rel.id AND r.source_id = ?
		  )`

	if im.dryRun {
		var n int64
		if err := im.catalog.Raw(`SELECT count(*)`+where, model.EntityTypeRelease, vndbSource).
			Scan(&n).Error; err != nil {
			return 0, err
		}
		return int(n), nil
	}

	res := im.catalog.Exec(`
		INSERT INTO catalog_external_ref (entity_type, entity_id, source_id, external_id, link_kind, matched_by, created_at)
		SELECT ?::smallint, rel.id, ?::smallint, rel.extra->>'vndb_id', ?::smallint, ?::text, now()`+where+`
		ON CONFLICT DO NOTHING`,
		model.EntityTypeRelease, vndbSource, model.LinkKindProbable, ruleVNDBReleaseBackfill,
		model.EntityTypeRelease, vndbSource)
	if res.Error != nil {
		return 0, res.Error
	}
	return int(res.RowsAffected), nil
}

func (im *Importer) loadReleaseExactHolders() (map[string]releaseExactHolder, error) {
	var rows []struct {
		ExternalID string `gorm:"column:external_id"`
		ReleaseID  int64  `gorm:"column:release_id"`
		WorkID     int64  `gorm:"column:work_id"`
		Retired    bool   `gorm:"column:retired"`
	}
	if err := im.catalog.Raw(`
		SELECT r.external_id, r.entity_id AS release_id,
		       coalesce(rel.work_id, 0) AS work_id,
		       (rel.id IS NULL OR rel.deleted_at IS NOT NULL) AS retired
		FROM catalog_external_ref r
		LEFT JOIN catalog_release rel ON rel.id = r.entity_id
		WHERE r.entity_type = ? AND r.source_id = ? AND r.link_kind = ?
		ORDER BY r.external_id, r.entity_id`,
		model.EntityTypeRelease, vndbSource, model.LinkKindExact).Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[string]releaseExactHolder, len(rows))
	for _, r := range rows {
		if _, seen := m[r.ExternalID]; seen {
			continue
		}
		m[r.ExternalID] = releaseExactHolder{releaseID: r.ReleaseID, workID: r.WorkID, retired: r.Retired}
	}
	return m, nil
}

func (im *Importer) loadExistingVNDBReleaseKeys() (map[string]bool, error) {
	var rows []struct {
		WorkID  int64  `gorm:"column:work_id"`
		VndbID  string `gorm:"column:vndb_id"`
		Retired bool   `gorm:"column:retired"`
	}
	if err := im.catalog.Raw(`
		SELECT work_id, vndb_id, bool_and(retired) AS retired FROM (
			SELECT rel.work_id, r.external_id AS vndb_id, rel.deleted_at IS NOT NULL AS retired
			FROM catalog_external_ref r
			JOIN catalog_release rel ON rel.id = r.entity_id
			WHERE r.entity_type = ? AND r.source_id = ?
			UNION ALL
			SELECT rel.work_id, rel.extra->>'vndb_id' AS vndb_id, rel.deleted_at IS NOT NULL AS retired
			FROM catalog_release rel
			WHERE rel.extra->>'vndb_id' IS NOT NULL
		) k
		GROUP BY work_id, vndb_id`,
		model.EntityTypeRelease, vndbSource).Scan(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[string]bool, len(rows))
	for _, r := range rows {
		m[releaseKey(r.WorkID, r.VndbID)] = r.Retired
	}
	return m, nil
}

func mintReleaseExactAnchors(tx *gorm.DB, items []releaseAnchorItem) (map[int64]bool, error) {
	landed := make(map[int64]bool, len(items))
	const batch = 1000
	for start := 0; start < len(items); start += batch {
		end := min(start+batch, len(items))
		chunk := items[start:end]
		var ids []int64
		if err := tx.Raw(releaseRefInsertSQL(len(chunk))+` ON CONFLICT DO NOTHING RETURNING entity_id`,
			releaseRefInsertArgs(chunk, model.LinkKindExact, ruleVNDBRelease)...).
			Scan(&ids).Error; err != nil {
			return nil, err
		}
		for _, id := range ids {
			landed[id] = true
		}
	}
	return landed, nil
}

func insertReleaseProbableRefs(tx *gorm.DB, items []releaseAnchorItem, rule string) (int, error) {
	written := 0
	const batch = 1000
	for start := 0; start < len(items); start += batch {
		chunk := items[start:min(start+batch, len(items))]
		res := tx.Exec(releaseRefInsertSQL(len(chunk))+` ON CONFLICT DO NOTHING`,
			releaseRefInsertArgs(chunk, model.LinkKindProbable, rule)...)
		if res.Error != nil {
			return written, res.Error
		}
		written += int(res.RowsAffected)
	}
	return written, nil
}

func releaseRefInsertSQL(n int) string {
	var sb strings.Builder
	sb.WriteString(`INSERT INTO catalog_external_ref (entity_type, entity_id, source_id, external_id, link_kind, matched_by, created_at) VALUES `)
	for i := range n {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString("(?,?,?,?,?,?,now())")
	}
	return sb.String()
}

func releaseRefInsertArgs(items []releaseAnchorItem, linkKind int16, rule string) []any {
	args := make([]any, 0, len(items)*6)
	for _, it := range items {
		args = append(args, model.EntityTypeRelease, it.releaseID, vndbSource, it.rid, linkKind, rule)
	}
	return args
}
