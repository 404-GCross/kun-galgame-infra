package getchutitlerefs

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// catalogRelease is one release of a live work, with the three fields the
// match keys on.
type catalogRelease struct {
	ReleaseID int64  `gorm:"column:release_id"`
	WorkID    int64  `gorm:"column:work_id"`
	Title     string `gorm:"column:title"`
	Brand     string `gorm:"column:brand"`
	RDate     string `gorm:"column:rdate"`
}

// getchuItem is one crawled product with no Getchu anchor of any kind.
type getchuItem struct {
	GetchuID string `gorm:"column:getchu_id"`
	Title    string `gorm:"column:title"`
	Brand    string `gorm:"column:brand"`
	RDate    string `gorm:"column:release_date"`
}

// candidate is a resolved (product → release) pair awaiting confirmation.
type candidate struct {
	GetchuID  string
	ReleaseID int64
	WorkID    int64
}

// loadCatalogReleases reads every dated release of a live galgame work that has
// both a title and a label. A release with no date cannot take part: the date
// is the signal that separates a game from its own re-release.
func loadCatalogReleases(ctx context.Context, db *gorm.DB, medium int16) ([]catalogRelease, error) {
	var rows []catalogRelease
	err := db.WithContext(ctx).Raw(`
		SELECT DISTINCT r.id AS release_id, w.id AS work_id,
		       t.title, l.display_name AS brand,
		       r.released_y::text || '/' || lpad(r.released_m::text,2,'0') || '/' || lpad(r.released_d::text,2,'0') AS rdate
		FROM catalog_release r
		JOIN catalog_work w ON w.id = r.work_id AND w.deleted_at IS NULL AND w.medium_id = ?
		JOIN catalog_work_title t ON t.work_id = w.id
		JOIN catalog_work_label wl ON wl.work_id = w.id
		JOIN catalog_label l ON l.id = wl.label_id AND l.deleted_at IS NULL
		WHERE r.deleted_at IS NULL
		  AND r.released_y IS NOT NULL AND r.released_m IS NOT NULL AND r.released_d IS NOT NULL
		  AND btrim(t.title) <> '' AND btrim(l.display_name) <> ''`, medium).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("load catalog releases: %w", err)
	}
	return rows, nil
}

// loadUnanchoredItems reads the crawled products the catalog has no Getchu ref
// for, in any tier — re-running must never mint a second anchor for a product
// that already has one, including one this lane wrote earlier.
func loadUnanchoredItems(ctx context.Context, gdb *gorm.DB, anchored map[string]bool) ([]getchuItem, error) {
	var rows []getchuItem
	err := gdb.WithContext(ctx).Raw(`
		SELECT getchu_id, title, coalesce(brand,'') AS brand, coalesce(release_date,'') AS release_date
		FROM items
		WHERE status = 'fetched' AND btrim(title) <> ''
		  AND brand IS NOT NULL AND btrim(brand) <> ''
		  AND release_date IS NOT NULL AND btrim(release_date) <> ''`).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("load getchu items: %w", err)
	}
	out := rows[:0]
	for _, r := range rows {
		if !anchored[r.GetchuID] {
			out = append(out, r)
		}
	}
	return out, nil
}

// loadAnchored returns every getchu external_id the catalog already carries.
func loadAnchored(ctx context.Context, db *gorm.DB, source int16) (map[string]bool, error) {
	var ids []string
	if err := db.WithContext(ctx).Raw(
		`SELECT DISTINCT external_id FROM catalog_external_ref WHERE source_id = ?`, source).
		Scan(&ids).Error; err != nil {
		return nil, fmt.Errorf("load anchored ids: %w", err)
	}
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

// matchKey is the (title, brand) pair a product and a release must share.
type matchKey struct{ title, brand string }

// match resolves products to releases on title + brand + date.
//
// Ambiguity is a SKIP, never a pick, exactly as in the character matcher. Two
// distinct shapes are refused:
//
//   - the key reaches more than one WORK — the same title and brand naming two
//     different games, which a date cannot arbitrate;
//   - the key reaches one work but more than one RELEASE on that same date —
//     an edition split (限定版 / 通常版 shipping together), where picking one
//     would attach a product to an arbitrary sibling.
//
// The second is why this anchors a RELEASE rather than a work: a Getchu product
// IS one edition, and the existing anchors are release-level.
func match(items []getchuItem, rels []catalogRelease) ([]candidate, Stats) {
	byKey := map[matchKey][]catalogRelease{}
	for _, r := range rels {
		k := matchKey{NormTitle(r.Title), NormBrand(r.Brand)}
		byKey[k] = append(byKey[k], r)
	}

	var st Stats
	out := make([]candidate, 0, len(items))
	for _, it := range items {
		group := byKey[matchKey{NormTitle(it.Title), NormBrand(it.Brand)}]
		if len(group) == 0 {
			st.NoTitleMatch++
			continue
		}
		works := map[int64]bool{}
		for _, r := range group {
			works[r.WorkID] = true
		}
		if len(works) != 1 {
			st.AmbiguousWork++
			continue
		}
		var dated []catalogRelease
		for _, r := range group {
			if r.RDate == it.RDate {
				dated = append(dated, r)
			}
		}
		switch len(dated) {
		case 0:
			st.DateDiffers++
		case 1:
			out = append(out, candidate{GetchuID: it.GetchuID, ReleaseID: dated[0].ReleaseID, WorkID: dated[0].WorkID})
		default:
			st.AmbiguousRelease++
		}
	}
	return out, st
}
