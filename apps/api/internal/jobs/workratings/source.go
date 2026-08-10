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

type bgmCandidate struct {
	WorkID       int64   `gorm:"column:work_id"`
	SubjectID    int64   `gorm:"column:subject_id"`
	Score        float64 `gorm:"column:score"`
	Rank         int     `gorm:"column:rank"`
	ScoreDetails []byte  `gorm:"column:score_details"`
}

func loadBgmCandidates(ctx context.Context, db *gorm.DB, reg registry, limit, offset int) ([]bgmCandidate, error) {
	var out []bgmCandidate
	if err := db.WithContext(ctx).
		Raw(`SELECT DISTINCT ON (w.id) w.id AS work_id, r.external_id::bigint AS subject_id,
				sub.score AS score, sub.rank AS rank, sub.score_details AS score_details
			FROM catalog_work w
			JOIN catalog_external_ref r ON r.entity_type = ? AND r.entity_id = w.id
				AND r.source_id = ? AND r.link_kind = ?
			JOIN src_bangumi.subject sub ON sub.id = r.external_id::bigint
			WHERE w.medium_id = ? AND w.deleted_at IS NULL
			ORDER BY w.id, r.external_id`,
			model.EntityTypeWork, reg.bangumiSource, model.LinkKindExact, reg.galgameMedium).
		Scan(&out).Error; err != nil {
		return nil, err
	}
	return window(out, limit, offset), nil
}

type egCandidate struct {
	WorkID int64
	EgIDs  []int64
}

func loadEgCandidates(ctx context.Context, db *gorm.DB, reg registry, limit, offset int) ([]egCandidate, error) {
	var rows []struct {
		WorkID     int64   `gorm:"column:work_id"`
		Site       *string `gorm:"column:site"`
		ExternalID string  `gorm:"column:external_id"`
	}
	if err := db.WithContext(ctx).
		Raw(`SELECT w.id AS work_id, r.external_id AS external_id
			FROM catalog_external_ref r
			JOIN catalog_work w ON w.id = r.entity_id
			WHERE r.entity_type = ? AND r.source_id = ? AND r.link_kind = ?
				AND w.medium_id = ? AND w.deleted_at IS NULL
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
			c = &egCandidate{WorkID: r.WorkID}
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

type egData struct {
	median *int
	votes  int
}

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

func pickBest(egIDs []int64, mirror map[int64]egData) int64 {
	best := egIDs[0]
	for _, id := range egIDs[1:] {
		if worse(best, id, mirror) {
			best = id
		}
	}
	return best
}

func worse(a, b int64, mirror map[int64]egData) bool {
	da, oka := mirror[a]
	db, okb := mirror[b]
	if oka != okb {
		return !oka
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
