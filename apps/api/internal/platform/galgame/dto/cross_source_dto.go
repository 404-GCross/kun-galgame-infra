package dto

import "encoding/json"

// Cross-source read face (step 34): per-game three-source scores and the
// site-wide stats overview. Both are served from the materialized narrow tables
// / galgame_stats snapshot — never the mirror. The P-★ narrow slice (00-workflow
// §10) lands here as per-source attribution URLs the backend composes; the
// frontend renders the "来源名 + 链接" credit.

// VNDBScore is the VNDB rating object of GalgameScores. rating is null when the
// VN has no votes (the narrow table stores NULL, never a fake zero); the object
// itself is present whenever a galgame_vndb_meta row exists. url is the VN page.
type VNDBScore struct {
	Rating    *float64 `json:"rating"`
	VoteCount int      `json:"vote_count"`
	URL       string   `json:"url"`
}

// BangumiScore is the Bangumi rating object. score is null when the subject is
// unrated (Bangumi stores 0 for "unrated"; surfaced as null to match the stats
// score>0 predicate); rank/total pass through verbatim. url is the subject page.
type BangumiScore struct {
	Score *float64 `json:"score"`
	Rank  int      `json:"rank"`
	Total int      `json:"total"`
	URL   string   `json:"url"`
}

// EGScore is the ErogameScape rating object. median is null when EG has no
// median (0 is a real low score there, so NULL stays distinct); url is the EG
// game statistics page.
type EGScore struct {
	Median    *int   `json:"median"`
	VoteCount int    `json:"vote_count"`
	URL       string `json:"url"`
}

// GalgameScores is the body of GET /galgame/:gid/scores. Each source is null
// when no narrow-table row anchors it (zero sources → all three null); present
// with a null score field when the row exists but carries no usable rating.
// synced_at is the freshest synced_at across the present sources (null when
// none). Shape is the frozen v1 contract (naming = contract).
type GalgameScores struct {
	VNDB     *VNDBScore    `json:"vndb"`
	Bangumi  *BangumiScore `json:"bangumi"`
	EG       *EGScore      `json:"eg"`
	SyncedAt *string       `json:"synced_at"`
}

// GalgameStatsEntry is one snapshot key of the stats overview: the aggregate
// payload passed through verbatim (no reshape) plus that key's own built_at.
type GalgameStatsEntry struct {
	Payload json.RawMessage `json:"payload"`
	BuiltAt string          `json:"built_at"`
}

// GalgameStatsData is the body of GET /galgame/stats: the six frozen v1 keys,
// each carrying its verbatim payload + built_at, and the overall built_at =
// max(built_at) across keys (the ETag basis). The build job (build-galgame-stats)
// writes all six under one built_at, so they are normally equal; a per-key
// built_at is still surfaced so a partial rebuild stays observable.
type GalgameStatsData struct {
	ReleaseYears          GalgameStatsEntry `json:"release_years"`
	ScoreHistogramVNDB    GalgameStatsEntry `json:"score_histogram_vndb"`
	ScoreHistogramBangumi GalgameStatsEntry `json:"score_histogram_bangumi"`
	ScoreHistogramEG      GalgameStatsEntry `json:"score_histogram_eg"`
	YearlyScores          GalgameStatsEntry `json:"yearly_scores"`
	Coverage              GalgameStatsEntry `json:"coverage"`
	BuiltAt               string            `json:"built_at"`
}
