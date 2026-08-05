package getchutitlerefs

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// catalogRelease is one release of a live work: the three fields the match keys
// on, plus the three that tell one same-day sibling from another.
type catalogRelease struct {
	ReleaseID int64  `gorm:"column:release_id"`
	WorkID    int64  `gorm:"column:work_id"`
	Title     string `gorm:"column:title"`
	Brand     string `gorm:"column:brand"`
	RDate     string `gorm:"column:rdate"`

	// RTitle is the RELEASE's own title, which carries the edition marker the
	// work title does not.
	RTitle string `gorm:"column:rtitle"`
	// Platform and Lang are empty when the catalog does not know — 44% and 49%
	// of dated releases respectively. They are therefore used to REJECT a
	// contradiction, never to require a confirmation; an unknown must not lose
	// to a known.
	Platform string `gorm:"column:platform"`
	Lang     string `gorm:"column:lang"`
}

// getchuItem is one crawled product with no Getchu anchor of any kind.
type getchuItem struct {
	GetchuID string `gorm:"column:getchu_id"`
	Title    string `gorm:"column:title"`
	Brand    string `gorm:"column:brand"`
	RDate    string `gorm:"column:release_date"`
}

// candidate is a resolved (product → release) pair awaiting confirmation. The
// trailing fields are carried for the audit dump only: the release-level choice
// has no second signal behind it, so it has to be reviewable (see audit.go).
type candidate struct {
	GetchuID  string
	ReleaseID int64
	WorkID    int64

	GetchuTitle  string
	Edition      string
	ReleaseTitle string
	Platform     string
	Lang         string
	Siblings     int
}

// loadCatalogReleases reads every dated release of a live galgame work that has
// both a title and a label. A release with no date cannot take part: the date
// is the signal that separates a game from its own re-release.
func loadCatalogReleases(ctx context.Context, db *gorm.DB, medium int16) ([]catalogRelease, error) {
	var rows []catalogRelease
	err := db.WithContext(ctx).Raw(`
		SELECT DISTINCT r.id AS release_id, w.id AS work_id,
		       t.title, l.display_name AS brand,
		       r.released_y::text || '/' || lpad(r.released_m::text,2,'0') || '/' || lpad(r.released_d::text,2,'0') AS rdate,
		       coalesce(r.title, '') AS rtitle,
		       coalesce(r.platform, '') AS platform,
		       coalesce(r.lang, '') AS lang
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
		brand := NormBrand(it.Brand)
		group := byKey[matchKey{NormTitle(it.Title), brand}]
		// A product titled "Summer Pockets 初回限定版" names the work
		// "Summer Pockets". Only fall back to the bare title, so a work whose
		// own title genuinely ends in a marker still wins on the exact form.
		base, edition := SplitEdition(it.Title)
		if len(group) == 0 && base != it.Title {
			if group = byKey[matchKey{NormTitle(base), brand}]; len(group) > 0 {
				st.MatchedAfterStrip++
			}
		}
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
		if len(dated) == 0 {
			st.DateDiffers++
			continue
		}
		// How many boxes the narrowing had to choose between, recorded before it
		// narrows: a row resolved out of one candidate needed no judgement, a row
		// resolved out of six is where a reviewer should look first.
		siblings := len(dated)
		if len(dated) > 1 {
			if narrowed := rejectContradictions(dated); len(narrowed) > 0 && len(narrowed) < len(dated) {
				dated = narrowed
				st.NarrowedByPlatform++
			}
		}
		if len(dated) > 1 && edition != "" {
			if narrowed := sameEdition(dated, edition); len(narrowed) == 1 {
				dated = narrowed
				st.NarrowedByEdition++
			}
		}
		// A choice was made, and the winner's own title contradicts the box the
		// product says it is. Refuse it: this is the same "ambiguity is a skip,
		// never a pick" rule the work and release checks apply, on the edition
		// axis. Scoped to siblings > 1 on purpose — when the catalog records a
		// single release for the day, a marker disagreement means only that the
		// catalog under-models editions, and dropping those would throw away 12
		// correct work matches to gain nothing.
		if siblings > 1 && len(dated) == 1 && contradicts(edition, dated[0].RTitle) {
			st.EditionConflict++
			continue
		}
		switch len(dated) {
		case 1:
			out = append(out, candidate{
				GetchuID: it.GetchuID, ReleaseID: dated[0].ReleaseID, WorkID: dated[0].WorkID,
				GetchuTitle: it.Title, Edition: edition, ReleaseTitle: dated[0].RTitle,
				Platform: dated[0].Platform, Lang: dated[0].Lang, Siblings: siblings,
			})
		default:
			st.AmbiguousRelease++
		}
	}
	return out, st
}

// rejectContradictions drops the siblings that positively cannot be the Getchu
// product: Getchu's pc_soft genre is Windows software sold in Japan, so a
// release the catalog KNOWS to be a console port or an English localization is
// out. A release with no platform or no language recorded stays — half of them
// have none, and an unknown must not lose to a known.
func rejectContradictions(rels []catalogRelease) []catalogRelease {
	out := make([]catalogRelease, 0, len(rels))
	for _, r := range rels {
		if r.Platform != "" && r.Platform != "win" {
			continue
		}
		if r.Lang != "" && r.Lang != "ja" {
			continue
		}
		out = append(out, r)
	}
	return out
}

// contradicts reports whether the product and the release name DIFFERENT
// editions. Silence on either side is not a contradiction: an unrecognized
// marker, or a release title with none at all, is an absence of evidence.
func contradicts(edition, releaseTitle string) bool {
	a, b := EditionClass(edition), TitleEditionClass(releaseTitle)
	return a != "" && b != "" && a != b
}

// sameEdition keeps the siblings whose own title claims the same edition the
// product does. It returns the input untouched unless exactly one sibling
// agrees: two agreeing siblings are still ambiguous, and zero means the
// vocabulary simply did not reach this pair.
func sameEdition(rels []catalogRelease, edition string) []catalogRelease {
	want := EditionClass(edition)
	if want == "" {
		return rels
	}
	var hit []catalogRelease
	for _, r := range rels {
		if TitleEditionClass(r.RTitle) == want {
			hit = append(hit, r)
		}
	}
	if len(hit) == 1 {
		return hit
	}
	return rels
}
