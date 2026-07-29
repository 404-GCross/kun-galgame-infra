// Package vndbscores is the shared logic behind the sync-vndb-scores CLI and the
// scheduled "sync-vndb-scores" job: ingest each vndb-anchored galgame's VNDB
// rating + votecount into the galgame_vndb_meta narrow table (one /vn per 100
// VNs, kana API). Mirrors the vndbenrich structure. Third-party rating data is
// source-owned and kept out of the main galgame table — whether/where it is
// displayed is a later product decision (like galgame_bangumi_meta).
//
// Semantics: a VN with zero votes (VNDB returns rating:null) writes a row with
// Rating=NULL (never a fake zero); a VN VNDB doesn't return (deleted / merged /
// a fabricated vndb_id) writes NO row and is counted as api_missing. The upsert
// keys on galgame_id so re-runs update in place — no row growth.
//
// Changes-feed stamping (refs/proj/119) splits those two facts apart:
//
//   - synced_at is refreshed for EVERY successfully fetched row, unconditionally.
//     It is the "last refreshed" watermark and must never drift into meaning
//     "last changed".
//   - catalog_work.updated_at is bumped ONLY when (rating, vote_count) really
//     moved, or the row is new. Stamping on synced_at instead would push all
//     62k claimed works through GET /v1/catalog/changes every single night and
//     the feed would stop being an incremental feed at all.
package vndbscores

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"api/internal/infrastructure/database"
	"api/internal/jobs/galgametouch"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/vndb"
	"api/pkg/config"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const batchSize = 100

// Opts selects which galgames get their VNDB scores refreshed.
type Opts struct {
	Apply bool          // false = dry run (fetch + count, no writes)
	Gap   time.Duration // min delay between VNDB API calls (default 2s)
	Limit int           // max galgames to process (0 = all); for a quick sampled trial
}

// Run refreshes VNDB rating/votecount for every vndb-anchored galgame and
// returns a summary. apply=false fetches + counts but writes nothing.
func Run(ctx context.Context, cfg *config.Config, opts Opts) (map[string]any, error) {
	if opts.Gap <= 0 {
		opts.Gap = 2 * time.Second
	}

	db, err := database.NewPostgresDB(cfg.GalgameDatabase)
	if err != nil {
		return nil, fmt.Errorf("connect galgame db (%s): %w", cfg.GalgameDatabase.DBName, err)
	}
	defer db.Close()

	// Opened only when applying — a dry run keeps a nil Toucher, which is a
	// no-op, so a preview cannot move any watermark.
	var toucher *galgametouch.Toucher
	if opts.Apply {
		toucher, err = galgametouch.Open(cfg)
		if err != nil {
			return nil, err
		}
		defer toucher.Close()
	}

	cands, err := candidates(db.DB(), opts)
	if err != nil {
		return nil, fmt.Errorf("list candidates: %w", err)
	}
	slog.Info("sync-vndb-scores start", "candidates", len(cands), "batch", batchSize,
		"apply", opts.Apply, "gap", opts.Gap.String())

	vc := vndb.New(opts.Gap)
	ap := &applier{db: db.DB(), toucher: toucher, apply: opts.Apply}
	var written, zeroVote, apiMissing, failed int
	for start := 0; start < len(cands); start += batchSize {
		if err := ctx.Err(); err != nil {
			return summary(len(cands), written, zeroVote, apiMissing, failed, toucher.Count()), err
		}
		end := min(start+batchSize, len(cands))
		batch := cands[start:end]

		ids := make([]string, 0, len(batch))
		for _, c := range batch {
			ids = append(ids, c.VNDBID)
		}
		// One /vn per 100 VNs. On error (VNDB down, or a rare out-of-range
		// vndb_id), skip the batch and retry next run — the failed count + log
		// surface it; we don't hammer the API per-id.
		scores, err := vc.FetchVNScoresBatch(ctx, ids)
		if err != nil {
			failed += len(batch)
			slog.Error("fetch scores batch", "from", batch[0].ID, "to", batch[len(batch)-1].ID, "error", err)
			continue
		}

		res, err := ap.applyBatch(ctx, batch, scores, time.Now())
		written += res.written
		zeroVote += res.zeroVote
		apiMissing += res.apiMissing
		failed += res.failed
		if err != nil {
			// Only the pre-image read fails this way, and it happens before any
			// write — the batch is untouched, so it is wholly counted as failed
			// and retried next run.
			failed += len(batch)
			slog.Error("apply scores batch", "from", batch[0].ID, "to", batch[len(batch)-1].ID, "error", err)
		}
		slog.Info("progress", "processed", end, "of", len(cands), "written", written,
			"zero_vote", zeroVote, "api_missing", apiMissing, "failed", failed, "touched", toucher.Count())
	}

	slog.Info("sync-vndb-scores done", "candidates", len(cands), "written", written,
		"zero_vote", zeroVote, "api_missing", apiMissing, "failed", failed,
		"touched", toucher.Count(), "applied", opts.Apply)
	if !opts.Apply {
		slog.Info("DRY RUN — nothing written; re-run with Apply")
	}
	return summary(len(cands), written, zeroVote, apiMissing, failed, toucher.Count()), nil
}

// batchResult carries one batch's counter deltas back to the run loop.
type batchResult struct {
	written    int
	zeroVote   int
	apiMissing int
	failed     int
}

// applier owns the write + stamp half of a run. Split out of Run so the
// (rating, vote_count)-only stamping gate can be exercised against a real
// database without going anywhere near the VNDB API.
type applier struct {
	db      *gorm.DB
	toucher *galgametouch.Toucher // nil on a dry run
	apply   bool
}

// applyBatch upserts one batch's fetched scores and stamps the claimed works
// whose values really moved. Every successfully fetched row is upserted with a
// fresh synced_at regardless of whether anything changed; only genuine value
// movement (or a brand-new row) reaches the touch set. Rows VNDB did not return,
// and rows whose upsert failed, reach neither.
func (a *applier) applyBatch(ctx context.Context, batch []candidate, scores map[string]vndb.VNScore, now time.Time) (batchResult, error) {
	var res batchResult
	// The pre-image must be read BEFORE this batch's upserts — afterwards every
	// row compares equal to itself and nothing would ever be stamped.
	prev, err := a.previous(ctx, batch)
	if err != nil {
		return res, err
	}

	var changed []int
	for _, c := range batch {
		s, ok := scores[c.VNDBID]
		if !ok {
			res.apiMissing++ // VNDB didn't return this VN — deleted / merged / fabricated id
			continue
		}
		// Zero votes => no real score. Store NULL, never a fake zero.
		rating := s.Rating
		if s.VoteCount == 0 {
			rating = nil
		}
		if rating == nil {
			res.zeroVote++
		}
		if a.apply {
			if err := upsert(a.db, model.GalgameVNDBMeta{
				GalgameID: c.ID,
				VNDBID:    c.VNDBID,
				Rating:    rating,
				VoteCount: s.VoteCount,
				SyncedAt:  now,
			}); err != nil {
				res.failed++
				slog.Error("upsert score", "id", c.ID, "error", err)
				continue
			}
			// A committed write whose values moved — or a row that did not exist
			// before — is the only thing the feed hears about.
			if p, had := prev[c.ID]; !had || !sameRating(p.Rating, rating) || p.VoteCount != s.VoteCount {
				changed = append(changed, c.ID)
			}
		}
		res.written++
	}

	// The upserts above are already committed, so stamping per batch keeps the
	// watermarks an interrupted run has earned. A failure here is loud but not
	// fatal — the score data itself is sound.
	if err := a.toucher.Touch(ctx, changed); err != nil {
		slog.Error("touch claimed works", "error", err)
	}
	return res, nil
}

// scoreRow is the pre-image of one galgame_vndb_meta row. synced_at is
// deliberately absent: it moves on every run and would make every row look
// changed.
type scoreRow struct {
	GalgameID int      `gorm:"column:galgame_id"`
	Rating    *float64 `gorm:"column:rating"`
	VoteCount int      `gorm:"column:vote_count"`
}

// previous loads the batch's current values. A dry run never stamps, so it
// skips the query entirely rather than paying for a pre-image it cannot use.
func (a *applier) previous(ctx context.Context, batch []candidate) (map[int]scoreRow, error) {
	if !a.apply {
		return nil, nil
	}
	ids := make([]int, 0, len(batch))
	for _, c := range batch {
		ids = append(ids, c.ID)
	}
	var rows []scoreRow
	if err := a.db.WithContext(ctx).Raw(
		`SELECT galgame_id, rating, vote_count FROM galgame_vndb_meta WHERE galgame_id IN (?)`, ids).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load previous scores: %w", err)
	}
	out := make(map[int]scoreRow, len(rows))
	for _, r := range rows {
		out[r.GalgameID] = r
	}
	return out, nil
}

// sameRating compares two nullable ratings. A VN losing its last vote goes from
// a value to NULL, and gaining its first goes the other way — both are real
// changes, not no-ops.
func sameRating(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// upsert writes one score row, keyed on galgame_id so a re-run UPDATEs in place
// (idempotent — no row growth).
func upsert(db *gorm.DB, row model.GalgameVNDBMeta) error {
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "galgame_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"vndb_id", "rating", "vote_count", "synced_at"}),
	}).Create(&row).Error
}

type candidate struct {
	ID     int
	VNDBID string `gorm:"column:vndb_id"`
}

// candidates = every galgame with a canonical vndb_id (any status; scores are
// pure source metadata, unrelated to publish state). The DB CHECK already keeps
// vndb_id to either the empty string or the canonical form, so the regex both
// filters out the empty value and matches the canonical form.
func candidates(db *gorm.DB, opts Opts) ([]candidate, error) {
	q := db.Model(&model.Galgame{}).
		Select("id", "vndb_id").
		Where("vndb_id ~ '^v[0-9]+$'").
		Order("id")
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit)
	}
	var cands []candidate
	return cands, q.Scan(&cands).Error
}

func summary(candidates, written, zeroVote, apiMissing, failed, touched int) map[string]any {
	return map[string]any{
		"candidates":  candidates,
		"written":     written,
		"zero_vote":   zeroVote,
		"api_missing": apiMissing,
		"failed":      failed,
		"touched":     touched,
	}
}
