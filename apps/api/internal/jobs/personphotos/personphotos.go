// The mirror's file stem stays "logo" (<mirror-dir>/<external_id>/logo.<ext>)
// because kun-bangumi-api's fetch-person-images writes it that way for both the
// label and person lanes. Renaming it here would silently stop resolving files
// the crawler has already written.
package personphotos

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"api/internal/infrastructure/database"
	"api/pkg/config"
	"api/pkg/imageclient"

	"gorm.io/gorm"
)

const (
	uploadRetries  = 6
	defaultTimeout = 60 * time.Second

	sourceKey   = "bangumi"
	uploaderSub = "system:person-photo-backfill:bangumi"
	fileStem    = "logo"
)

type Opts struct {
	DSN       string
	MirrorDir string
	Apply     bool
	Limit     int
	Offset    int
	UploadGap time.Duration
	ImageBase string
	IDsOut    string
	Workers   int
}

type Stats struct {
	Candidates int
	Missing    int
	Would      int
	Uploaded   int
	Raced      int
	Rejected   int
	Errors     int
	Quota      bool
}

func (s Stats) String() string {
	return fmt.Sprintf("source=%s candidates=%d missing=%d would=%d uploaded=%d raced=%d rejected=%d errors=%d quota=%t",
		sourceKey, s.Candidates, s.Missing, s.Would, s.Uploaded, s.Raced, s.Rejected, s.Errors, s.Quota)
}

func Run(ctx context.Context, cfg *config.Config, opts Opts) (*Stats, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn); refusing to guess — pass the rehearsal copy locally, the live catalog only in the production run")
	}
	if opts.Apply && opts.MirrorDir == "" {
		return nil, fmt.Errorf("--mirror-dir is REQUIRED to apply; bytes only ever come from the local mirror")
	}

	db, err := open(opts.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect catalog: %w", err)
	}
	defer closeDB(db)

	sourceID, err := resolveSourceID(ctx, db)
	if err != nil {
		return nil, err
	}

	cands, err := loadCandidates(ctx, db, sourceID)
	if err != nil {
		return nil, fmt.Errorf("load candidates: %w", err)
	}
	cands = window(cands, opts.Limit, opts.Offset)

	m, err := loadMirror(opts.MirrorDir)
	if err != nil {
		return nil, err
	}

	st := &Stats{Candidates: len(cands)}

	if opts.IDsOut != "" {
		n, err := writeIDs(opts.IDsOut, cands, m)
		if err != nil {
			return nil, fmt.Errorf("write ids file: %w", err)
		}
		slog.Info("person-photos wrote mirror worklist", "path", opts.IDsOut, "ids", n)
	}

	r := &runner{db: db, mirror: m, gap: opts.UploadGap, stats: st}
	if opts.Apply {
		if r.cli, err = newClient(ctx, cfg, opts); err != nil {
			return nil, err
		}
	}
	slog.Info("person-photos candidates", "persons", len(cands), "apply", opts.Apply,
		"mirror_dir", opts.MirrorDir, "offset", opts.Offset, "limit", opts.Limit)

	r.run(ctx, opts, cands)
	if err := r.ping(ctx); err != nil {
		slog.Warn("refping fresh hashes", "err", err)
	}
	slog.Info("person-photos done", "apply", opts.Apply, "result", st.String())
	return st, nil
}

func newClient(ctx context.Context, cfg *config.Config, opts Opts) (*imageclient.Client, error) {
	clientCfg := cfg.CatalogImageClient
	if clientCfg.ClientID == "" || clientCfg.ClientSecret == "" {
		return nil, fmt.Errorf("catalog image client not configured (set KUN_CATALOG_IMAGE_CLIENT_ID/SECRET); refusing to --apply photo upload")
	}
	base := opts.ImageBase
	if base == "" {
		base = clientCfg.BaseURL
	}
	if base == "" {
		base = fmt.Sprintf("http://%s:%d", cfg.ImageService.Host, cfg.ImageService.Port)
	}
	cli := imageclient.New(imageclient.Config{
		BaseURL: base, CDNBase: cfg.ImageService.CDNBase,
		ClientID: clientCfg.ClientID, ClientSecret: clientCfg.ClientSecret,
		Timeout: defaultTimeout,
	})
	hctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := cli.Health(hctx); err != nil {
		return nil, fmt.Errorf("image_service unreachable at %s: %w", base, err)
	}
	return cli, nil
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
