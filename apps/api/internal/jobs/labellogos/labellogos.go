// Package labellogos fills catalog_label.logo_hash — the brand logo / circle
// avatar of a signing subject (会社 / ブランド / 同人サークル) — from a LOCAL
// mirror produced by one of the two crawler repos (wave 170, refs/proj/170).
//
// WHY THIS LANE EXISTS. Label was the one catalog entity with no image slot at
// all: VNDB, DLsite, Getchu and EGS simply do not publish a producer image.
// Two sources do, and neither overlaps the other much:
//
//	--source bangumi   api.bgm.tv persons of type=2 (会社)  ~3,097 anchored labels
//	--source cien      ci-en.net creator profile avatars    ~2,532 anchored labels
//
// PRECEDENCE IS THE CANDIDATE FILTER, NOT A RANKING FUNCTION. Bangumi wins over
// Ci-en (a curated brand logo beats a creator's self-chosen avatar), and that
// ordering is expressed entirely by an empty logo_hash plus the order the two
// production runs happen in: bangumi first, then cien, which by construction
// only sees labels still empty afterwards. There is no survivorship code here
// and there deliberately is none — a second mechanism that could disagree with
// the run order is a way for the two to fight. The UPDATE re-asserts
// an empty logo_hash, so even a concurrent cien run cannot overwrite a bangumi
// logo; it just claims no rows.
//
// WHAT COUNTS AS AN ANCHOR IS NOT THE SAME ON BOTH LANES. Bangumi labels are
// reached through ordinary EXACT identity anchors. Ci-en labels have none: all
// 2,537 of its label refs are link_kind=related, because both writers file
// Ci-en as web presence rather than identity, so an exact-only filter forecasts
// zero candidates and looks perfectly healthy doing it (measured, acceptance
// run 1). The cien lane therefore accepts related refs carrying one of two
// pinned rules — 'rule:eg-cien' and 'rule:cien-self', both first-party
// self-declarations riding on an already-exact anchor — and nothing else. The
// full argument is on the Source vars in source.go; the predicate itself is
// built in exactly one place (anchorClause) so the candidate query and the
// audit query cannot drift apart.
//
// THE MIRROR CONTRACT (fixed; the crawler repos produce it):
//
//	<mirror-dir>/<external_id>/logo.<ext>     --source bangumi
//	<mirror-dir>/<external_id>/avatar.<ext>   --source cien
//	<mirror-dir>/dims.jsonl                   one {"id","file","w","h","url"} per line
//
// ext is jpg|png|webp|gif — whichever the fetcher happened to get; this lane
// takes the one that exists rather than demanding a format the source never
// offered. dims.jsonl is OPTIONAL: it names the exact mirrored file (preferred
// when present) but pixel sizes are not a filter for logos — unlike the Bangumi
// cover lane, which uses them to tell a portrait from a landscape, a logo is
// used at whatever shape it has. The image service measures dims and thumbhash
// on upload anyway, so a missing manifest costs nothing.
//
// DISCIPLINE, inherited from bangumicovers / getchuportraits / dlsitemedia:
//   - Bytes come ONLY from the local mirror. This binary NEVER dials Bangumi or
//     Ci-en; fetching is the crawler repos' job.
//   - --dsn is ALWAYS explicit — a bare run cannot touch a live database.
//   - Idempotent: a label whose logo_hash is already set is skipped before any
//     byte is read, and the UPDATE re-asserts the empty-string precondition.
//   - Dry-run is the default; --apply is the only thing that uploads or writes.
//   - Fresh hashes are reference-pinged immediately — an image sits at TTL from
//     upload time, so waiting for the nightly sweep risks uploading bytes and
//     then losing them. catalog_label.logo_hash is in that nightly union too
//     (internal/platform/catalog/imagerefs).
//   - Sexual/Violence do not apply: a label logo is a brand mark, rated 0 by
//     charter (§定案 3). The preset carries no rating field, so this is simply
//     not asserted anywhere.
package labellogos

import (
	"context"
	"fmt"
	"log/slog"
	"time"

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
	// Source is which upstream supplies the bytes: SourceBangumi or SourceCien.
	// No default — the two read different mirror filenames and stamp different
	// provenance, and production runs them in a fixed order.
	Source    Source
	DSN       string // catalog — REQUIRED
	MirrorDir string // local mirror root — REQUIRED in apply mode
	Apply     bool
	Limit     int // max LABELS to process (0 = all)
	Offset    int
	UploadGap time.Duration
	ImageBase string // image service base override (local dev)
	// IDsOut, when set, writes the distinct external ids the candidates still
	// need bytes for — the crawler's ids file for its mirror phase.
	IDsOut string
	// AuditOut, when set, writes the falsification set (labels carrying BOTH a
	// bangumi and a cien exact anchor) as CSV. Those are the only labels where
	// the bangumi > cien precedence is an actual choice rather than a
	// tautology, so they are what a human reviews to confirm the ruling.
	AuditOut string
	// Workers is how many labels upload concurrently (0/1 = serial). The image
	// service answers in ~2s at 5% CPU — the wait is the object store, so a few
	// in flight is the whole speedup. Keep it modest: each worker holds a
	// postgres connection.
	Workers int
}

// Stats reports one run. Every candidate lands in exactly one of missing /
// would / uploaded / raced / rejected / errors, so those add up to Candidates.
type Stats struct {
	Source     string
	Candidates int // labels with an exact anchor and no logo yet
	Missing    int // mirror holds no bytes for this label's external id
	Would      int // dry run: bytes present, would upload
	Uploaded   int // apply: uploaded and logo_hash written
	Raced      int // uploaded, but another writer filled logo_hash first
	Rejected   int // moderation said no
	Errors     int
	Quota      bool // the daily image quota stopped the run
}

func (s Stats) String() string {
	return fmt.Sprintf("source=%s candidates=%d missing=%d would=%d uploaded=%d raced=%d rejected=%d errors=%d quota=%t",
		s.Source, s.Candidates, s.Missing, s.Would, s.Uploaded, s.Raced, s.Rejected, s.Errors, s.Quota)
}

// Run resolves the candidate labels and forecasts (dry) or backfills (apply)
// their logos.
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

	// The falsification set is produced BEFORE any write and independently of
	// the candidate set, so auditing never depends on the thing it audits.
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

	// The mirror is optional for a dry run: before the crawler has fetched
	// anything there is no directory at all, and the whole point of that first
	// dry run is to size the population and emit the ids file.
	m, err := loadMirror(opts.MirrorDir, opts.Source)
	if err != nil {
		return nil, err
	}

	st := &Stats{Source: opts.Source.Key, Candidates: len(cands)}

	// Written before any upload so a dry run is a complete planning step on its
	// own: it is exactly the list of ids whose bytes are still missing.
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

// newClient builds the catalog image client and proves the service is reachable
// before the first byte, rather than after five thousand failed attempts.
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
