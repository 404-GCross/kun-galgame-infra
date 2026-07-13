package portraitfill

import (
	"context"
	stderrors "errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"api/internal/platform/galgame/vndb"

	"gorm.io/gorm"
)

// loadDims reads (hash -> [w,h]) for every non-deleted image. The set is small
// enough (~160k) to hold in memory and lets candidate resolution decide
// portrait-ness of existing covers without a cross-database join.
func loadDims(ctx context.Context, db *gorm.DB) (map[string][2]int64, error) {
	type row struct {
		Hash   string
		Width  int64
		Height int64
	}
	var rows []row
	if err := db.WithContext(ctx).Raw(
		"SELECT hash, width, height FROM images WHERE deleted_at IS NULL").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string][2]int64, len(rows))
	for _, r := range rows {
		out[strings.TrimSpace(r.Hash)] = [2]int64{r.Width, r.Height}
	}
	return out, nil
}

// loadBangumiAnchors reads {catalog_work id → Bangumi bid} from the catalog
// EXACT anchor face: catalog_external_ref rows with source_id=3 (bangumi),
// entity_type=5 (work), link_kind=0 (exact). This is the true bid source (the
// wiki galgame.bid column is a stale snapshot); a candidate's bid is the anchor
// on the work its galgame claims (galgame.catalog_work_id = entity_id).
func loadBangumiAnchors(ctx context.Context, catalogDB *gorm.DB) (map[int64]int, error) {
	type row struct {
		EntityID   int64  `gorm:"column:entity_id"`
		ExternalID string `gorm:"column:external_id"`
	}
	var rows []row
	if err := catalogDB.WithContext(ctx).Raw(
		`SELECT entity_id, external_id FROM catalog_external_ref
		  WHERE source_id = 3 AND entity_type = 5 AND link_kind = 0`).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]int, len(rows))
	for _, r := range rows {
		if bid, err := strconv.Atoi(strings.TrimSpace(r.ExternalID)); err == nil && bid > 0 {
			out[r.EntityID] = bid
		}
	}
	return out, nil
}

// coverRow is one (game, existing cover) pairing feeding candidate resolution.
// CatalogWorkID resolves the Bangumi bid via the catalog exact anchor (T2) —
// the wiki galgame.bid column is a stale 5,313-row snapshot, so it is no longer
// the bid source.
type coverRow struct {
	ID            int
	VndbID        string `gorm:"column:vndb_id"`
	CatalogWorkID *int64 `gorm:"column:catalog_work_id"`
	ImageHash     string `gorm:"column:image_hash"`
}

// candidates lists games (VNDB-anchored, status Published/VNDBDraft, at least
// one existing cover, no portrait cover), after offset/limit. bidByWork maps a
// catalog_work id → Bangumi bid (catalog bangumi EXACT anchor); it decides each
// candidate's BID for the Bangumi fallback pass.
//
// Ordering is a deterministic spread (md5 of the id) rather than raw id, so a
// --limit slice is a representative random sample of the whole population
// instead of the id-ordered head (the low ids are the oldest hand-made entries,
// almost all landscape). All cover rows of one game share the md5 key so they
// stay contiguous for the streaming grouping below. A full run is unaffected;
// --offset chunking stays stable because the total order is deterministic.
func candidates(ctx context.Context, db *gorm.DB, dims map[string][2]int64, bidByWork map[int64]int, opts Opts) ([]candidate, error) {
	var rows []coverRow
	if err := db.WithContext(ctx).Raw(
		`SELECT g.id, g.vndb_id, g.catalog_work_id, c.image_hash
		   FROM galgame g JOIN galgame_cover c ON c.galgame_id = g.id
		  WHERE g.vndb_id ~ '^v[0-9]+$' AND g.status IN (0,2)
		  ORDER BY md5(g.id::text), g.id`).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := groupNoPortrait(rows, dims, bidByWork)

	if opts.Offset > 0 {
		if opts.Offset >= len(out) {
			return nil, nil
		}
		out = out[opts.Offset:]
	}
	if opts.Limit > 0 && opts.Limit < len(out) {
		out = out[:opts.Limit]
	}
	return out, nil
}

// groupNoPortrait folds id-ordered (game, cover) rows into the candidate list:
// one entry per game whose covers are ALL non-portrait. Pure (no DB) so the
// grouping + portrait rule are unit-testable. BID is resolved from the catalog
// bangumi exact anchor (bidByWork keyed by catalog_work_id); 0 when unanchored.
func groupNoPortrait(rows []coverRow, dims map[string][2]int64, bidByWork map[int64]int) []candidate {
	var out []candidate
	var cur candidate
	curSet := false
	hasPortrait := false
	flush := func() {
		if curSet && !hasPortrait {
			out = append(out, cur)
		}
	}
	for _, rr := range rows {
		if !curSet || rr.ID != cur.ID {
			flush()
			bid := 0
			if rr.CatalogWorkID != nil {
				bid = bidByWork[*rr.CatalogWorkID]
			}
			cur = candidate{ID: rr.ID, VNDBID: rr.VndbID, BID: bid}
			curSet = true
			hasPortrait = false
		}
		if d, ok := dims[strings.TrimSpace(rr.ImageHash)]; ok && isPortrait(d[0], d[1]) {
			hasPortrait = true
		}
	}
	flush()
	return out
}

// fetchVNDBImages fetches vn.image for all candidates in batches of 100. A batch
// that keeps failing after a few retries (transient TLS/network hiccup over a
// long run) is skipped and its ids are returned in failedIDs so the forecast can
// report them honestly rather than mis-counting them as "no image". Only a
// context cancellation returns an error.
func fetchVNDBImages(ctx context.Context, cands []candidate, gap time.Duration) (meta map[string]vndb.VNImage, failedIDs map[string]bool, err error) {
	vc := vndb.New(gap)
	meta = make(map[string]vndb.VNImage, len(cands))
	failedIDs = map[string]bool{}
	ids := make([]string, 0, len(cands))
	for _, c := range cands {
		ids = append(ids, c.VNDBID)
	}
	batches := chunk(ids, batchSize)
	for bi, b := range batches {
		if e := ctx.Err(); e != nil {
			return meta, failedIDs, e
		}
		m, e := fetchBatchWithRetry(ctx, vc, b)
		if e != nil {
			if stderrors.Is(e, context.Canceled) || stderrors.Is(e, context.DeadlineExceeded) {
				return meta, failedIDs, e
			}
			slog.Warn("vndb image batch failed after retries -- skipping", "batch", bi, "size", len(b), "err", e)
			for _, id := range b {
				failedIDs[id] = true
			}
			continue
		}
		for k, v := range m {
			meta[k] = v
		}
		if bi%50 == 0 {
			slog.Info("vndb image fetch progress", "batch", bi, "of", len(batches), "fetched", len(meta))
		}
	}
	return meta, failedIDs, nil
}

// fetchBatchWithRetry retries one batch up to 4 attempts with a short backoff,
// riding out transient network errors on a multi-minute run.
func fetchBatchWithRetry(ctx context.Context, vc *vndb.Client, ids []string) (map[string]vndb.VNImage, error) {
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(time.Duration(attempt) * 3 * time.Second):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		m, err := vc.FetchVNImagesBatch(ctx, ids)
		if err == nil {
			return m, nil
		}
		lastErr = err
	}
	return nil, lastErr
}
