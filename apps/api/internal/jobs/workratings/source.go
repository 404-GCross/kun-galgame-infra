package workratings

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

// registry holds the catalog registry ids this backfill needs, resolved by key
// (never hardcoded) so a rehearsal / prod DB with different auto-increment
// seeds still works — the bgmsummaries / dlsitemedia discipline.
type registry struct {
	galgameMedium int16
	bangumiSource int16
	egSource      int16
	dlsiteSource  int16
}

func resolveRegistry(ctx context.Context, db *gorm.DB) (registry, error) {
	var r registry
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_medium WHERE key = 'galgame'`).Scan(&r.galgameMedium).Error; err != nil {
		return r, fmt.Errorf("resolve galgame medium: %w", err)
	}
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_source WHERE key = 'bangumi'`).Scan(&r.bangumiSource).Error; err != nil {
		return r, fmt.Errorf("resolve bangumi source: %w", err)
	}
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_source WHERE key = 'erogamespace'`).Scan(&r.egSource).Error; err != nil {
		return r, fmt.Errorf("resolve erogamespace source: %w", err)
	}
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_source WHERE key = 'dlsite'`).Scan(&r.dlsiteSource).Error; err != nil {
		return r, fmt.Errorf("resolve dlsite source: %w", err)
	}
	if r.galgameMedium == 0 || r.bangumiSource == 0 || r.egSource == 0 || r.dlsiteSource == 0 {
		return r, fmt.Errorf("registry not seeded (galgame medium=%d, bangumi source=%d, erogamespace source=%d, dlsite source=%d)",
			r.galgameMedium, r.bangumiSource, r.egSource, r.dlsiteSource)
	}
	return r, nil
}

// bgmCandidate is one bodyless galgame work joined to its EXACT Bangumi
// anchor's subject score block. Site is carried (not filtered out) so the
// write-time XOR guard can re-assert bodylessness.
type bgmCandidate struct {
	WorkID       int64   `gorm:"column:work_id"`
	SubjectID    int64   `gorm:"column:subject_id"`
	Site         *string `gorm:"column:site"`
	Score        float64 `gorm:"column:score"`
	Rank         int     `gorm:"column:rank"`
	ScoreDetails []byte  `gorm:"column:score_details"`
}

// loadBgmCandidates resolves bodyless galgame works carrying an EXACT Bangumi
// work anchor — matched_by UNRESTRICTED (every exact tier asserts identity, the
// 66/69/71 ruling; the bgmworkmeta precedent) — joined to the anchored
// subject's score/rank/score_details, the same join shape as
// bgmsummaries.loadCandidates (src_bangumi is a schema inside the catalog DB,
// single DSN). DISTINCT ON keeps ONE anchor per work (the lowest external_id);
// the external_id→bigint cast is safe (surveyed: zero non-numeric exact bgm
// work anchors — the 69 verification). Limit/Offset window the distinct-work
// list in Go (the dlsitemedia chunking discipline).
func loadBgmCandidates(ctx context.Context, db *gorm.DB, reg registry, limit, offset int) ([]bgmCandidate, error) {
	var out []bgmCandidate
	if err := db.WithContext(ctx).
		Raw(`SELECT DISTINCT ON (w.id) w.id AS work_id, r.external_id::bigint AS subject_id,
				w.site AS site, sub.score AS score, sub.rank AS rank, sub.score_details AS score_details
			FROM catalog_work w
			JOIN catalog_external_ref r ON r.entity_type = ? AND r.entity_id = w.id
				AND r.source_id = ? AND r.link_kind = ?
			JOIN src_bangumi.subject sub ON sub.id = r.external_id::bigint
			WHERE w.medium_id = ? AND (w.site IS NULL OR w.site = '') AND w.deleted_at IS NULL
			ORDER BY w.id, r.external_id`,
			model.EntityTypeWork, reg.bangumiSource, model.LinkKindExact, reg.galgameMedium).
		Scan(&out).Error; err != nil {
		return nil, err
	}
	return window(out, limit, offset), nil
}

// egCandidate is one bodyless galgame work with its EG exact work anchors
// (several when a work carries >1 EG exact anchor — pickBest collapses them).
type egCandidate struct {
	WorkID int64
	Site   *string
	EgIDs  []int64
}

// loadEgCandidates resolves bodyless galgame works carrying EG EXACT work
// anchors — cmd/enrich-eg-scores' anchor query with the claimed-only filter
// (site='galgame_wiki' AND product_work_id IS NOT NULL) INVERTED to
// bodyless-only. A non-numeric external_id becomes the -1 sentinel so it is
// counted missing_in_mirror rather than silently dropped (same tool's
// convention). Grouped one candidate per work, ordered by work id;
// Limit/Offset window the per-work list in Go.
func loadEgCandidates(ctx context.Context, db *gorm.DB, reg registry, limit, offset int) ([]egCandidate, error) {
	var rows []struct {
		WorkID     int64   `gorm:"column:work_id"`
		Site       *string `gorm:"column:site"`
		ExternalID string  `gorm:"column:external_id"`
	}
	if err := db.WithContext(ctx).
		Raw(`SELECT w.id AS work_id, w.site AS site, r.external_id AS external_id
			FROM catalog_external_ref r
			JOIN catalog_work w ON w.id = r.entity_id
			WHERE r.entity_type = ? AND r.source_id = ? AND r.link_kind = ?
				AND w.medium_id = ? AND (w.site IS NULL OR w.site = '') AND w.deleted_at IS NULL
			ORDER BY w.id, r.external_id`,
			model.EntityTypeWork, reg.egSource, model.LinkKindExact, reg.galgameMedium).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	byWork := map[int64]*egCandidate{}
	var order []int64
	for _, r := range rows {
		egID, err := strconv.ParseInt(r.ExternalID, 10, 64)
		if err != nil {
			egID = -1
		}
		c := byWork[r.WorkID]
		if c == nil {
			c = &egCandidate{WorkID: r.WorkID, Site: r.Site}
			byWork[r.WorkID] = c
			order = append(order, r.WorkID)
		}
		c.EgIDs = append(c.EgIDs, egID)
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	out := make([]egCandidate, 0, len(order))
	for _, id := range order {
		out = append(out, *byWork[id])
	}
	return window(out, limit, offset), nil
}

// egData is one EG mirror games row (median nullable — NULL vs a real low 0
// must stay distinct; count2 → votes, 0 when NULL).
type egData struct {
	median *int
	votes  int
}

// loadEGMirror batch-loads the EG mirror games rows for the referenced ids.
func loadEGMirror(ctx context.Context, egDB *gorm.DB, ids []int64) (map[int64]egData, error) {
	out := map[int64]egData{}
	type row struct {
		ID     int64 `gorm:"column:id"`
		Median *int  `gorm:"column:median"`
		Count2 *int  `gorm:"column:count2"`
	}
	for start := 0; start < len(ids); start += 1000 {
		end := min(start+1000, len(ids))
		var batch []row
		if err := egDB.WithContext(ctx).Table("games").Select("id, median, count2").
			Where("id IN ?", ids[start:end]).Scan(&batch).Error; err != nil {
			return nil, err
		}
		for _, r := range batch {
			votes := 0
			if r.Count2 != nil {
				votes = *r.Count2
			}
			out[r.ID] = egData{median: r.Median, votes: votes}
		}
	}
	return out, nil
}

// pickBest chooses the representative EG game for a work with several exact
// anchors: most-voted wins (biggest count2 = most reliable median), tie-break
// higher median, then id; ids absent from the mirror sort last — the
// cmd/enrich-eg-scores rule verbatim.
func pickBest(egIDs []int64, mirror map[int64]egData) int64 {
	best := egIDs[0]
	for _, id := range egIDs[1:] {
		if worse(best, id, mirror) {
			best = id
		}
	}
	return best
}

// worse reports whether a is a WORSE representative than b.
func worse(a, b int64, mirror map[int64]egData) bool {
	da, oka := mirror[a]
	db, okb := mirror[b]
	if oka != okb {
		return !oka // present beats absent
	}
	if !oka {
		return a < b
	}
	if da.votes != db.votes {
		return da.votes < db.votes
	}
	ma, mb := derefOr(da.median, -1), derefOr(db.median, -1)
	if ma != mb {
		return ma < mb
	}
	return a < b
}

func derefOr(p *int, fallback int) int {
	if p == nil {
		return fallback
	}
	return *p
}

// ratingTotal sums the score_details buckets ({"1": n, …, "10": n}) — the dump
// has NO total field, so the vote count is derived exactly like
// galgame_bangumi_meta.total (bangumienrich.ratingTotal).
func ratingTotal(details []byte) int {
	if len(details) == 0 {
		return 0
	}
	var buckets map[string]int
	if err := json.Unmarshal(details, &buckets); err != nil {
		return 0
	}
	total := 0
	for _, n := range buckets {
		total += n
	}
	return total
}

// window applies the offset/limit chunking to an already-distinct candidate
// list (slicing keeps it obviously correct — the bgmsummaries discipline).
func window[T any](in []T, limit, offset int) []T {
	if offset > 0 {
		if offset >= len(in) {
			return nil
		}
		in = in[offset:]
	}
	if limit > 0 && limit < len(in) {
		in = in[:limit]
	}
	return in
}

func keysOf(m map[int64]bool) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
