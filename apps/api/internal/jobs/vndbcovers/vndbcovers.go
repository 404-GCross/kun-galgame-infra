// Package vndbcovers fills catalog-native cover rows for galgame works that
// carry an EXACT VNDB anchor and show NO cover at all.
//
// WHY. Wave 164 flipped the cover read face onto catalog_work_cover, so a work
// with zero rows in that table renders with no image anywhere — and the
// VNDB-anchored population is the one place where the missing bytes are a
// single, unambiguous API call away: the anchor already names the exact vn, and
// /kana/vn hands back that vn's own cover with its dimensions and content
// rating. This lane is a FALLBACK and never a supplement: a work that already
// has any cover keeps it (see loadCandidates), so re-running writes nothing.
//
// DISCIPLINE, from internal/jobs/dlsitemedia and internal/jobs/bangumicovers:
//   - --dsn is ALWAYS explicit; a bare run cannot touch a live DB.
//   - Dry-run is the default and prints a per-work forecast; --apply uploads and
//     writes, --limit caps how many works --apply touches.
//   - Idempotent: "no cover today" is the admission rule itself, so a work that
//     gained a cover between runs is not a candidate and is skipped before any
//     network call; the (work_id, image_hash) unique index plus ON CONFLICT DO
//     NOTHING is the write-time backstop.
//   - Fresh hashes are reference-pinged immediately — an image sits at TTL from
//     upload time, so waiting for the nightly refping risks losing the bytes.
//   - Only works that actually gained a row are touched, so a second --apply
//     moves no watermark on the public changes feed.
//
// UNLIKE its siblings this job DOES dial the network: bytes come from
// t.vndb.org and metadata from the official https://api.vndb.org/kana API (no
// auth needed for /vn). It is polite about it — small batches, a ~1 req/s
// throttle, and 429 honoured with the server's own Retry-After.
package vndbcovers

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
	DSN          string  // catalog — REQUIRED
	Apply        bool    // false = dry-run forecast (no downloads, no uploads, no writes)
	Limit        int     // max works to APPLY (0 = all); the dry-run forecast always covers the whole population
	Offset       int     // skip this many candidate works (chunking)
	IDs          []int64 // restrict to these catalog_work ids (the anchor / no-cover predicates still apply)
	ImageBaseURL string  // image_service base override (point at the LOCAL dev service)
	UploadGap    time.Duration
	APIBase      string // VNDB API base override (tests / a mirror); defaults to the official one
}

// Stats reports one run.
type Stats struct {
	Candidates int  // anchored galgame works with no cover today
	NoImage    int  // ...of which VNDB has no cover image (or the vn is gone)
	Portrait   int  // planned rows whose cover is vertical (portrait_pinned=true)
	Landscape  int  // planned rows whose cover is landscape/square
	Planned    int  // rows that would be written (dry) / were attempted (apply)
	Uploaded   int  // rows actually written
	Dedup      int  // ON CONFLICT no-op
	Rejected   int  // moderation said no
	Errors     int  // download / upload / write failures
	Quota      bool // the daily image quota stopped the run
}

func (s Stats) String() string {
	return fmt.Sprintf("candidates=%d no_image=%d portrait=%d landscape=%d planned=%d uploaded=%d dedup=%d rejected=%d errors=%d quota=%t",
		s.Candidates, s.NoImage, s.Portrait, s.Landscape, s.Planned, s.Uploaded, s.Dedup, s.Rejected, s.Errors, s.Quota)
}

// imageUploader is the slice of the image client this backfill needs. Narrowing
// to an interface (satisfied by *imageclient.Client) keeps the write path
// exercisable without an image service.
type imageUploader interface {
	UploadWithSub(ctx context.Context, r io.Reader, filename, preset, uploaderSub string) (*imageclient.UploadResult, error)
	ReferencePing(ctx context.Context, hashes []string) (*imageclient.ReferencePingResult, error)
	Health(ctx context.Context) error
}

type runner struct {
	db         *gorm.DB
	cli        imageUploader
	sourceID   int16
	gap        time.Duration
	stats      *Stats
	touched    []int64
	pingHashes []string
}

// Run resolves the candidates, asks VNDB what cover each anchored vn has, and
// forecasts (dry) or uploads + writes (apply).
func Run(ctx context.Context, cfg *config.Config, opts Opts) (*Stats, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn); refusing to guess — pass the rehearsal copy locally, the live catalog only in the production run")
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
	cands, err := loadCandidates(ctx, db, reg, opts.IDs)
	if err != nil {
		return nil, fmt.Errorf("load candidates: %w", err)
	}
	cands = window(cands, opts.Offset)
	stats := &Stats{Candidates: len(cands)}
	slog.Info("vndb-covers candidates", "works", len(cands), "apply", opts.Apply,
		"offset", opts.Offset, "limit", opts.Limit, "explicit_ids", len(opts.IDs))

	api := newVNDBAPI(opts.APIBase)
	images, err := api.fetchImages(ctx, anchorIDs(cands))
	if err != nil {
		return stats, fmt.Errorf("query vndb api: %w", err)
	}
	plan := buildPlan(cands, images, stats)
	printForecast(plan, opts)
	if !opts.Apply {
		printTotals(stats)
		slog.Info("vndb-covers done (dry run)", "result", stats.String())
		return stats, nil
	}

	r := &runner{db: db, sourceID: reg.vndbSource, gap: opts.UploadGap, stats: stats}
	r.cli = imageclient.New(imageclient.Config{
		BaseURL:      resolveBaseURL(cfg, clientCfg, opts.ImageBaseURL),
		CDNBase:      cfg.ImageService.CDNBase,
		ClientID:     clientCfg.ClientID,
		ClientSecret: clientCfg.ClientSecret,
		Timeout:      defaultTimeout,
	})
	// Fail before the first byte rather than after N download attempts.
	hctx, hcancel := context.WithTimeout(ctx, 5*time.Second)
	defer hcancel()
	if err := r.cli.Health(hctx); err != nil {
		return stats, fmt.Errorf("image_service unreachable at %s: %w", resolveBaseURL(cfg, clientCfg, opts.ImageBaseURL), err)
	}

	for _, row := range actionable(plan, opts.Limit) {
		if ctx.Err() != nil || stats.Quota {
			break
		}
		r.fill(ctx, row)
	}
	if err := repository.TouchWorks(ctx, db, r.touched); err != nil {
		return stats, fmt.Errorf("touch works: %w", err)
	}
	if err := r.ping(ctx); err != nil {
		slog.Warn("refping fresh hashes", "err", err)
	}
	printTotals(stats)
	slog.Info("vndb-covers done", "result", stats.String())
	if stats.Quota {
		return stats, fmt.Errorf("image quota exceeded — aborted (rerun to resume; idempotent)")
	}
	return stats, nil
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

// window slices whole works for chunked runs.
func window(c []candidate, offset int) []candidate {
	if offset > 0 {
		if offset >= len(c) {
			return nil
		}
		c = c[offset:]
	}
	return c
}

// ping keeps freshly-uploaded bytes alive immediately. An image sits at TTL
// from upload time, so waiting for the nightly refping is a real risk of
// uploading bytes and then losing them.
func (r *runner) ping(ctx context.Context) error {
	if r.cli == nil || len(r.pingHashes) == 0 {
		return nil
	}
	for i := 0; i < len(r.pingHashes); i += 1000 {
		batch := r.pingHashes[i:min(i+1000, len(r.pingHashes))]
		if _, err := r.cli.ReferencePing(ctx, batch); err != nil {
			return err
		}
	}
	return nil
}
