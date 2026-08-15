package workratings

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"api/internal/platform/catalog/model"

	"gorm.io/gorm"
)

type vndbCandidate struct {
	WorkID  int64
	VndbIDs []string
}

func loadVndbCandidates(ctx context.Context, db *gorm.DB, reg registry, limit, offset int) ([]vndbCandidate, error) {
	var rows []struct {
		WorkID     int64  `gorm:"column:work_id"`
		ExternalID string `gorm:"column:external_id"`
	}
	if err := db.WithContext(ctx).
		Raw(`SELECT w.id AS work_id, r.external_id AS external_id
			FROM catalog_external_ref r
			JOIN catalog_work w ON w.id = r.entity_id
			WHERE r.entity_type = ? AND r.source_id = ? AND r.link_kind = ?
				AND w.medium_id = ? AND w.deleted_at IS NULL
			ORDER BY w.id, r.external_id`,
			model.EntityTypeWork, reg.vndbSource, model.LinkKindExact, reg.galgameMedium).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	byWork := map[int64]*vndbCandidate{}
	var order []int64
	for _, r := range rows {
		c := byWork[r.WorkID]
		if c == nil {
			c = &vndbCandidate{WorkID: r.WorkID}
			byWork[r.WorkID] = c
			order = append(order, r.WorkID)
		}
		c.VndbIDs = append(c.VndbIDs, r.ExternalID)
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	out := make([]vndbCandidate, 0, len(order))
	for _, id := range order {
		out = append(out, *byWork[id])
	}
	return window(out, limit, offset), nil
}

type vndbData struct {
	rating  *float64
	average *float64
	votes   int
}

// c_rating and c_average are both stored ×100 (787 = 7.87). c_rating is VNDB's
// headline number and the one the frozen 2026-07 rows were written from — 17,400
// of 20,890 match it against 2,761 for c_average, the rest being drift since.
func loadVndbMirror(ctx context.Context, db *gorm.DB, ids []string) (map[string]vndbData, error) {
	out := map[string]vndbData{}
	type row struct {
		ID      string   `gorm:"column:id"`
		Rating  *float64 `gorm:"column:rating"`
		Average *float64 `gorm:"column:average"`
		Votes   int      `gorm:"column:votes"`
	}
	for start := 0; start < len(ids); start += 1000 {
		end := min(start+1000, len(ids))
		var batch []row
		if err := db.WithContext(ctx).Table("src_vndb.vn").
			Select(`id, c_rating / 100.0 AS rating, c_average / 100.0 AS average, c_votecount AS votes`).
			Where("id IN ?", ids[start:end]).Scan(&batch).Error; err != nil {
			return nil, err
		}
		for _, r := range batch {
			out[r.ID] = vndbData{rating: r.Rating, average: r.Average, votes: r.Votes}
		}
	}
	return out, nil
}

func loadVndbVoteStats(ctx context.Context, db *gorm.DB) (map[string][]byte, error) {
	var staged bool
	if err := db.WithContext(ctx).
		Raw(`SELECT to_regclass('src_vndb.vn_vote_stats') IS NOT NULL`).Scan(&staged).Error; err != nil {
		return nil, err
	}
	if !staged {
		slog.Warn("src_vndb.vn_vote_stats is not staged — vndb ratings will refresh without histograms; " +
			"run ingest-vndb --votes-file <vndb-votes-latest.gz>")
		return nil, nil
	}
	var rows []struct {
		ID           string `gorm:"column:id"`
		Distribution []byte `gorm:"column:distribution"`
	}
	if err := db.WithContext(ctx).
		Raw(`SELECT id, distribution FROM src_vndb.vn_vote_stats WHERE total > 0`).Scan(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		slog.Warn("src_vndb.vn_vote_stats is empty — vndb ratings will refresh without histograms; " +
			"run ingest-vndb --votes-file <vndb-votes-latest.gz>")
		return nil, nil
	}
	out := make(map[string][]byte, len(rows))
	for _, r := range rows {
		out[r.ID] = r.Distribution
	}
	return out, nil
}

func vndbPickBest(ids []string, mirror map[string]vndbData) string {
	best := ids[0]
	for _, id := range ids[1:] {
		if vndbWorse(best, id, mirror) {
			best = id
		}
	}
	return best
}

func vndbWorse(a, b string, mirror map[string]vndbData) bool {
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
	ra, rb := derefOrF(da.rating, -1), derefOrF(db.rating, -1)
	if ra != rb {
		return ra < rb
	}
	return a < b
}

func derefOrF(p *float64, fallback float64) float64 {
	if p == nil {
		return fallback
	}
	return *p
}

func runVndbLane(ctx context.Context, db *gorm.DB, w *writer, reg registry, opts Opts) error {
	cands, err := loadVndbCandidates(ctx, db, reg, opts.Limit, opts.Offset)
	if err != nil {
		return fmt.Errorf("load VNDB candidates: %w", err)
	}
	st := w.stats
	st.VndbCandidates = len(cands)
	if len(cands) == 0 {
		return nil
	}

	idSet := map[string]bool{}
	for _, c := range cands {
		for _, id := range c.VndbIDs {
			idSet[id] = true
		}
	}
	ids := make([]string, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	mirror, err := loadVndbMirror(ctx, db, ids)
	if err != nil {
		return fmt.Errorf("load VNDB mirror vn: %w", err)
	}
	votes, err := loadVndbVoteStats(ctx, db)
	if err != nil {
		return fmt.Errorf("load VNDB vote stats: %w", err)
	}

	for _, c := range cands {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		chosen := vndbPickBest(c.VndbIDs, mirror)
		if len(c.VndbIDs) > 1 {
			st.VndbMultiAnchor += len(c.VndbIDs) - 1
		}
		vn, ok := mirror[chosen]
		if !ok {
			st.VndbMissingMirror++
			continue
		}
		if vn.rating == nil || vn.votes <= 0 {
			st.VndbNoScore++
			continue
		}
		stats := marshalStats(ratingStats{Average: vn.average})
		if stats != nil {
			st.VndbStats++
		}
		dist := votes[chosen]
		if len(dist) > 0 {
			st.VndbDistribution++
		} else {
			dist = nil
			st.VndbNoVotes++
		}
		st.VndbPlanned++
		collect(&st.VndbSamples, Sample{WorkID: c.WorkID, VndbID: chosen, Score: *vn.rating, VoteCount: vn.votes})
		w.write(ctx, plannedRow{
			WorkID: c.WorkID, SourceID: reg.vndbSource,
			Score: *vn.rating, VoteCount: vn.votes, Stats: stats, Distribution: dist,
		}, opts.Apply, &st.VndbWritten, &st.VndbUnchanged)
	}
	return nil
}
