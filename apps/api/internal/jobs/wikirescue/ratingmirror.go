package wikirescue

import (
	"context"
	"fmt"
	"strconv"
)

// The rating / popularity nativization (steps s and t, refs/proj/140 §2.4-§2.5).
//
// SCORE ENCODING — why every score goes through a Go float64 and then a SHORTEST
// ROUND-TRIP decimal string.
//
// The bridge these steps replace reads galgame_*_meta's numeric columns INTO Go
// float64 and hands that float64 to the wire (service.bridgeGalgameRatings); the
// native lane reads catalog_work_rating.score, also numeric, also into float64.
// So the flipped face is byte-identical iff the stored decimal parses back to the
// EXACT float64 the bridge produced. Two things could break that:
//
//   - re-deriving the value in SQL. `rating / 10` in Postgres numeric arithmetic
//     is exact decimal division, which is NOT in general the same double as the
//     float64 division the bridge performs (`float64(82.34) / 10` may differ from
//     the nearest double to 8.234 by one ulp) — and one ulp is a different JSON
//     string on the wire.
//   - letting the driver format the float64 for a numeric column. Any rounding
//     there is silent precision loss.
//
// So each step computes the value the way the bridge does, in float64, and stores
// strconv.FormatFloat(v, 'f', -1, 64) — the shortest decimal that parses back to
// that exact double. Parity then holds by construction rather than by measurement.
//
// The stored string keeps numeric's own comparison semantics, so the diff
// compares the DOUBLE (parsed back from what is stored), never the text: a row
// written as "8.234" and one written as "8.2340" are the same rating and must not
// look like a change.

// scoreOf renders a float64 for a numeric column: the shortest decimal that
// round-trips to this exact double (see the file doc).
func scoreOf(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// ratingKey identifies one catalog_work_rating row: uq_catalog_work_rating
// (work_id, source_id).
type ratingKey struct {
	workID int64
	srcID  int16
}

// ratingVal is the value set steps s and t own on a rating row.
type ratingVal struct {
	score     float64
	voteCount int
	rank      *int
}

func sameRating(a, b ratingVal) bool {
	if a.score != b.score || a.voteCount != b.voteCount {
		return false
	}
	if (a.rank == nil) != (b.rank == nil) {
		return false
	}
	return a.rank == nil || *a.rank == *b.rank
}

// ratingSpec is the shared target description of both rating steps.
func (r *Runner) ratingSpec() mirrorSpec[ratingKey, ratingVal] {
	return mirrorSpec[ratingKey, ratingVal]{
		table:      "catalog_work_rating",
		insertCols: []string{"work_id", "source_id", "score", "vote_count", "rank", "created_at", "updated_at"},
		rowOf: func(k ratingKey, v ratingVal) []any {
			return []any{k.workID, k.srcID, scoreOf(v.score), v.voteCount, v.rank, r.now, r.now}
		},
		updateCols: []string{"score", "vote_count", "rank", "updated_at"},
		updateArgs: func(v ratingVal) []any { return []any{scoreOf(v.score), v.voteCount, v.rank, r.now} },
		workOf:     func(k ratingKey) int64 { return k.workID },
		same:       sameRating,
	}
}

// loadRatingScope reads the existing catalog_work_rating rows of the claimed
// works for a set of sources — the ownership scope of whichever step asks.
func (r *Runner) loadRatingScope(ctx context.Context, span claimSpan, srcIDs []int16) (map[ratingKey]mirrorExisting[ratingVal], error) {
	var scope []struct {
		ID        int64   `gorm:"column:id"`
		WorkID    int64   `gorm:"column:work_id"`
		SourceID  int16   `gorm:"column:source_id"`
		Score     float64 `gorm:"column:score"`
		VoteCount int     `gorm:"column:vote_count"`
		Rank      *int    `gorm:"column:rank"`
	}
	if err := r.catalog.WithContext(ctx).Raw(
		`SELECT t.id, t.work_id, t.source_id, t.score, t.vote_count, t.rank FROM catalog_work_rating t
		 `+claimedScopeJoin+` WHERE t.source_id IN ?`, siteGalgameWiki, srcIDs).Scan(&scope).Error; err != nil {
		return nil, fmt.Errorf("read catalog_work_rating scope: %w", err)
	}
	out := make(map[ratingKey]mirrorExisting[ratingVal], len(scope))
	for _, x := range scope {
		out[ratingKey{workID: x.WorkID, srcID: x.SourceID}] = mirrorExisting[ratingVal]{
			id: x.ID, val: ratingVal{score: x.Score, voteCount: x.VoteCount, rank: x.Rank},
		}
	}
	return out, nil
}

// stepRatingVndbMirror mirrors the CLAIMED works' VNDB rating meta into
// catalog_work_rating (step s, refs/proj/140 §2.4).
//
// VNDB is the ONE rating source that stays on a mirror step instead of moving to
// an importer: galgame_vndb_meta is kept fresh by the wiki-side sync-vndb-scores
// job, which lives until W1 drops the family, and post-W1 the catalog reads
// src_vndb directly (a design item of its own). bangumi / dlsite / eg move to the
// workratings importer in this same commit — see stepMetaAdopt.
//
// Bridge semantics, verbatim: rating IS NOT NULL is the filter (the sync stores
// NULL — never a fake 0 — for a VN with no public rating), score = rating / 10
// decodes the kana wire scale (10-100) to the 1-10 scale VNDB itself displays,
// and rank is always NULL (the meta table carries none).
//
// OWNERSHIP SCOPE — (claimed work, source=vndb). The bangumi / dlsite / eg rating
// rows on the same works belong to the importer and are not in the diff.
func (r *Runner) stepRatingVndbMirror(ctx context.Context) (Stats, error) {
	st := Stats{Step: "s"}

	span, err := r.claimedSpan(ctx)
	if err != nil {
		return st, err
	}
	vndbSrc, err := resolveSourceID(r.catalog.WithContext(ctx), mediaSourceVNDB)
	if err != nil {
		return st, err
	}

	var metas []struct {
		GalgameID int64   `gorm:"column:galgame_id"`
		Rating    float64 `gorm:"column:rating"`
		VoteCount int     `gorm:"column:vote_count"`
	}
	if err := r.galgame.WithContext(ctx).Raw(
		`SELECT galgame_id, rating, vote_count FROM galgame_vndb_meta
		 WHERE rating IS NOT NULL ORDER BY galgame_id`).Scan(&metas).Error; err != nil {
		return st, fmt.Errorf("read galgame_vndb_meta: %w", err)
	}
	st.Source = len(metas)

	desired := newMirrorDesired[ratingKey, ratingVal](len(metas))
	for _, m := range metas {
		workID, ok := span.byGalgame[m.GalgameID]
		if !ok {
			continue
		}
		desired.put(ratingKey{workID: workID, srcID: vndbSrc}, ratingVal{
			score: m.Rating / 10, voteCount: m.VoteCount,
		})
	}
	st.Anchored = desired.len()

	existing, err := r.loadRatingScope(ctx, span, []int16{vndbSrc})
	if err != nil {
		return st, err
	}
	st.Note = "scope=claimed×vndb; score=rating/10 (kana wire scale decoded)"
	ledger := newMirrorLedger()
	if err := runMirror(ctx, r, ledger, r.ratingSpec(), desired, existing); err != nil {
		return st, err
	}
	ledger.into(&st)
	return st, nil
}
