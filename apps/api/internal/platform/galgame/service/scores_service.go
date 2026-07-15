package service

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/repository"
)

// Cross-source read face (step 34): three-source per-game scores and the
// site-wide stats overview, both served from the materialized narrow tables /
// galgame_stats snapshot. The P-★ narrow slice = the backend composes each
// source's attribution URL (00-workflow §10); the frontend renders the credit.

// Attribution URL templates. EG uses the canonical game-statistics page on the
// erogamescape site (the same host the mirror is a copy of).
const (
	vndbURLBase    = "https://vndb.org/"
	bangumiURLBase = "https://bgm.tv/subject/"
	egURLBase      = "https://erogamescape.dyndns.org/~ap2/ero/toukei_kaiseki/game.php?game="
)

// GetScores returns the three-source scores for one galgame. Each source is nil
// when no narrow-table row anchors it; a present source with an unrated value
// keeps its score field nil (vote counts pass through). synced_at is the
// freshest synced_at across the present sources (nil when none). A galgame with
// no rows at all yields all-null — never an error, no visibility gate (external
// score indicators are display-only).
func (s *GalgameService) GetScores(ctx context.Context, gid int) (dto.GalgameScores, error) {
	sm, err := s.galgameRepo.LoadScoreMeta(ctx, gid)
	if err != nil {
		return dto.GalgameScores{}, err
	}
	return buildGalgameScores(sm), nil
}

// buildGalgameScores maps a loaded ScoreMeta to the frozen GalgameScores shape
// (each source null when unanchored, present-with-null-value when anchored but
// unrated). Shared by GET /galgame/:gid/scores and the open-API public detail's
// embedded scores block so the two never drift.
func buildGalgameScores(sm repository.ScoreMeta) dto.GalgameScores {
	var scores dto.GalgameScores
	var latest time.Time

	track := func(t time.Time) {
		if t.After(latest) {
			latest = t
		}
	}

	if m := sm.VNDB; m != nil {
		scores.VNDB = &dto.VNDBScore{
			Rating:    m.Rating,
			VoteCount: m.VoteCount,
			URL:       vndbURLBase + sm.VNDBID,
		}
		track(m.SyncedAt)
	}
	if m := sm.Bangumi; m != nil {
		// Bangumi stores 0 for "unrated"; surface it as a null score so the null
		// semantics match VNDB/EG and the stats score>0 predicate.
		var score *float64
		if m.Score > 0 {
			v := m.Score
			score = &v
		}
		scores.Bangumi = &dto.BangumiScore{
			Score: score,
			Rank:  m.Rank,
			Total: m.Total,
			URL:   bangumiURLBase + strconv.Itoa(m.BID),
		}
		track(m.SyncedAt)
	}
	if m := sm.EG; m != nil {
		scores.EG = &dto.EGScore{
			Median:    m.Median,
			VoteCount: m.VoteCount,
			URL:       egURLBase + strconv.Itoa(m.EGGameID),
		}
		track(m.SyncedAt)
	}
	if !latest.IsZero() {
		syncedAt := latest.UTC().Format(time.RFC3339)
		scores.SyncedAt = &syncedAt
	}

	return scores
}

// statsKeys maps each galgame_stats key (the frozen v1 contract) to the
// GalgameStatsData field it fills. Kept literal here so the read face owns its
// contract independently of the build package's unexported constants.
func statsField(key string, data *dto.GalgameStatsData) *dto.GalgameStatsEntry {
	switch key {
	case "release_years":
		return &data.ReleaseYears
	case "score_histogram_vndb":
		return &data.ScoreHistogramVNDB
	case "score_histogram_bangumi":
		return &data.ScoreHistogramBangumi
	case "score_histogram_eg":
		return &data.ScoreHistogramEG
	case "yearly_scores":
		return &data.YearlyScores
	case "coverage":
		return &data.Coverage
	default:
		return nil
	}
}

// GetStats returns the six stats snapshots verbatim (payload + per-key built_at)
// plus the overall built_at = max(built_at), which the handler folds into the
// ETag. Unknown / missing keys are ignored (the build job always writes the six
// v1 keys); an empty table yields empty entries and a zero built_at.
func (s *GalgameService) GetStats(ctx context.Context) (dto.GalgameStatsData, time.Time, error) {
	rows, err := s.galgameRepo.LoadAllStats(ctx)
	if err != nil {
		return dto.GalgameStatsData{}, time.Time{}, err
	}

	var data dto.GalgameStatsData
	var maxBuilt time.Time
	for i := range rows {
		row := &rows[i]
		entry := statsField(row.Key, &data)
		if entry == nil {
			continue // ignore any key outside the v1 set
		}
		builtAt := row.BuiltAt.UTC().Format(time.RFC3339)
		*entry = dto.GalgameStatsEntry{Payload: json.RawMessage(row.Payload), BuiltAt: builtAt}
		if row.BuiltAt.After(maxBuilt) {
			maxBuilt = row.BuiltAt
		}
	}
	if !maxBuilt.IsZero() {
		data.BuiltAt = maxBuilt.UTC().Format(time.RFC3339)
	}

	return data, maxBuilt, nil
}
