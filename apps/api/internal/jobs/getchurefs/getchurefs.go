// Package getchurefs builds the catalog's Getchu anchors from VNDB's own
// curated external links (refs/proj/167 §1).
//
// WHY THERE IS NO MATCHING HERE. Every other storefront we anchor needed either
// a title-matching pass (and the probable tier that comes with it) or a
// community cross-reference of unknown provenance — the DMM anchors, for
// instance, are 14,014 rows of link_kind=probable inherited from erogamescape,
// which is why nothing can be ingested from them without an adjudication wave
// first. Getchu needs none of that: VNDB records the getchu id on the RELEASE,
// and we have already anchored that release EXACT to VNDB. The chain is
//
//	catalog_release --(exact, source=vndb)--> src_vndb.releases
//	  --> src_vndb.releases_extlinks --> src_vndb.extlinks (site='getchu')
//
// so the getchu ref is exactly as strong as the vndb ref it rides on, and is
// written at the same tier.
//
// Discipline:
//   - RELEASE-level, mirroring how DLsite worknos are anchored. A work can hold
//     several editions (限定版 / 通常版 / DL 版) with different getchu ids, and
//     collapsing them onto the work would have to pick one arbitrarily.
//   - INSERT-if-absent, never re-grade: an existing row (whatever its link_kind
//     or matched_by) is left untouched. Re-grading someone else's judgement
//     from a bulk importer is the step-21 negative-knowledge rule.
//   - 'getchudl' is deliberately NOT ingested. It is the download edition's own
//     id and points at a different page shape; the wave-167 crawler only
//     understands /item/<id>/.
package getchurefs

import (
	"context"
	"fmt"
	"log/slog"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// matchedBy tags every row this importer writes, so the provenance of an anchor
// is legible in the table without consulting a document.
const matchedBy = "rule:vndb-extlink-getchu"

// site is the VNDB extlink vocabulary key.
const site = "getchu"

type Opts struct {
	Apply bool
	Limit int
}

type Stats struct {
	Candidates int // (release, getchu id) pairs reachable from an exact vndb anchor
	Existing   int // already anchored — left untouched
	Planned    int // rows a dry run would write
	Written    int
	Conflict   int // unique key said it was already there (concurrent writer)
	Errors     int
}

type candidate struct {
	ReleaseID int64  `gorm:"column:release_id"`
	GetchuID  string `gorm:"column:getchu_id"`
}

// Run resolves and (in apply mode) writes the anchors.
func Run(ctx context.Context, db *gorm.DB, opts Opts) (*Stats, error) {
	var getchuSource int16
	if err := db.WithContext(ctx).
		Raw(`SELECT id FROM catalog_source WHERE key = ?`, site).Scan(&getchuSource).Error; err != nil {
		return nil, fmt.Errorf("resolve getchu source: %w", err)
	}
	if getchuSource == 0 {
		return nil, fmt.Errorf("catalog_source has no %q row — run the seed first", site)
	}
	var vndbSource int16
	if err := db.WithContext(ctx).
		Raw(`SELECT id FROM catalog_source WHERE key = 'vndb'`).Scan(&vndbSource).Error; err != nil {
		return nil, fmt.Errorf("resolve vndb source: %w", err)
	}

	cands, err := loadCandidates(ctx, db, vndbSource, getchuSource, opts.Limit)
	if err != nil {
		return nil, fmt.Errorf("load candidates: %w", err)
	}
	st := &Stats{Candidates: len(cands)}
	slog.Info("getchu-refs candidates", "candidates", st.Candidates, "apply", opts.Apply)

	for _, c := range cands {
		st.Planned++
		if !opts.Apply {
			continue
		}
		res := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).
			Create(&model.CatalogExternalRef{
				EntityType: model.EntityTypeRelease,
				EntityID:   c.ReleaseID,
				SourceID:   getchuSource,
				ExternalID: c.GetchuID,
				LinkKind:   model.LinkKindExact,
				MatchedBy:  matchedBy,
			})
		switch {
		case res.Error != nil:
			st.Errors++
			slog.Warn("write ref", "release", c.ReleaseID, "getchu", c.GetchuID, "err", res.Error)
		case res.RowsAffected == 0:
			st.Conflict++
		default:
			st.Written++
		}
	}
	slog.Info("getchu-refs done", "apply", opts.Apply, "candidates", st.Candidates,
		"existing", st.Existing, "planned", st.Planned, "written", st.Written,
		"conflict", st.Conflict, "errors", st.Errors)
	return st, nil
}

// loadCandidates walks the chain described in the package doc and excludes
// anything already anchored. The NOT EXISTS is on (entity, source) rather than
// on the full row: a release that already carries SOME getchu id keeps it, even
// if VNDB now lists a different one — re-pointing an existing anchor is an
// adjudication, not an import.
func loadCandidates(ctx context.Context, db *gorm.DB, vndbSource, getchuSource int16, limit int) ([]candidate, error) {
	q := `
		SELECT DISTINCT ON (rel.id) rel.id AS release_id, e.value AS getchu_id
		FROM catalog_release rel
		JOIN catalog_external_ref vr ON vr.entity_type = ? AND vr.entity_id = rel.id
			AND vr.source_id = ? AND vr.link_kind = ?
		JOIN src_vndb.releases_extlinks rx ON rx.id = vr.external_id
		JOIN src_vndb.extlinks e ON e.id = rx.link AND e.site = ?
		WHERE rel.deleted_at IS NULL
		  AND NOT EXISTS (
			SELECT 1 FROM catalog_external_ref g
			WHERE g.entity_type = ? AND g.entity_id = rel.id AND g.source_id = ?)
		ORDER BY rel.id, e.value`
	args := []any{
		model.EntityTypeRelease, vndbSource, model.LinkKindExact, site,
		model.EntityTypeRelease, getchuSource,
	}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	var out []candidate
	if err := db.WithContext(ctx).Raw(q, args...).Scan(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}
