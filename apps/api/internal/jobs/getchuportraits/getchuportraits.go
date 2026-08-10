// THE GETCHU KIND NAMES ARE THE WRONG WAY ROUND: the kind called `nameplate`
// is the upper-body crop and the kind called `portrait` is the full-body
// standing art. That is measured from the files, not inferred from the names,
// and it is the single easiest thing here to get backwards.
//
// Positional matching (ordinal N <-> the Nth image) holds for only 8,013 of
// 13,127 items, so it would misfile ~39% of faces; the parser records the
// page's own pairing instead.
package getchuportraits

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"api/internal/infrastructure/database"
	"api/internal/jobs/getchuchars"
	"api/pkg/config"
	"api/pkg/imageclient"

	"gorm.io/gorm"
)

const (
	uploadRetries  = 6
	defaultTimeout = 60 * time.Second
)

type Opts struct {
	DSN       string
	GetchuDSN string
	Slot      Slot
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
	Matched      int
	Resolved     int
	SkipHasImage int
	NoImage      int
	Missing      int
	Uploaded     int
	Rejected     int
	Errors       int
	Quota        bool
}

func (s Stats) String() string {
	return fmt.Sprintf("matched=%d resolved=%d skip_has_image=%d no_image=%d missing=%d uploaded=%d rejected=%d errors=%d quota=%t",
		s.Matched, s.Resolved, s.SkipHasImage, s.NoImage, s.Missing, s.Uploaded, s.Rejected, s.Errors, s.Quota)
}

func Run(ctx context.Context, cfg *config.Config, opts Opts) (*Stats, error) {
	if opts.DSN == "" || opts.GetchuDSN == "" {
		return nil, fmt.Errorf("--dsn and --getchu-dsn are both REQUIRED; refusing to guess either")
	}
	if opts.Slot.TargetColumn == "" {
		return nil, fmt.Errorf("Slot is REQUIRED (see ParseSlot); a bare run must not guess which image it is filling")
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

	source, err := getchuchars.SourceID(ctx, db)
	if err != nil {
		return nil, err
	}
	matched, ms, err := getchuchars.Resolve(ctx, db, gdb, source)
	if err != nil {
		return nil, err
	}
	slog.Info("getchu-portraits matched", "slot", opts.Slot.Name, "result", ms, "characters", len(matched))

	if opts.AuditOut != "" {
		pairs, err := collectAudit(ctx, db, gdb, opts.Slot, matched)
		if err != nil {
			return nil, fmt.Errorf("collect audit pairs: %w", err)
		}
		if err := writeAudit(opts.AuditOut, pairs); err != nil {
			return nil, fmt.Errorf("write audit file: %w", err)
		}
		slog.Info("getchu-portraits wrote falsification set", "path", opts.AuditOut, "pairs", len(pairs))
	}

	st := &Stats{Matched: len(matched)}
	cands, err := selectCandidates(ctx, db, gdb, opts.Slot, matched, st)
	if err != nil {
		return nil, err
	}
	cands = window(cands, opts.Limit, opts.Offset)
	st.Resolved = len(cands)

	if opts.IDsOut != "" {
		if err := writeIDs(opts.IDsOut, cands); err != nil {
			return nil, fmt.Errorf("write ids file: %w", err)
		}
		slog.Info("getchu-portraits wrote mirror worklist", "path", opts.IDsOut)
	}

	r := &runner{db: db, slot: opts.Slot, gap: opts.UploadGap, stats: st}
	if opts.Apply {
		if r.cli, err = newClient(ctx, cfg, opts); err != nil {
			return nil, err
		}
	}
	slog.Info("getchu-portraits candidates", "slot", opts.Slot.Name, "characters", len(cands), "apply", opts.Apply,
		"mirror_dir", opts.MirrorDir, "offset", opts.Offset, "limit", opts.Limit)

	r.run(ctx, opts, cands)
	if err := r.ping(ctx); err != nil {
		slog.Warn("refping fresh hashes", "err", err)
	}
	slog.Info("getchu-portraits done", "slot", opts.Slot.Name, "apply", opts.Apply, "result", st.String())
	return st, nil
}

func newClient(ctx context.Context, cfg *config.Config, opts Opts) (*imageclient.Client, error) {
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
