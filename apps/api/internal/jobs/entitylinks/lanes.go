package entitylinks

import (
	"context"
	"fmt"

	"api/internal/platform/catalog/model"
)

type extlinkRow struct {
	EntityID int64  `gorm:"column:entity_id"`
	Site     string `gorm:"column:site"`
	Value    string `gorm:"column:value"`
}

const vnChainQuery = `
	SELECT vr.entity_id AS entity_id, e.site AS site, e.value AS value
	FROM catalog_external_ref vr
	JOIN %s jx ON jx.id = vr.external_id
	JOIN src_vndb.extlinks e ON e.id = jx.link
	WHERE vr.entity_type = ? AND vr.source_id = ? AND vr.link_kind = 0
	  AND e.site IN ? AND coalesce(e.value, '') <> ''
	ORDER BY vr.entity_id, e.site, e.value`

const releaseChainQuery = `
	SELECT rel.work_id AS entity_id, e.site AS site, e.value AS value
	FROM catalog_external_ref vr
	JOIN catalog_release rel ON rel.id = vr.entity_id AND rel.deleted_at IS NULL
	JOIN src_vndb.releases_extlinks jx ON jx.id = vr.external_id
	JOIN src_vndb.releases sr ON sr.id = jx.id AND sr.official
	JOIN src_vndb.extlinks e ON e.id = jx.link
	WHERE vr.entity_type = ? AND vr.source_id = ? AND vr.link_kind = 0
	  AND e.site IN ? AND coalesce(e.value, '') <> ''
	ORDER BY rel.work_id, e.site, e.value`

const bgmWorkInfoboxQuery = `
	SELECT a.entity_id AS entity_id, f->>'Key' AS site, f->>'Value' AS value
	FROM (SELECT entity_id, external_id::bigint AS sid
	      FROM catalog_external_ref
	      WHERE entity_type = ? AND source_id = ? AND link_kind = 0
	        AND external_id ~ '^[0-9]+$') a
	JOIN src_bangumi.subject s ON s.id = a.sid
	CROSS JOIN LATERAL jsonb_array_elements(s.infobox_parsed->'Fields') f
	WHERE coalesce(s.parse_error, '') = ''
	  AND jsonb_typeof(s.infobox_parsed->'Fields') = 'array'
	  AND coalesce(f->>'Value', '') <> ''
	ORDER BY a.entity_id`

func (r *runner) planWork(ctx context.Context) error {
	rows, err := r.scan(ctx, releaseChainQuery, model.EntityTypeRelease, r.reg.vndb, workTypedSites)
	if err != nil {
		return fmt.Errorf("release chain: %w", err)
	}
	for _, row := range rows {
		r.addTyped(row, workTypedSites, ruleVNDBExtlink)
	}

	rows, err = r.scanJunction(ctx, "src_vndb.vn_extlinks", model.EntityTypeWork, workWebSites)
	if err != nil {
		return fmt.Errorf("vn chain: %w", err)
	}
	for _, row := range rows {
		r.addWeb(row, workWebSites)
	}

	return r.planBangumiWorkSites(ctx)
}

func (r *runner) planLabel(ctx context.Context) error {
	return r.planEntity(ctx, "src_vndb.producers_extlinks", model.EntityTypeLabel,
		labelTypedSites, labelWebSites)
}

func (r *runner) planPerson(ctx context.Context) error {
	return r.planEntity(ctx, "src_vndb.staff_extlinks", model.EntityTypePerson,
		personTypedSites, personWebSites)
}

func (r *runner) planEntity(ctx context.Context, junction string, entityType int16, typed, web siteSet) error {
	both := newSiteSet(append(append([]string{}, typed.list...), web.list...)...)
	rows, err := r.scanJunction(ctx, junction, entityType, both)
	if err != nil {
		return err
	}
	for _, row := range rows {
		switch {
		case typed.has(row.Site):
			r.addTyped(row, typed, ruleVNDBExtlink)
		case web.has(row.Site):
			r.addWeb(row, web)
		}
	}
	return nil
}

func (r *runner) planBangumiWorkSites(ctx context.Context) error {
	var rows []extlinkRow
	if err := r.db.WithContext(ctx).
		Raw(bgmWorkInfoboxQuery, model.EntityTypeWork, r.reg.bangumi).Scan(&rows).Error; err != nil {
		return fmt.Errorf("bangumi infobox: %w", err)
	}
	for _, row := range rows {
		if classifyBGMKey(row.Site) != bgmKeyWebsite {
			continue
		}
		ext, ok := normalizeURL(row.Value)
		if !ok {
			r.stats.SkippedMalformed++
			continue
		}
		if isStoreHost(ext) {
			r.stats.SkippedStore++
			continue
		}
		r.add(row.EntityID, r.reg.typed[officialSiteKey], ext, ruleBGMWorkSite)
	}
	return nil
}

func (r *runner) scanJunction(ctx context.Context, junction string, entityType int16, sites siteSet) ([]extlinkRow, error) {
	return r.scan(ctx, fmt.Sprintf(vnChainQuery, junction), entityType, r.reg.vndb, sites)
}

func (r *runner) scan(ctx context.Context, query string, entityType, source int16, sites siteSet) ([]extlinkRow, error) {
	var rows []extlinkRow
	err := r.db.WithContext(ctx).Raw(query, entityType, source, sites.list).Scan(&rows).Error
	return rows, err
}

func (r *runner) addTyped(row extlinkRow, allowed siteSet, rule string) {
	ts, known := typedSites[row.Site]
	if !known || !allowed.has(row.Site) {
		return
	}
	ext, ok := ts.normalize(row.Value)
	if !ok {
		r.stats.SkippedMalformed++
		return
	}
	if r.entityType == model.EntityTypeWork && ts.sourceKey == officialSiteKey && isStoreHost(ext) {
		r.stats.SkippedStore++
		return
	}
	r.add(row.EntityID, r.reg.typed[ts.sourceKey], ext, rule)
}

func (r *runner) addWeb(row extlinkRow, allowed siteSet) {
	if !allowed.has(row.Site) {
		return
	}
	url, ok := renderWeb(row.Site, row.Value)
	if !ok {
		r.stats.SkippedMalformed++
		return
	}
	r.add(row.EntityID, r.reg.web, url, ruleVNDBExtlink)
}
