package importer

import (
	"strings"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// Release identity: everything that reads or writes catalog_external_ref for
// entity_type 6 (release), source 2 (vndb). The file exists so there is exactly
// ONE place that mints an exact release anchor and exactly one place that
// answers "is this rid's slot free?" — see the vndbreleases.go header for why
// the (work, rid) row key must never be asked that question.

// releaseAnchorItem pairs a catalog_release row with the vndb r-id it should
// answer to.
type releaseAnchorItem struct {
	releaseID int64
	rid       string
}

// releaseExactHolder is the current occupant of a rid's EXACT identity slot.
// workID is 0 (and retired true) when the ref points at a release row that no
// longer exists — a broken ref still squats the slot, so it still blocks.
type releaseExactHolder struct {
	releaseID int64
	workID    int64
	retired   bool
}

// backfillReleaseProbableRefs is PHASE 1: every catalog_release row that carries
// Extra.vndb_id but holds no source-2 release ref at all gets a PROBABLE one.
//
// Why probable and not exact: uq_catalog_external_ref_exact covers link_kind = 0
// only, so a probable ref can represent a multi-VN release (the bulk of this
// set: a rid mapping to several vids, deliberately never anchored) in the
// identity index WITHOUT contending for the single exact slot. It states "this
// row is known upstream as rid" without asserting "this row IS rid".
//
// Idempotent by construction — the NOT EXISTS is the whole filter, so a second
// run inserts 0 — and it honours the dry-run flag by counting instead of
// writing. One statement, so it is atomic on its own; it does not need the
// wave's transaction and must not be inside it (it is useful even if the wave
// later fails).
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

	// ON CONFLICT DO NOTHING is belt-and-braces only: the NOT EXISTS already
	// excludes every row that holds a ref, so nothing here can collide.
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

// loadReleaseExactHolders is the ANCHOR index: rid → the release holding its
// EXACT source-2 ref, keyed by rid ALONE. That is exactly the shape of
// uq_catalog_external_ref_exact (source_id, external_id, entity_type) WHERE
// link_kind = 0 — a resume index must be at least as coarse as the unique it
// protects, and the (work, rid) row index is finer, which is what let a mint be
// planned against an already-occupied slot.
//
// Liveness is reported, never used to free a slot: the unique is not
// deleted_at-aware, so a soft-deleted holder still squats (the rostervndb /
// vndbcredits "retired holder is not a vacancy" ruling). The LEFT JOIN keeps a
// ref whose release row is gone: it still occupies the index.
func (im *Importer) loadReleaseExactHolders() (map[string]releaseExactHolder, error) {
	var rows []struct {
		ExternalID string `gorm:"column:external_id"`
		ReleaseID  int64  `gorm:"column:release_id"`
		WorkID     int64  `gorm:"column:work_id"`
		Retired    bool   `gorm:"column:retired"`
	}
	// ORDER BY makes the (constraint-forbidden, but cheap to survive) duplicate
	// case deterministic: the lowest holder wins.
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

// loadExistingVNDBReleaseKeys is the ROW resume index: every (work_id, rid)
// already claimed, mapped to whether its only holders are soft-deleted. It is
// deliberately NOT restricted to live rows — a retired row is a claim, not a
// vacancy, and re-creating it on every pass is exactly what liveness-awareness
// here would cause. The value separates the two skip classes for the report; a
// key with any live holder counts as live.
//
// Arm A is catalog_external_ref, the identity index. Arm B is the legacy
// Extra.vndb_id payload.
//
// TRANSITIONAL: arm B exists only until the phase-1 backfill
// (backfillReleaseProbableRefs) has been APPLIED to a database — before that,
// legacy rows hold no ref and arm A cannot see them, and a dry run (which never
// backfills) would otherwise disagree with the apply that follows it. Delete arm
// B, and this note, once prod has run --apply once and
// `SELECT count(*) FROM catalog_release rel WHERE rel.extra->>'vndb_id' IS NOT NULL
//
//	AND NOT EXISTS (SELECT 1 FROM catalog_external_ref r WHERE r.entity_type = 6
//	AND r.entity_id = rel.id AND r.source_id = 2)` reports 0.
//
// The `extra->>'vndb_id' IS NOT NULL` filter avoids the jsonb `?` operator
// (which collides with GORM's positional placeholder).
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

// mintReleaseExactAnchors is the SINGLE chokepoint through which this wave
// creates EXACT release anchors. It never propagates a unique violation: ON
// CONFLICT DO NOTHING (untargeted, so it covers both the ref primary key and
// uq_catalog_external_ref_exact) turns "the slot belongs to someone else" into a
// missing row, and RETURNING tells the caller precisely which claims landed. A
// 23505 from this wave is therefore unreachable by construction rather than by
// the planner having guessed right.
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

// insertReleaseProbableRefs attaches PROBABLE refs — the grade a row gets when
// the exact slot is unavailable (held elsewhere) or inappropriate (a multi-VN
// release). Probable is outside the partial unique, so these can never collide
// with each other; ON CONFLICT DO NOTHING only guards the ref primary key.
// Returns the number written.
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

// releaseRefInsertSQL builds the VALUES skeleton shared by both ref writers.
// Raw SQL rather than a GORM batch create because only RETURNING can tell an
// inserted row from a conflict-skipped one, which is the whole point of the
// chokepoint above.
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
