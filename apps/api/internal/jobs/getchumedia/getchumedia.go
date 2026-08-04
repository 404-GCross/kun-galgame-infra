// Package getchumedia fills catalog work SCREENSHOT galleries from the Getchu
// crawler's mirrored sample images (refs/proj/167 §9).
//
// WHY. 3,482 works carrying an exact Getchu release anchor show no screenshot
// at all — no wiki gallery, no VNDB shots, no DLsite samples. 2,102 of them
// have Getchu sample CG staged, 16,340 images in total. Getchu is a FALLBACK
// here and never a supplement: a work that already has a gallery keeps it (see
// loadCandidates).
//
// WHAT IT IS NOT. The other three staged image kinds are out of scope, each for
// its own reason:
//   - package (17,753) is the store cover, and the anchored population is
//     ALREADY at 100% cover coverage. The entire kind would fill five gaps.
//   - nameplate (75,133, a face/bust crop) and portrait (39,617, full-body
//     standing art) are character art. They belong on
//     catalog_character.image_hash, and attaching one to the right character
//     needs a pairing this lane does not have — see the note on ordinals below.
//
// THE ORDINAL TRAP, recorded so nobody tries the shortcut. It is tempting to
// match character art positionally: item_characters.ordinal N ←→ the Nth
// nameplate/portrait. Measured, that holds for only 8,013 of 13,127 items on
// nameplate and 3,667 on portrait — a wrong link on ~39% of items, which is
// exactly the kind of guess getchuchars refuses to make. The source itself
// carries the pairing (each character sits in one <tr> with its own image),
// so the honest fix is a crawler reparse that records the image URL on the
// character row. That is a separate wave and needs no re-crawl.
//
// DISCIPLINE, from internal/jobs/dlsitemedia:
//   - Bytes come ONLY from the local mirror (--mirror-dir), produced by
//     kun-getchu-api's `mirror` phase. This job never dials getchu.com.
//   - Both DSNs are explicit; a bare run cannot touch a live DB.
//   - Idempotent: a work with any screenshot is not a candidate at all, and the
//     (work_id, image_hash) unique index is the backstop. A second --apply
//     writes zero.
//   - Fresh hashes are reference-pinged immediately — an image sits at TTL from
//     upload time, so waiting for the nightly refping risks losing the bytes.
//   - Only works that actually gained a row are touched, so a second --apply
//     moves no watermark on the public changes feed.
package getchumedia

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"api/internal/platform/catalog/repository"
	"api/pkg/config"
	"api/pkg/imageclient"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Opts configures a run.
type Opts struct {
	DSN        string // catalog — REQUIRED
	GetchuDSN  string // crawler staging — REQUIRED
	MirrorDir  string // local mirror root — REQUIRED in apply mode
	Apply      bool
	Limit      int // max WORKS to process (0 = all)
	Offset     int
	UploadGap  time.Duration // min delay between uploads
	ImageBase  string        // image service base override (local dev)
	MaxPerWork int           // cap the gallery size per work (0 = no cap)
	// Workers is how many WORKS upload concurrently (0/1 = serial). The image
	// service answers an upload in ~2s at 5% CPU — the wait is the object store,
	// not compute — so a few in flight is the whole speedup. Never parallelises
	// within one work: see runner.run.
	Workers int
}

// Stats reports one run.
type Stats struct {
	Works    int // anchored works with no screenshot today
	NoStaged int // ...of which none has a mirrored sample image
	Planned  int // images decided
	Uploaded int
	Missing  int // staged row exists but the bytes are not in the mirror dir
	Dedup    int // the same bytes were already on this work
	Rejected int // moderation said no
	Errors   int
	Quota    bool // the daily image quota stopped the run
}

func (s Stats) String() string {
	return fmt.Sprintf("works=%d no_staged=%d planned=%d uploaded=%d missing=%d dedup=%d rejected=%d errors=%d quota=%t",
		s.Works, s.NoStaged, s.Planned, s.Uploaded, s.Missing, s.Dedup, s.Rejected, s.Errors, s.Quota)
}

// Run resolves the candidates and forecasts (dry) or uploads (apply).
func Run(ctx context.Context, cfg *config.Config, opts Opts) (*Stats, error) {
	if opts.DSN == "" || opts.GetchuDSN == "" {
		return nil, fmt.Errorf("--dsn and --getchu-dsn are both REQUIRED; refusing to guess either")
	}
	if opts.Apply && opts.MirrorDir == "" {
		return nil, fmt.Errorf("--mirror-dir is REQUIRED to apply; bytes only ever come from the local mirror")
	}
	db, err := open(opts.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect catalog: %w", err)
	}
	defer closeDB(db)
	gdb, err := open(opts.GetchuDSN)
	if err != nil {
		return nil, fmt.Errorf("connect getchu staging: %w", err)
	}
	defer closeDB(gdb)

	reg, err := resolveRegistry(ctx, db)
	if err != nil {
		return nil, err
	}
	cands, err := loadCandidates(ctx, db, reg.getchuSource, reg.galgameMedium)
	if err != nil {
		return nil, err
	}
	cands = window(cands, opts.Limit, opts.Offset)
	staged, err := loadSamples(ctx, gdb)
	if err != nil {
		return nil, err
	}
	workIDs := make([]int64, 0, len(cands))
	for _, c := range cands {
		workIDs = append(workIDs, c.WorkID)
	}
	have, err := preloadHashes(ctx, db, workIDs)
	if err != nil {
		return nil, fmt.Errorf("preload screenshot hashes: %w", err)
	}

	r := &runner{
		db: db, source: reg.getchuSource, have: have, gap: opts.UploadGap,
		maxPerWork: opts.MaxPerWork, stats: &Stats{Works: len(cands)},
	}
	if opts.Apply {
		clientCfg := cfg.CatalogImageClient
		if clientCfg.ClientID == "" || clientCfg.ClientSecret == "" {
			return nil, fmt.Errorf("catalog image client credentials are not configured")
		}
		base := opts.ImageBase
		if base == "" {
			base = clientCfg.BaseURL
		}
		if base == "" {
			base = fmt.Sprintf("http://%s:%d", cfg.ImageService.Host, cfg.ImageService.Port)
		}
		r.cli = imageclient.New(imageclient.Config{
			BaseURL: base, CDNBase: cfg.ImageService.CDNBase,
			ClientID: clientCfg.ClientID, ClientSecret: clientCfg.ClientSecret,
			Timeout: defaultTimeout,
		})
		// Fail before the first byte rather than after 16,000 upload attempts.
		hctx, hcancel := context.WithTimeout(ctx, 5*time.Second)
		defer hcancel()
		if err := r.cli.Health(hctx); err != nil {
			return nil, fmt.Errorf("image_service unreachable at %s: %w", base, err)
		}
	}
	slog.Info("getchu-media candidates", "works", len(cands), "staged_items", len(staged),
		"apply", opts.Apply, "mirror_dir", opts.MirrorDir, "offset", opts.Offset, "limit", opts.Limit)

	r.run(ctx, opts, cands, staged)
	if err := repository.TouchWorks(ctx, db, r.touched); err != nil {
		return nil, fmt.Errorf("touch works: %w", err)
	}
	if err := r.ping(ctx); err != nil {
		slog.Warn("refping fresh hashes", "err", err)
	}
	slog.Info("getchu-media done", "apply", opts.Apply, "result", r.stats.String())
	return r.stats, nil
}

type registry struct {
	getchuSource  int16
	galgameMedium int16
}

func resolveRegistry(ctx context.Context, db *gorm.DB) (registry, error) {
	var r registry
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_source WHERE key = 'getchu'`).Scan(&r.getchuSource).Error; err != nil {
		return r, err
	}
	if err := db.WithContext(ctx).Raw(`SELECT id FROM catalog_medium WHERE key = 'galgame'`).Scan(&r.galgameMedium).Error; err != nil {
		return r, err
	}
	if r.getchuSource == 0 || r.galgameMedium == 0 {
		return r, fmt.Errorf("registry not seeded (getchu source=%d, galgame medium=%d)", r.getchuSource, r.galgameMedium)
	}
	return r, nil
}

// window slices whole works for chunked runs.
func window(c []candidate, limit, offset int) []candidate {
	if offset > 0 {
		if offset >= len(c) {
			return nil
		}
		c = c[offset:]
	}
	if limit > 0 && limit < len(c) {
		c = c[:limit]
	}
	return c
}

func open(dsn string) (*gorm.DB, error) {
	return gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
}

func closeDB(db *gorm.DB) {
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}
