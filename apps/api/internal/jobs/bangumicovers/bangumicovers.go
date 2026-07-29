// Package bangumicovers backfills catalog-native PORTRAIT cover rows for
// BODYLESS galgame works from Bangumi (BGM 竖封轨 Phase 3, refs/proj/56c). It is
// the infra-side consumer of two prior phases:
//
//   - Phase 1 (56a, internal/jobs/doujinbangumi) wrote graded Bangumi anchors
//     into catalog_external_ref (source=bangumi, entity_type=work). This wave
//     reads ONLY the EXACT ones (matched_by='rule:bgm-title-year').
//   - Phase 2 (56b, the sibling repo kun-bangumi-api) fetched each anchored
//     subject's Bangumi cover into a local mirror <dir>/<subject_id>/cover.jpg
//     plus a <dir>/dims.jsonl manifest of pixel sizes.
//
// For each bodyless galgame work carrying an EXACT Bangumi anchor whose cover is
// VERTICAL (dims h > w) it uploads that cover to the catalog image scope and
// writes one catalog_work_cover row with portrait_pinned=true, source=bangumi —
// the vertical cover kungal/moyu read for the portrait-first UI. Landscape/square
// covers are skipped (DLsite already supplies the landscape cover, step 55), so
// one work can carry BOTH a DLsite landscape (portrait_pinned=false) and this
// Bangumi portrait (portrait_pinned=true); the (work_id, image_hash) unique key
// lets them coexist.
//
// Discipline, reusing the step-55 machinery (internal/jobs/dlsitemedia):
//   - Bytes ONLY ever come from the LOCAL mirror (--bangumi-mirror). This job
//     NEVER dials Bangumi.
//   - Idempotent: a work that already has a Bangumi cover is skipped before any
//     byte read; writes are ON CONFLICT DO NOTHING, so a second run writes zero.
//   - --dsn (catalog) is ALWAYS explicit — a bare run cannot touch a live DB.
//   - XOR guard (§8.D): native rows are written ONLY for bodyless works
//     (site=""); a claimed work is refused at write time.
//   - Only EXACT anchors are pinned; probable (rule:bgm-title-only) anchors stay
//     in the confirm bucket.
//   - Freshly-uploaded hashes are reference-pinged immediately (an image sits at
//     TTL from upload time; don't wait for the nightly refping).
package bangumicovers

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"api/internal/platform/catalog/repository"
	"api/pkg/config"
	"api/pkg/imageclient"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const defaultTimeout = 60 * time.Second

// Opts configures a run.
type Opts struct {
	Apply         bool          // false = dry-run forecast (no uploads, no writes)
	Limit         int           // max candidate works (0 = all)
	Offset        int           // skip this many candidate works (chunking)
	DSN           string        // catalog DSN — REQUIRED (rehearsal locally; live only in prod run)
	BangumiMirror string        // local mirror root (<dir>/<subject_id>/cover.jpg + <dir>/dims.jsonl) — REQUIRED
	ImageBaseURL  string        // image_service base override (point at the LOCAL dev service)
	UploadGap     time.Duration // min delay between uploads (0 = none; raise for the prod sweep)
}

// imageUploader is the slice of the image client this backfill needs. Narrowing
// to an interface (satisfied by *imageclient.Client) lets the write path be
// exercised with a fake in tests — no image service, no network.
type imageUploader interface {
	UploadWithSub(ctx context.Context, r io.Reader, filename, preset, uploaderSub string) (*imageclient.UploadResult, error)
	ReferencePing(ctx context.Context, hashes []string) (*imageclient.ReferencePingResult, error)
	Health(ctx context.Context) error
}

// counters tallies outcomes. In a dry run coverWould carries the forecast; in
// apply coverUploaded carries the real writes.
type counters struct {
	coverUploaded  int // portrait uploaded + row written (apply)
	coverWould     int // portrait that would upload (dry)
	coverExists    int // work already has a Bangumi cover (idempotency skip)
	coverLandscape int // skipped: dims say landscape/square (DLsite supplies it)
	coverNoDims    int // skipped: subject absent from dims.jsonl (no fetched cover)
	coverMissing   int // portrait, but the mirror file is absent/empty
	coverRejected  int // image moderation rejected the upload
	coverRefused   int // XOR guard refused a claimed work
	coverDedup     int // ON CONFLICT no-op (row already present)
	errors         int
}

// runner carries per-run dependencies + counters (serial, plain ints). exist is
// the set of works already carrying a Bangumi cover (idempotency preload).
type runner struct {
	db         *gorm.DB
	cli        imageUploader
	sourceID   int16
	gap        time.Duration
	exist      map[int64]bool
	c          counters
	pingHashes []string
	// touched collects works that actually gained a cover row, so the run bumps
	// their catalog_work.updated_at once at the end and the public changes feed
	// learns the work is worth re-pulling. Dedup hits and dry-runs contribute
	// nothing, so a second --apply moves no watermark.
	touched []int64
}

// Run resolves the candidate set, forecasts (dry) or backfills (apply) the
// portrait covers, and reference-pings freshly-uploaded hashes. Returns a
// loggable summary.
func Run(ctx context.Context, cfg *config.Config, opts Opts) (map[string]any, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn); refusing to guess — pass the rehearsal copy locally, the live catalog only in the production run")
	}
	if opts.BangumiMirror == "" {
		return nil, fmt.Errorf("--bangumi-mirror is required (local mirror root <dir>/<subject_id>/cover.jpg + <dir>/dims.jsonl)")
	}

	clientCfg := cfg.CatalogImageClient
	if opts.Apply && (clientCfg.ClientID == "" || clientCfg.ClientSecret == "") {
		return nil, fmt.Errorf("catalog image client not configured (set KUN_CATALOG_IMAGE_CLIENT_ID/SECRET); refusing to --apply cover upload")
	}

	db, err := openGorm(opts.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect catalog db: %w", err)
	}
	if sqlDB, e := db.DB(); e == nil {
		defer sqlDB.Close()
	}

	reg, err := resolveRegistry(ctx, db)
	if err != nil {
		return nil, err
	}
	cands, err := loadCandidates(ctx, db, reg, opts.Limit, opts.Offset)
	if err != nil {
		return nil, fmt.Errorf("load candidates: %w", err)
	}
	d, err := loadDims(opts.BangumiMirror)
	if err != nil {
		return nil, err
	}
	workIDs := make([]int64, len(cands))
	for i, c := range cands {
		workIDs[i] = c.WorkID
	}
	exist, err := preloadExistingCovers(ctx, db, workIDs, reg.bangumiSource)
	if err != nil {
		return nil, err
	}
	slog.Info("bangumi-covers candidates", "exact_anchors", len(cands), "dims_rows", len(d.entry),
		"apply", opts.Apply, "offset", opts.Offset, "limit", opts.Limit)

	r := &runner{db: db, sourceID: reg.bangumiSource, gap: opts.UploadGap, exist: exist}
	if opts.Apply {
		r.cli = imageclient.New(imageclient.Config{
			BaseURL:      resolveBaseURL(cfg, clientCfg, opts.ImageBaseURL),
			CDNBase:      cfg.ImageService.CDNBase,
			ClientID:     clientCfg.ClientID,
			ClientSecret: clientCfg.ClientSecret,
			Timeout:      defaultTimeout,
		})
		hctx, hcancel := context.WithTimeout(ctx, 5*time.Second)
		defer hcancel()
		if err := r.cli.Health(hctx); err != nil {
			return nil, fmt.Errorf("image_service unreachable at %s: %w", resolveBaseURL(cfg, clientCfg, opts.ImageBaseURL), err)
		}
	}

	quota := r.process(ctx, opts, cands, d)

	if err := repository.TouchWorks(ctx, r.db, r.touched); err != nil {
		return nil, fmt.Errorf("touch works: %w", err)
	}

	if opts.Apply {
		for _, b := range chunk(r.pingHashes, 1000) {
			if _, err := r.cli.ReferencePing(ctx, b); err != nil {
				slog.Warn("bangumi-covers reference-ping failed", "err", err)
			}
		}
	}

	sum := r.summary(opts, len(cands))
	slog.Info("bangumi-covers done", "summary", sum)
	if quota {
		return sum, fmt.Errorf("image quota exceeded — aborted (rerun to resume; idempotent)")
	}
	return sum, nil
}

// process walks the candidates, classifying each by its dims entry and, for the
// vertical portraits, forecasting or writing the cover. Returns quota=true if the
// image quota was hit (the run aborts).
func (r *runner) process(ctx context.Context, opts Opts, cands []candidate, d *dims) (quota bool) {
	for _, c := range cands {
		if err := ctx.Err(); err != nil {
			return false
		}
		e, ok := d.entry[c.SubjectID]
		if !ok {
			r.c.coverNoDims++ // Phase 2 fetched no cover for this subject
			continue
		}
		if !e.portrait() {
			r.c.coverLandscape++ // landscape/square — DLsite already supplies it (step 55)
			continue
		}
		if r.writeCover(ctx, opts.BangumiMirror, c, e, opts.Apply) {
			return true
		}
	}
	return false
}

func (r *runner) summary(opts Opts, candidates int) map[string]any {
	return map[string]any{
		"apply":         opts.Apply,
		"exact_anchors": candidates,
		"cover": map[string]any{
			"uploaded":        r.c.coverUploaded,
			"would_upload":    r.c.coverWould,
			"already_exists":  r.c.coverExists,
			"landscape_skip":  r.c.coverLandscape,
			"no_dims_skip":    r.c.coverNoDims,
			"missing_file":    r.c.coverMissing,
			"rejected":        r.c.coverRejected,
			"refused_claimed": r.c.coverRefused,
			"dedup":           r.c.coverDedup,
		},
		"errors": r.c.errors,
	}
}

func openGorm(dsn string) (*gorm.DB, error) {
	return gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
}

func resolveBaseURL(cfg *config.Config, clientCfg config.ImageClientConfig, override string) string {
	if override != "" {
		return override
	}
	if clientCfg.BaseURL != "" {
		return clientCfg.BaseURL
	}
	return fmt.Sprintf("http://%s:%d", cfg.ImageService.Host, cfg.ImageService.Port)
}

func chunk[T any](src []T, n int) [][]T {
	var out [][]T
	for i := 0; i < len(src); i += n {
		out = append(out, src[i:min(i+n, len(src))])
	}
	return out
}
