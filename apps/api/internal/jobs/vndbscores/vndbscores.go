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
package vndbscores

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"api/internal/infrastructure/database"
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

	cands, err := candidates(db.DB(), opts)
	if err != nil {
		return nil, fmt.Errorf("list candidates: %w", err)
	}
	slog.Info("sync-vndb-scores start", "candidates", len(cands), "batch", batchSize,
		"apply", opts.Apply, "gap", opts.Gap.String())

	vc := vndb.New(opts.Gap)
	var written, zeroVote, apiMissing, failed int
	for start := 0; start < len(cands); start += batchSize {
		if err := ctx.Err(); err != nil {
			return summary(len(cands), written, zeroVote, apiMissing, failed), err
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

		now := time.Now()
		for _, c := range batch {
			s, ok := scores[c.VNDBID]
			if !ok {
				apiMissing++ // VNDB didn't return this VN — deleted / merged / fabricated id
				continue
			}
			// Zero votes => no real score. Store NULL, never a fake zero.
			rating := s.Rating
			if s.VoteCount == 0 {
				rating = nil
			}
			if rating == nil {
				zeroVote++
			}
			if opts.Apply {
				if err := upsert(db.DB(), model.GalgameVNDBMeta{
					GalgameID: c.ID,
					VNDBID:    c.VNDBID,
					Rating:    rating,
					VoteCount: s.VoteCount,
					SyncedAt:  now,
				}); err != nil {
					failed++
					slog.Error("upsert score", "id", c.ID, "error", err)
					continue
				}
			}
			written++
		}
		slog.Info("progress", "processed", end, "of", len(cands),
			"written", written, "zero_vote", zeroVote, "api_missing", apiMissing, "failed", failed)
	}

	slog.Info("sync-vndb-scores done", "candidates", len(cands), "written", written,
		"zero_vote", zeroVote, "api_missing", apiMissing, "failed", failed, "applied", opts.Apply)
	if !opts.Apply {
		slog.Info("DRY RUN — nothing written; re-run with Apply")
	}
	return summary(len(cands), written, zeroVote, apiMissing, failed), nil
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

func summary(candidates, written, zeroVote, apiMissing, failed int) map[string]any {
	return map[string]any{
		"candidates":  candidates,
		"written":     written,
		"zero_vote":   zeroVote,
		"api_missing": apiMissing,
		"failed":      failed,
	}
}
