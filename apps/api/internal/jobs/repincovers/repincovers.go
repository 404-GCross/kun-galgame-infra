// Package repincovers re-decides which cover a work pins as its portrait, and
// cleans up the super-resolution products the old decision wasted GPU on.
//
// WHY. The retired cmd/pin-portrait-covers picked "the tallest portrait cover"
// and consulted the cover KIND only to break a tie between equal heights. A
// high-resolution scan of a disc face, a box back or a booklet page is taller
// than a clean digital cover far more often than it should be, so those rows
// won the pin — and then the GPU wave dutifully upscaled them. Measured on
// production 2026-08-08: 1,673 works pin something the kind ladder disagrees
// with, and 47 super-resolution products are enlargements of a box back, a
// booklet page or a spine.
//
// WHAT IT DOES. ladder.go holds the new rule (kind decides the tier, size only
// orders within it). This file resolves every work that HAS a pin today, and
// write.go carries out one of four jobs:
//
//   - report (default): the plan, with counts and a CSV for human review.
//   - --export-dir: download the winners that are under 1080px so they can go
//     through upscale-bench locally. Downloads only; writes nothing.
//   - --reinject-dir --apply: upload the upscaled products, write ONE new
//     cover row each (source=upscale, kind inherited) and move the pin to it.
//   - --purge-bad-upscales --apply: delete the super-resolution rows whose
//     inherited kind says they enlarge a box back / booklet / spine / disc.
//
// DISCIPLINE.
//   - --dsn is REQUIRED; a bare run cannot touch a database.
//   - Every write is additive or a flag flip, EXCEPT the purge, which is why
//     the purge is its own mode and is meant to run last.
//   - The pin is exclusive: moving it clears the work's other pins in the same
//     transaction, so "at most one pinned cover per work" survives a crash.
//   - Bytes go to the CATALOG image scope under the catalog_cover preset. The
//     first wave uploaded through the galgame_wiki client because covers lived
//     in the wiki body then; they do not any more, and that key is off limits.
//   - Idempotent: a re-run re-reads the pins and plans only what still
//     disagrees, so a second --apply moves nothing.
package repincovers

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"time"

	"api/pkg/config"
	"api/pkg/imageclient"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const defaultTimeout = 60 * time.Second

// metaBatchSize is the image service's own cap on /image/meta-batch.
const metaBatchSize = 1000

// Opts configures a run. The mode flags are mutually exclusive; Run rejects a
// combination rather than guessing an order.
type Opts struct {
	DSN     string // catalog — REQUIRED
	Apply   bool
	IDs     []int64 // restrict to these works (the plan predicates still apply)
	Limit   int     // max works to ACT on (0 = all); the report always covers everything
	PlanOut string  // write the full plan to this CSV path

	ExportDir        string // download under-target winners here
	ReinjectDir      string // upload the upscaled products from here
	PurgeBadUpscales bool

	ImageBaseURL string        // image service base override (local dev)
	UploadGap    time.Duration // min delay between uploads
}

// Stats reports one run.
type Stats struct {
	Works      int // works carrying a pin today
	Covers     int
	NoDims     int // rows image_service does not know (never eligible)
	Agreed     int // the ladder agrees with the pin in place
	NoWinner   int // no eligible portrait cover at all; left alone
	DirectPin  int
	NeedUpsc   int
	DeferNSFW  int
	Exported   int
	Uploaded   int
	Repinned   int
	Purged     int
	Skipped    int // planned but not acted on (missing product file, limit)
	Errors     int
	QuotaBreak bool
}

func (s Stats) String() string {
	return fmt.Sprintf("works=%d covers=%d no_dims=%d agreed=%d no_winner=%d direct_pin=%d need_upscale=%d nsfw_deferred=%d exported=%d uploaded=%d repinned=%d purged=%d skipped=%d errors=%d quota=%t",
		s.Works, s.Covers, s.NoDims, s.Agreed, s.NoWinner, s.DirectPin, s.NeedUpsc, s.DeferNSFW,
		s.Exported, s.Uploaded, s.Repinned, s.Purged, s.Skipped, s.Errors, s.QuotaBreak)
}

// Run resolves the plan and carries out the selected mode.
func Run(ctx context.Context, cfg *config.Config, opts Opts) (*Stats, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("--dsn is REQUIRED; refusing to guess a catalog database")
	}
	if err := checkModes(opts); err != nil {
		return nil, err
	}
	db, err := open(opts.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect catalog: %w", err)
	}
	defer closeDB(db)

	r := &runner{db: db, stats: &Stats{}, gap: opts.UploadGap}
	if needsImageClient(opts) {
		if r.cli, err = newImageClient(ctx, cfg, opts); err != nil {
			return nil, err
		}
	}

	if opts.PurgeBadUpscales {
		err = r.purge(ctx, opts)
		slog.Info("repin-covers done", "mode", "purge", "apply", opts.Apply, "result", r.stats.String())
		return r.stats, err
	}

	plans, err := r.plan(ctx, opts)
	if err != nil {
		return r.stats, err
	}
	if opts.PlanOut != "" {
		if err := writePlanCSV(opts.PlanOut, plans, r.url); err != nil {
			return r.stats, fmt.Errorf("write plan csv: %w", err)
		}
		slog.Info("plan written", "path", opts.PlanOut, "rows", len(plans))
	}

	switch {
	case opts.ExportDir != "":
		err = r.export(ctx, opts, plans)
	case opts.ReinjectDir != "":
		err = r.reinject(ctx, opts, plans)
	case opts.Apply:
		err = r.directPin(ctx, opts, plans)
	}
	slog.Info("repin-covers done", "apply", opts.Apply, "result", r.stats.String())
	return r.stats, err
}

// checkModes refuses an ambiguous combination instead of picking an order.
func checkModes(o Opts) error {
	n := 0
	for _, on := range []bool{o.ExportDir != "", o.ReinjectDir != "", o.PurgeBadUpscales} {
		if on {
			n++
		}
	}
	if n > 1 {
		return fmt.Errorf("--export-dir, --reinject-dir and --purge-bad-upscales are mutually exclusive")
	}
	if o.PurgeBadUpscales && !o.Apply {
		slog.Warn("purge is a DRY listing without --apply")
	}
	return nil
}

// needsImageClient reports whether this mode talks to image_service. The plan
// itself does: cover dimensions come from there, and without them the ladder
// has no shape evidence at all.
func needsImageClient(o Opts) bool { return !o.PurgeBadUpscales }

func newImageClient(ctx context.Context, cfg *config.Config, opts Opts) (*imageclient.Client, error) {
	c := cfg.CatalogImageClient
	if c.ClientID == "" || c.ClientSecret == "" {
		return nil, fmt.Errorf("catalog image client credentials are not configured")
	}
	base := opts.ImageBaseURL
	if base == "" {
		base = c.BaseURL
	}
	if base == "" {
		base = fmt.Sprintf("http://%s:%d", cfg.ImageService.Host, cfg.ImageService.Port)
	}
	cli := imageclient.New(imageclient.Config{
		BaseURL: base, CDNBase: cfg.ImageService.CDNBase,
		ClientID: c.ClientID, ClientSecret: c.ClientSecret, Timeout: defaultTimeout,
	})
	hctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := cli.Health(hctx); err != nil {
		return nil, fmt.Errorf("image_service unreachable at %s: %w", base, err)
	}
	return cli, nil
}

type runner struct {
	db      *gorm.DB
	cli     *imageclient.Client
	gap     time.Duration
	stats   *Stats
	touched []int64
	fresh   []string // hashes uploaded this run, for the reference ping
}

// coverRow is the raw projection of catalog_work_cover joined to its source key.
type coverRow struct {
	ID        int64  `gorm:"column:id"`
	WorkID    int64  `gorm:"column:work_id"`
	Hash      string `gorm:"column:image_hash"`
	Kind      string `gorm:"column:kind"`
	SourceKey string `gorm:"column:source_key"`
	Sexual    int16  `gorm:"column:sexual"`
	SortOrder int    `gorm:"column:sort_order"`
	Pinned    bool   `gorm:"column:portrait_pinned"`
}

// plan loads every cover of every work that currently pins one, resolves the
// dimensions from image_service and runs the ladder.
//
// The population is deliberately "works that HAVE a pin". A work with no pin
// falls back on the read face's own rule (first portrait-shaped cover in sort
// order), and that rule was measured clean — zero unpinned works lead with a
// disc face or a box back. Re-deciding those too would be a much larger change
// than the defect justifies.
func (r *runner) plan(ctx context.Context, opts Opts) ([]Plan, error) {
	q := r.db.WithContext(ctx).Raw(`
		SELECT c.id, c.work_id, c.image_hash, c.kind, s.key AS source_key,
		       c.sexual, c.sort_order, c.portrait_pinned
		FROM catalog_work_cover c
		JOIN catalog_source s ON s.id = c.source_id
		WHERE c.work_id IN (SELECT work_id FROM catalog_work_cover WHERE portrait_pinned)
		ORDER BY c.work_id, c.sort_order, c.image_hash`)
	var rows []coverRow
	if err := q.Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("load covers: %w", err)
	}
	want := idSet(opts.IDs)
	byWork := map[int64][]Cover{}
	hashes := make([]string, 0, len(rows))
	for _, row := range rows {
		if want != nil && !want[row.WorkID] {
			continue
		}
		byWork[row.WorkID] = append(byWork[row.WorkID], Cover{
			ID: row.ID, WorkID: row.WorkID, Hash: row.Hash, Kind: row.Kind,
			SourceKey: row.SourceKey, Sexual: row.Sexual, SortOrder: row.SortOrder, Pinned: row.Pinned,
		})
		hashes = append(hashes, row.Hash)
	}
	meta, err := r.loadMeta(ctx, hashes)
	if err != nil {
		return nil, err
	}
	r.stats.Works = len(byWork)
	r.stats.Covers = len(hashes)

	workIDs := make([]int64, 0, len(byWork))
	for id := range byWork {
		workIDs = append(workIDs, id)
	}
	slices.Sort(workIDs)

	plans := make([]Plan, 0, len(workIDs))
	for _, id := range workIDs {
		covers := byWork[id]
		for i := range covers {
			if m, ok := meta[covers[i].Hash]; ok && m.Width > 0 && m.Height > 0 {
				covers[i].Width, covers[i].Height, covers[i].DimsKnown = m.Width, m.Height, true
			} else {
				r.stats.NoDims++
			}
		}
		p := planWork(id, covers)
		switch {
		case p.New == nil:
			r.stats.NoWinner++
		case p.Action == ActionNone:
			r.stats.Agreed++
		case p.Action == ActionDirectPin:
			r.stats.DirectPin++
		case p.Action == ActionUpscale:
			r.stats.NeedUpsc++
		case p.Action == ActionDeferredNSFW:
			r.stats.DeferNSFW++
		}
		plans = append(plans, p)
	}
	return plans, nil
}

// loadMeta resolves dimensions for every hash in batches of the service's cap.
func (r *runner) loadMeta(ctx context.Context, hashes []string) (map[string]imageclient.ImageMeta, error) {
	out := make(map[string]imageclient.ImageMeta, len(hashes))
	for i := 0; i < len(hashes); i += metaBatchSize {
		batch := hashes[i:min(i+metaBatchSize, len(hashes))]
		m, err := r.cli.MetaBatch(ctx, batch)
		if err != nil {
			return nil, fmt.Errorf("meta-batch at %d: %w", i, err)
		}
		maps.Copy(out, m)
	}
	return out, nil
}

// actionable filters the plans this mode should act on, honouring --limit.
func actionable(plans []Plan, want Action, limit int) []Plan {
	out := make([]Plan, 0, len(plans))
	for _, p := range plans {
		if p.Action != want {
			continue
		}
		out = append(out, p)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func idSet(ids []int64) map[int64]bool {
	if len(ids) == 0 {
		return nil
	}
	m := make(map[int64]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

func open(dsn string) (*gorm.DB, error) {
	return gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
}

func closeDB(db *gorm.DB) {
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}
