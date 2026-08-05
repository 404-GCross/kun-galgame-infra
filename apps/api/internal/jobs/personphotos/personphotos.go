// Package personphotos fills catalog_person.photo_hash — the photograph of a
// real-world individual behind credit names — from a LOCAL mirror produced by
// the kun-bangumi-api crawler (wave 172).
//
// WHY THIS LANE EXISTS. Person was, after label, the remaining catalog entity
// with no image slot: VNDB, DLsite, Getchu and EGS publish no person picture.
// Bangumi does (api.bgm.tv persons), and it is the ONLY source here — this lane
// has no second upstream, so there is no precedence question to answer and no
// ranking code to get wrong. That is the one structural difference from the
// label-logo lane (internal/jobs/labellogos), which this package otherwise
// mirrors deliberately: same candidate/idempotence shape, same upload/retry
// policy, same provenance document, same reference-ping discipline.
//
// THE ANCHOR IS AN ORDINARY EXACT IDENTITY ANCHOR: catalog_external_ref with
// entity_type=person (0), source_id=bangumi and link_kind=exact. Nothing looser
// qualifies. A guessed link would put a stranger's face on this person's page —
// the one error a reader cannot detect and the catalog cannot self-correct.
//
// PERSON anchors only, never credit-name anchors: projecting a NAME's source
// page onto its person would smuggle in exactly the identity-resolution
// judgment the entity layer keeps explicit (and the credit_name→person link is
// visibility-gated). This is the same rule CatalogPersonIntro's backfill states.
//
// THE MIRROR CONTRACT is the label lane's, byte for byte, because it is
// produced by the SAME crawler command (kun-bangumi-api fetch-person-images):
//
//	<mirror-dir>/<external_id>/logo.<ext>   ext ∈ jpg|jpeg|png|webp|gif
//	<mirror-dir>/dims.jsonl                 one {"id","file","w","h","url"} per line
//
// The stem stays "logo" — renaming it here would silently stop resolving files
// the crawler already wrote. dims.jsonl is OPTIONAL: it names the exact
// mirrored file (preferred when present), but pixel sizes are not a filter, and
// the image service measures dims + thumbhash on upload anyway.
//
// DISCIPLINE, inherited from labellogos / bangumicovers / getchuportraits:
//   - Bytes come ONLY from the local mirror. This NEVER dials Bangumi.
//   - --dsn is ALWAYS explicit — a bare run cannot touch a live database.
//   - Idempotent: a person whose photo_hash is already set is skipped before any
//     byte is read, and the UPDATE re-asserts the empty-string precondition.
//   - Dry-run is the default; --apply is the only thing that uploads or writes.
//   - Fresh hashes are reference-pinged immediately — an image sits at TTL from
//     upload time, so waiting for the nightly sweep risks uploading bytes and
//     then losing them. catalog_person.photo_hash is in that nightly union too
//     (internal/jobs/catalog_image_refping.go).
//   - Sexual/Violence do not apply: a person photograph is rated 0. The preset
//     carries no rating field, so this is simply not asserted anywhere.
package personphotos

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

	// sourceKey is the only upstream this lane reads — also the provenance
	// source string. Its catalog_source id is resolved BY KEY at run time
	// (resolveSourceID), never hardcoded.
	sourceKey = "bangumi"
	// uploaderSub stamps a machine identity onto first_uploader_sub so the
	// backfilled image rows are traceable (there is no human uploader).
	uploaderSub = "system:person-photo-backfill:bangumi"
	// fileStem is the mirrored basename without extension. It is "logo", not
	// "photo", because the crawler that writes this mirror is the same command
	// the label-logo wave used — see the package doc.
	fileStem = "logo"
)

// Opts configures a run.
type Opts struct {
	DSN       string // catalog — REQUIRED
	MirrorDir string // local mirror root — REQUIRED in apply mode
	Apply     bool
	Limit     int // max PERSONS to process (0 = all)
	Offset    int
	UploadGap time.Duration
	ImageBase string // image service base override (local dev)
	// IDsOut, when set, writes the distinct external ids the candidates still
	// need bytes for — the crawler's ids file for its mirror phase.
	IDsOut string
	// Workers is how many persons upload concurrently (0/1 = serial). The image
	// service answers in ~2s at 5% CPU — the wait is the object store, so a few
	// in flight is the whole speedup. Keep it modest: each worker holds a
	// postgres connection.
	Workers int
}

// Stats reports one run. Every candidate lands in exactly one of missing /
// would / uploaded / raced / rejected / errors, so those add up to Candidates.
type Stats struct {
	Candidates int // persons with an exact bangumi anchor and no photo yet
	Missing    int // mirror holds no bytes for this person's external id
	Would      int // dry run: bytes present, would upload
	Uploaded   int // apply: uploaded and photo_hash written
	Raced      int // uploaded, but another writer filled photo_hash first
	Rejected   int // moderation said no
	Errors     int
	Quota      bool // the daily image quota stopped the run
}

func (s Stats) String() string {
	return fmt.Sprintf("source=%s candidates=%d missing=%d would=%d uploaded=%d raced=%d rejected=%d errors=%d quota=%t",
		sourceKey, s.Candidates, s.Missing, s.Would, s.Uploaded, s.Raced, s.Rejected, s.Errors, s.Quota)
}

// Run resolves the candidate persons and forecasts (dry) or backfills (apply)
// their photographs.
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

	// The mirror is optional for a dry run: before the crawler has fetched
	// anything there is no directory at all, and the whole point of that first
	// dry run is to size the population and emit the ids file.
	m, err := loadMirror(opts.MirrorDir)
	if err != nil {
		return nil, err
	}

	st := &Stats{Candidates: len(cands)}

	// Written before any upload so a dry run is a complete planning step on its
	// own: it is exactly the list of ids whose bytes are still missing.
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

// newClient builds the catalog image client and proves the service is reachable
// before the first byte, rather than after five thousand failed attempts.
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
