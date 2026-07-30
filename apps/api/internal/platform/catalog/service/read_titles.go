// read_titles.go — the work TITLE read face.
//
// One native lane for every work. Titles were bridged out of the wiki body for a
// CLAIMED work from A2-R1 (refs/proj/136 区 A) until the W1-pre nativization
// (refs/proj/140) materialized the bridge's projection into catalog_work_title
// (wikirescue step p, a MIRROR kept in step with the wiki until the family
// drops) and deleted the bridge. The projection this table now holds is the
// bridge's own, verbatim:
//
//   - the four fixed wiki name columns as OFFICIAL rows (BCP-47, in pivot order
//     via insertion order), and each galgame_alias row as an ALIAS row with NO
//     language. The wiki records no language for an alias, and inventing one
//     would be a fabrication — an empty tag also keeps aliases out of the D7
//     four-key names pivot (which they do not belong in) while still reaching
//     the detail titles[] and the search index: an alias is searchable and
//     displayable, not a name slot.
//   - an alias whose exact string is already one of the work's official names
//     was dropped at mirror time (the bridge's own "one name, one row" rule).
//
// TWO LANES, one table. The two consumers have different frozen contracts:
//
//   - the DISPLAY lane (loadWorkTitles) drops search_hint rows in the query —
//     they are findability-only and never leave the internal face — and orders
//     (kind, id) so the list's per-key pivot has a stable first row (officials
//     stay in pivot order: the mirror inserts them in that order);
//   - the DETAIL lane (loadWorkDetailTitles) keeps EVERY kind in the (kind,
//     lang) order the S2S work record has always emitted for a native work.
//     Claimed works joined that contract at the flip: their detail rows now
//     sort (kind, lang) like everyone else's, and their search_hint rows (the
//     dlsite release-attach importer's kana) surface like everyone else's —
//     both recorded as intended changes of the flip, both set-preserving.
package service

import (
	"context"

	"api/internal/platform/catalog/model"
)

// WorkTitleRow is one title on a work's read face. Latin is the romanized
// rendering ("" when unrecorded; rows mirrored from the wiki never carry one —
// the wiki body has no romaji column).
type WorkTitleRow struct {
	Lang  string
	Title string
	Latin string
	Kind  int16
}

// loadWorkTitles reads the DISPLAY titles for a set of works: native rows with
// search_hint excluded at the query level. Batched (§9.1) — one query for the
// whole set, never per work. A work with no title is absent from the map (the
// caller renders []).
func (s *ReadService) loadWorkTitles(ctx context.Context, subjects []claimSubject) (map[int64][]WorkTitleRow, error) {
	return s.loadTitles(ctx, subjects, false)
}

// loadWorkDetailTitles is the DETAIL lane: every kind (search hints included —
// the internal work record has always carried them) in its frozen (kind, lang)
// order.
func (s *ReadService) loadWorkDetailTitles(ctx context.Context, subjects []claimSubject) (map[int64][]WorkTitleRow, error) {
	return s.loadTitles(ctx, subjects, true)
}

// loadTitles is the shared body of the two lanes; withHints selects the caliber
// (see the file doc).
func (s *ReadService) loadTitles(ctx context.Context, subjects []claimSubject, withHints bool) (map[int64][]WorkTitleRow, error) {
	out := make(map[int64][]WorkTitleRow, len(subjects))
	if len(subjects) == 0 {
		return out, nil
	}
	workIDs := make([]int64, 0, len(subjects))
	for _, sub := range subjects {
		workIDs = append(workIDs, sub.WorkID)
	}
	return out, s.nativeWorkTitles(ctx, workIDs, out, withHints)
}

// nativeWorkTitles reads the works' catalog_work_title rows in ONE query.
// withHints selects the lane's caliber: the display lane excludes search_hint
// at the query level and orders (kind, id); the detail lane keeps every kind in
// the frozen (kind, lang) order.
//
// Both lanes end on `id`, and on the detail lane that is load-bearing rather than
// decorative: (kind, lang) does not distinguish two ALIAS rows, which all carry
// lang='' by construction, so without a tiebreak their relative order would be
// whatever the plan happened to produce — unstable across an index change or a
// VACUUM, on a frozen face. Insertion order is the honest tiebreak: for a mirrored
// wiki body it reproduces galgame_alias's own id order, which is exactly the order
// the deleted bridge emitted.
func (s *ReadService) nativeWorkTitles(ctx context.Context, workIDs []int64, out map[int64][]WorkTitleRow, withHints bool) error {
	q := `SELECT work_id, lang, title, coalesce(latin, '') AS latin, kind FROM catalog_work_title
		WHERE work_id IN ? AND kind <> ? ORDER BY work_id, kind, id`
	args := []any{workIDs, model.WorkTitleKindSearchHint}
	if withHints {
		q = `SELECT work_id, lang, title, coalesce(latin, '') AS latin, kind FROM catalog_work_title
			WHERE work_id IN ? ORDER BY work_id, kind, lang, id`
		args = []any{workIDs}
	}
	var rows []struct {
		WorkID int64  `gorm:"column:work_id"`
		Lang   string `gorm:"column:lang"`
		Title  string `gorm:"column:title"`
		Latin  string `gorm:"column:latin"`
		Kind   int16  `gorm:"column:kind"`
	}
	if err := s.db.WithContext(ctx).Raw(q, args...).Scan(&rows).Error; err != nil {
		return err
	}
	for _, r := range rows {
		out[r.WorkID] = append(out[r.WorkID], WorkTitleRow{
			Lang: r.Lang, Title: r.Title, Latin: r.Latin, Kind: r.Kind,
		})
	}
	return nil
}
