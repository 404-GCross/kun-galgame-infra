package labellogos

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
)

type Opts struct {
	Source    Source
	DSN       string
	MirrorDir string
	Apply     bool
	Limit     int
	Offset    int
	UploadGap time.Duration
	ImageBase string
	IDsOut    string
	AuditOut  string
	Workers   int
}

type Stats struct {
	Source     string
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
		s.Source, s.Candidates, s.Missing, s.Would, s.Uploaded, s.Raced, s.Rejected, s.Errors, s.Quota)
}

func Run(ctx context.Context, cfg *config.Config, opts Opts) (*Stats, error) {
	if opts.Source.Key == "" {
		return nil, fmt.Errorf("--source is REQUIRED (bangumi|cien); a bare run must not guess which upstream it is filling from")
	}
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

	reg, err := resolveRegistry(ctx, db)
	if err != nil {
		return nil, err
	}

	if opts.AuditOut != "" {
		pairs, err := collectAudit(ctx, db, reg)
		if err != nil {
			return nil, fmt.Errorf("collect audit pairs: %w", err)
		}
		if err := writeAudit(opts.AuditOut, pairs); err != nil {
			return nil, fmt.Errorf("write audit file: %w", err)
		}
		slog.Info("label-logos wrote falsification set", "path", opts.AuditOut, "pairs", len(pairs))
	}

	cands, err := loadCandidates(ctx, db, reg.sourceID(opts.Source), opts.Source)
	if err != nil {
		return nil, fmt.Errorf("load candidates: %w", err)
	}
	cands = window(cands, opts.Limit, opts.Offset)

	m, err := loadMirror(opts.MirrorDir, opts.Source)
	if err != nil {
		return nil, err
	}

	st := &Stats{Source: opts.Source.Key, Candidates: len(cands)}

	if opts.IDsOut != "" {
		n, err := writeIDs(opts.IDsOut, cands, m)
		if err != nil {
			return nil, fmt.Errorf("write ids file: %w", err)
		}
		slog.Info("label-logos wrote mirror worklist", "path", opts.IDsOut, "ids", n)
	}

	r := &runner{db: db, source: opts.Source, mirror: m, gap: opts.UploadGap, stats: st}
	if opts.Apply {
		if r.cli, err = newClient(ctx, cfg, opts); err != nil {
			return nil, err
		}
	}
	slog.Info("label-logos candidates", "source", opts.Source.Key, "labels", len(cands), "apply", opts.Apply,
		"mirror_dir", opts.MirrorDir, "offset", opts.Offset, "limit", opts.Limit)

	r.run(ctx, opts, cands)
	if err := r.ping(ctx); err != nil {
		slog.Warn("refping fresh hashes", "err", err)
	}
	slog.Info("label-logos done", "apply", opts.Apply, "result", st.String())
	return st, nil
}

func newClient(ctx context.Context, cfg *config.Config, opts Opts) (*imageclient.Client, error) {
	clientCfg := cfg.CatalogImageClient
	if clientCfg.ClientID == "" || clientCfg.ClientSecret == "" {
		return nil, fmt.Errorf("catalog image client not configured (set KUN_CATALOG_IMAGE_CLIENT_ID/SECRET); refusing to --apply logo upload")
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
