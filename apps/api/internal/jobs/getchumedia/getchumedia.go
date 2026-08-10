// Matching character art positionally (item_characters.ordinal N <-> the Nth
// nameplate/portrait) holds for only 8,013 of 13,127 items, so it would misfile
// ~39%. No guess is needed: the parser records the page's own pairing on
// item_characters.nameplate_url / portrait_url, which is what getchuportraits
// reads.
package getchumedia

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"api/internal/infrastructure/database"
	"api/internal/platform/catalog/repository"
	"api/pkg/config"
	"api/pkg/imageclient"

	"gorm.io/gorm"
)

type Opts struct {
	DSN        string
	GetchuDSN  string
	MirrorDir  string
	Apply      bool
	Limit      int
	Offset     int
	UploadGap  time.Duration
	ImageBase  string
	MaxPerWork int
	Workers    int
}

type Stats struct {
	Works    int
	NoStaged int
	Planned  int
	Uploaded int
	Missing  int
	Dedup    int
	Rejected int
	Errors   int
	Quota    bool
}

func (s Stats) String() string {
	return fmt.Sprintf("works=%d no_staged=%d planned=%d uploaded=%d missing=%d dedup=%d rejected=%d errors=%d quota=%t",
		s.Works, s.NoStaged, s.Planned, s.Uploaded, s.Missing, s.Dedup, s.Rejected, s.Errors, s.Quota)
}

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
	return database.OpenJob(dsn)
}

func closeDB(db *gorm.DB) {
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}
