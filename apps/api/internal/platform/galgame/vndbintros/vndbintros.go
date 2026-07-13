// Package vndbintros backfills the ENGLISH intro of vndb-anchored galgames from
// VNDB's own description — the second content-补缺 flow after bangumienrich (which
// fills the Chinese intro from Bangumi). Deliberately conservative, mirroring
// bangumienrich's fill-only-when-empty contract: a galgame whose intro_en_us is
// already non-empty — user- OR any-source-authored — is NEVER touched (it is not
// even a candidate). VNDB descriptions are BBCode, so every value is run through
// intronorm (the same VNDB-BBCode → wiki-Markdown normalizer the one-off cleanup
// cmd uses) before it lands; a description that normalizes to empty (a bare
// attribution line, an image-only blurb) writes nothing.
//
// Write discipline is APPROACH B (bangumienrich / normalize-galgame-intro): for
// every filled galgame one transaction UPDATEs the live intro_en_us AND
// jsonb-patches the LATEST revision's snapshot so live == latest snapshot stays
// true — WITHOUT minting a new revision (a bulk backfill must not create
// thousands of revisions). Dry-run is the default; --apply opts into writes.
// intro_en_us is a Meili-indexed field, so an applied run should be followed by
// `reindex-search --index=galgames` to surface the new intros in search.
package vndbintros

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"api/internal/platform/galgame/intronorm"

	"gorm.io/gorm"
)

const batchSize = 100

// Fetcher returns VNDB descriptions for a batch of vndb ids, keyed by id. An id
// ABSENT from the result = VNDB doesn't return that VN (deleted / merged /
// fabricated) → the caller counts it as api_missing; a present id mapping to ""
// = the VN exists but has no description → no_description. This is the exact
// shape of vndb.Client.FetchVNDescriptionsBatch, injected so the fill guard, the
// intronorm wiring, and idempotency are unit-testable without the network.
type Fetcher func(ctx context.Context, ids []string) (map[string]string, error)

// Options controls a run.
type Options struct {
	DryRun bool
	Limit  int
}

// Stats is the run summary. The accounting is fully self-proving:
//
//	Candidates = Filled + NoDescription + SkippedEmptyAfterNorm + APIMissing + Failed
//	Fetched    = Candidates − APIMissing − <fetch failures>   (== Candidates − APIMissing when Failed == 0)
//
// so in a clean run (Failed == 0): Candidates = Fetched + APIMissing and
// Fetched = Filled + NoDescription + SkippedEmptyAfterNorm.
type Stats struct {
	Candidates            int `json:"candidates"`
	Fetched               int `json:"fetched"`
	Filled                int `json:"filled"`
	NoDescription         int `json:"no_description"`
	SkippedEmptyAfterNorm int `json:"skipped_empty_after_norm"`
	APIMissing            int `json:"api_missing"`
	Failed                int `json:"failed"`
}

type candidate struct {
	ID     int
	VNDBID string `gorm:"column:vndb_id"`
}

// Run fills intro_en_us from VNDB descriptions for every vndb-anchored galgame
// whose English intro is empty, and returns a summary. fetch supplies the
// descriptions (see Fetcher); DryRun fetches + counts but writes nothing.
func Run(ctx context.Context, wikiDB *gorm.DB, fetch Fetcher, opts Options) (*Stats, error) {
	stats := &Stats{}

	// Candidate set = the fill guard: a canonical vndb_id AND an EMPTY
	// intro_en_us. Any non-empty English intro (user- or any-source-authored)
	// is excluded here and can never be overwritten. The regex both drops the
	// empty value and matches only the canonical form (mirrors the DB CHECK).
	q := wikiDB.Table("galgame").
		Select("id, vndb_id").
		Where("vndb_id ~ '^v[0-9]+$' AND intro_en_us = ''").
		Order("id")
	if opts.Limit > 0 {
		q = q.Limit(opts.Limit)
	}
	var cands []candidate
	if err := q.Scan(&cands).Error; err != nil {
		return nil, fmt.Errorf("list candidates: %w", err)
	}
	stats.Candidates = len(cands)
	slog.Info("enrich-vndb-intros start", "candidates", stats.Candidates, "batch", batchSize, "apply", !opts.DryRun)

	for start := 0; start < len(cands); start += batchSize {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		end := min(start+batchSize, len(cands))
		batch := cands[start:end]

		ids := make([]string, 0, len(batch))
		for _, c := range batch {
			ids = append(ids, c.VNDBID)
		}
		// One /vn per 100 VNs. On error (VNDB down, a rare out-of-range id),
		// count the batch as failed and move on — a rerun picks it up (the
		// candidate set is self-healing: filled rows drop out, unfilled stay).
		descs, err := fetch(ctx, ids)
		if err != nil {
			stats.Failed += len(batch)
			slog.Error("fetch descriptions batch", "from", batch[0].ID, "to", batch[len(batch)-1].ID, "error", err)
			continue
		}

		for _, c := range batch {
			desc, ok := descs[c.VNDBID]
			if !ok {
				stats.APIMissing++ // VNDB didn't return this VN — deleted / merged / fabricated id
				continue
			}
			stats.Fetched++
			if strings.TrimSpace(desc) == "" {
				stats.NoDescription++
				continue
			}
			// VNDB descriptions are BBCode → normalize to wiki Markdown. Never
			// bypass intronorm; never fabricate cleaning.
			cleaned, _, _ := intronorm.NormalizeEnglishIntro(desc)
			if strings.TrimSpace(cleaned) == "" {
				stats.SkippedEmptyAfterNorm++ // e.g. a bare attribution line or image-only blurb
				continue
			}
			if !opts.DryRun {
				if err := applyOne(wikiDB, c.ID, cleaned); err != nil {
					stats.Failed++
					slog.Error("apply intro", "id", c.ID, "error", err)
					continue
				}
			}
			stats.Filled++
		}
		slog.Info("progress", "processed", end, "of", len(cands), "filled", stats.Filled,
			"no_description", stats.NoDescription, "skipped_empty_after_norm", stats.SkippedEmptyAfterNorm,
			"api_missing", stats.APIMissing, "failed", stats.Failed)
	}

	slog.Info("enrich-vndb-intros done", "candidates", stats.Candidates, "fetched", stats.Fetched,
		"filled", stats.Filled, "no_description", stats.NoDescription,
		"skipped_empty_after_norm", stats.SkippedEmptyAfterNorm, "api_missing", stats.APIMissing, "failed", stats.Failed)
	if opts.DryRun {
		slog.Info("DRY RUN — nothing written; re-run with --apply")
	}
	return stats, nil
}

// applyOne fills one galgame's intro_en_us in a single transaction using
// APPROACH B: the live column is UPDATEd AND the LATEST revision's snapshot is
// jsonb-patched to match, so live == latest snapshot holds without minting a new
// revision. A galgame with no revisions simply skips the patch (0 rows updated).
func applyOne(db *gorm.DB, galgameID int, intro string) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`UPDATE galgame SET intro_en_us = ? WHERE id = ?`, intro, galgameID).Error; err != nil {
			return err
		}
		return tx.Exec(`
			UPDATE galgame_revision
			SET snapshot = jsonb_set(snapshot, '{intro_en_us}', to_jsonb(?::text), true)
			WHERE galgame_id = ?
			  AND revision = (SELECT max(revision) FROM galgame_revision WHERE galgame_id = ?)
		`, intro, galgameID, galgameID).Error
	})
}
