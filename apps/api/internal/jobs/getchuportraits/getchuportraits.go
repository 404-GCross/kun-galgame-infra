// Package getchuportraits fills a catalog character's IMAGES from the Getchu
// crawler's mirrored character art (refs/proj/167 §10 and §11).
//
// TWO SLOTS, ONE LANE. A character has two independent images, and this lane
// fills either — chosen by --slot, never defaulted:
//
//	slot    getchu kind  file           measured   catalog column  preset
//	bust    nameplate    charaN.jpg     250x300    image_hash      character
//	figure  portrait     charabN.jpg    500x500    figure_hash     character_figure
//
// THE KIND NAMES ARE THE WRONG WAY ROUND. The kind called `nameplate` is the
// upper-body crop; the kind called `portrait` is the full-body standing art.
// That is measured from the actual files, not inferred from the names, and it
// is the single easiest thing to get backwards here.
//
// The two slots differ ONLY in those three columns (see slot.go). Everything
// that is actually hard — the pairing, the roster match, the edition walk,
// fill-missing, the upload pool — is shared, deliberately: two copies of the
// matching path could decide differently who a character is, and then one would
// write one girl's bust and the other another girl's figure onto the same
// profile, with nothing downstream able to notice.
//
// WHAT THE BUST WAVE WAS WORTH (measured on production, refs/proj/167 §10):
//
//	26,511  characters the roster matcher resolves
//	22,428  ...already had a portrait  → skipped, fill-missing
//	   983  ...no edition offers a bust → unfillable
//	 3,100  ...FILLABLE, of which 3,099 landed
//
// The gap between "10,662 anchored characters with no portrait" and 4,083 is
// name matching: most portrait-less characters on an anchored work have no
// Getchu roster row that resolves to them. That is a coverage limit, not a
// defect — the alternative is guessing, which is what makes a wrong face
// possible.
//
// THE PAIRING IS FIRST-PARTY, NOT INFERRED. Each Getchu roster entry sits in
// one <tr> with its own images, and the parser records those on
// item_characters.nameplate_url / portrait_url. So image→roster-row is the
// page's own claim, carrying no guess of ours. The only inferential step is
// roster-row→catalog character, and that is getchuchars.Resolve — the SAME
// matcher that writes these characters' prose. Its ambiguity policy comes
// along: a name matching two roster characters is dropped, never guessed.
//
// Positional matching (ordinal N ←→ the Nth image) is the trap this avoids: it
// holds for only 8,013 of 13,127 items, so it would misfile ~39% of faces.
//
// VERIFIED, not asserted. Characters that already have a portrait from an
// independent source are never written by this lane, which makes them a free
// falsification set — see --audit-out. On the bust wave, restricted to the
// pages where the comparison is decisive, the lane picked the right image
// 129/129 against a chance baseline of 21.8.
//
// DISCIPLINE, inherited from getchumedia and dlsitemedia:
//   - Bytes come ONLY from the local mirror (--mirror-dir). Never dials getchu.
//   - Both DSNs are explicit; a bare run cannot touch a live DB.
//   - Fill-missing per slot: a character whose slot is already set is skipped
//     before any byte is read. Getchu is a FALLBACK, never a supplement — a
//     VNDB portrait is not replaced.
//   - Fresh hashes are reference-pinged immediately; an image sits at TTL from
//     upload time, so waiting for the nightly refping risks losing the bytes.
//     Both columns are in the nightly sweep's union (catalog_image_refping).
package getchuportraits

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"api/internal/jobs/getchuchars"
	"api/pkg/config"
	"api/pkg/imageclient"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const (
	// uploadRetries rides out an image-container recreation mid-run (~30-90s
	// unreachable). Quota and moderation are terminal and never retried.
	uploadRetries  = 6
	defaultTimeout = 60 * time.Second
)

// Opts configures a run.
type Opts struct {
	DSN       string // catalog — REQUIRED
	GetchuDSN string // crawler staging — REQUIRED
	// Slot is which image to fill: SlotBust or SlotFigure. No default — the two
	// write different columns from different bytes.
	Slot      Slot
	MirrorDir string // local mirror root — REQUIRED in apply mode
	Apply     bool
	Limit     int // max CHARACTERS to process (0 = all)
	Offset    int
	UploadGap time.Duration
	ImageBase string // image service base override (local dev)
	// IDsOut, when set, writes the distinct Getchu ids the candidates need to
	// this path — the exact --ids-file for the crawler's mirror phase.
	IDsOut string
	// AuditOut, when set, writes the falsification set (characters that already
	// have a bust AND resolve to a Getchu image) as CSV. This lane never writes
	// to those rows, which is what makes them a fair test of the pairing.
	AuditOut string
	// Workers is how many characters upload concurrently (0/1 = serial). The
	// image service answers in ~2s at 5% CPU — the wait is the object store, so
	// a few in flight is the whole speedup. Keep it modest: 6 workers each hold
	// a postgres connection, and a batch job on the other side of the same
	// database is how the screenshot wave hit "too many clients".
	Workers int
}

// Stats reports one run. Every resolved character lands in exactly one of
// skipHasImage / noImage / missing / uploaded / rejected / errors, so the
// buckets add up to Resolved.
type Stats struct {
	Matched      int // characters the roster matcher resolved
	Resolved     int // ...whose slot is empty and that have a staged image
	SkipHasImage int // slot already filled — fill-missing, so untouched
	NoImage      int // matched, but no edition of it offers this slot's image
	Missing      int // staged row exists, bytes are not in the mirror dir
	Uploaded     int
	Rejected     int // moderation said no
	Errors       int
	Quota        bool // the daily image quota stopped the run
}

func (s Stats) String() string {
	return fmt.Sprintf("matched=%d resolved=%d skip_has_image=%d no_image=%d missing=%d uploaded=%d rejected=%d errors=%d quota=%t",
		s.Matched, s.Resolved, s.SkipHasImage, s.NoImage, s.Missing, s.Uploaded, s.Rejected, s.Errors, s.Quota)
}

// Run resolves the candidates and forecasts (dry) or uploads (apply).
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

	// The falsification set is produced BEFORE any write and independently of
	// the candidate set, so auditing never depends on the thing it audits.
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

	// The mirror worklist. The crawler's mirror phase narrows by ITEM, so this
	// is the distinct Getchu ids behind the candidates — a few thousand items
	// instead of the whole 75,133-image kind. Written before any upload so a
	// dry run is a complete planning step on its own.
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

// newClient builds the catalog image client and proves the service is reachable
// before the first byte, rather than after ten thousand failed attempts.
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

// window slices the candidate list for chunked runs.
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
