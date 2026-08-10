// Package dlsitemedia backfills catalog-native media rows for galgame works from
// DLsite (media-aggregation wave, refs/proj/55 + refs/proj/125). Steps 52/53/54
// built the three native tables (catalog_work_intro / catalog_work_cover /
// catalog_work_screenshot), their read-face nativeWork* branches, and the catalog
// image refping — but bodyless read faces returned [] because the BYTES were
// deferred to this wave. 55 fills them.
//
// For each galgame work reachable via an EXACT DLsite workno release anchor it
// writes, all attributed to source_id = dlsite (4):
//
//   - intro: dlsite.works.page_json.parts → catalog_work_intro (lang 'ja'). Pure
//     DB, no bytes, no image service, no quota — landed first for immediate value.
//   - cover: image_main → imageclient.UploadWithSub(catalog_cover) →
//     catalog_work_cover (kind 'main', portrait_pinned=false — DLsite covers are
//     landscape).
//   - screenshot: each image_samples[i] → UploadWithSub(catalog_screenshot) →
//     catalog_work_screenshot (sort_order=i, caption=”).
//
// TWO CANDIDATE LANES (the write side of the (facet, source) XOR):
//   - BODYLESS (site=”/NULL) — every kind.
//   - CLAIMED — SCREENSHOT (refs/proj/125) and, since refs/proj/166, INTRO.
//     The intro lane was bodyless-only because a claimed work BRIDGED its intro
//     off its product body at read time; W1-pre (refs/proj/140) mirrored those
//     bodies into catalog_work_intro and deleted the bridge, so the table is now
//     the only intro storage and the gate had become the reason published works
//     read English-only. COVER keeps the claimed refusal — wave 164 flipped
//     covers onto the native table with the wiki rows already mirrored in, so a
//     claimed work's cover slot is occupied, not empty.
//
// The two claimed lanes target differently, and deliberately so:
//   - INTRO stays whole-facet fill-missing — no ja intro from ANY source
//     (loadClaimedIntroCandidates). Two same-language intros on one read face is
//     noise, so a second one is never worth writing.
//   - SCREENSHOT is PER-SOURCE fill-missing since wave 188 (the user's
//     2026-08-07 ruling, overturning refs/proj/125's fallback-only doctrine): no
//     DLSITE screenshot row (loadClaimedScreenshotCandidates). DLsite sample CG
//     is official promotional art, semantically distinct from a VNDB game
//     screenshot, and the read face renders per-source blocks — so it supplements
//     rather than dilutes. Identical bytes still collapse on (work_id, image_hash).
//
// dlsite is a catalog-native source, so its rows live in the catalog tables for
// claimed and bodyless alike.
//
// Discipline, modeled on internal/jobs/charportraits:
//   - Bytes ONLY ever come from a LOCAL mirror (--mirror-dir), produced by
//     kun-dlsite-api's `mirror` command (<root>/<workno>/<filename>). This job
//     NEVER dials DLsite.
//   - Idempotent: a preloaded existing row is skipped before any byte read;
//     writes are ON CONFLICT DO NOTHING, so a second run writes zero.
//   - --dsn (catalog) and --dlsite-dsn (staging) are ALWAYS explicit — a bare run
//     cannot touch a live DB.
//   - Freshly-uploaded hashes are reference-pinged immediately (an image sits at
//     TTL from upload time; don't wait for the nightly refping). catalog-image-refping
//     takes catalog_work_screenshot in FULL, so the claimed lane's bytes are kept
//     alive by the same nightly sweep as the bodyless ones.
//   - Only works that actually GAINED a row are touched (catalog_work.updated_at),
//     so a second --apply moves no watermark on the public changes feed.
package dlsitemedia

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"api/internal/infrastructure/database"
	"api/internal/platform/catalog/repository"
	"api/pkg/config"
	"api/pkg/imageclient"

	"gorm.io/gorm"
)

const (
	defaultTimeout = 60 * time.Second
	// metaChunk bounds how many worknos' staging JSON are held in memory at once
	// (page_json/parts can be large); the catalog writes stream per chunk.
	metaChunk = 500
)

// Kinds selects which media the run backfills. The zero value is "none"; the CLI
// defaults it to all three.
type Kinds struct{ Intro, Cover, Screenshot bool }

func (k Kinds) needsMirror() bool { return k.Cover || k.Screenshot }
func (k Kinds) needsImage() bool  { return k.Cover || k.Screenshot }
func (k Kinds) any() bool         { return k.Intro || k.Cover || k.Screenshot }

// ParseKinds turns a comma-separated flag ("all" / "intro,cover" / "screenshot")
// into a Kinds set.
func ParseKinds(s string) (Kinds, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" || s == "all" {
		return Kinds{Intro: true, Cover: true, Screenshot: true}, nil
	}
	var k Kinds
	for _, part := range strings.Split(s, ",") {
		switch strings.TrimSpace(part) {
		case "intro":
			k.Intro = true
		case "cover":
			k.Cover = true
		case "screenshot", "shot":
			k.Screenshot = true
		case "":
		default:
			return k, fmt.Errorf("unknown kind %q (want intro|cover|screenshot|all)", part)
		}
	}
	if !k.any() {
		return k, fmt.Errorf("no valid kind in %q", s)
	}
	return k, nil
}

// Opts configures a run.
type Opts struct {
	Apply        bool          // false = dry-run forecast (no uploads, no writes)
	Kinds        Kinds         // which media to backfill
	Limit        int           // max candidate works (0 = all)
	Offset       int           // skip this many candidate works (chunking)
	DSN          string        // catalog DSN — REQUIRED (rehearsal locally; live only in prod run)
	DlsiteDSN    string        // dlsite staging DSN — REQUIRED (reads product_json/page_json)
	MirrorDir    string        // local mirror root <root>/<workno>/<filename> (required for cover/screenshot)
	ImageBaseURL string        // image_service base override (point at the LOCAL dev service)
	UploadGap    time.Duration // min delay between uploads (0 = none; raise for the prod sweep)
}

// counters tallies per-kind outcomes. In a dry run the *Would fields carry the
// forecast; in apply the *Written/*Uploaded fields carry the real writes.
type counters struct {
	introWritten, introWould, introExists, introNoText int

	coverUploaded, coverWould, coverExists, coverPlaceholder, coverMissing, coverRejected, coverRefused, coverDedup int

	shotUploaded, shotWould, shotExists, shotMissing, shotRejected, shotNoSamples, shotDedup int

	errors int
}

// runner carries per-run dependencies + counters (serial, plain ints).
type runner struct {
	db         *gorm.DB
	cli        *imageclient.Client
	sourceID   int16
	gap        time.Duration
	exist      *existing
	c          counters
	pingHashes []string
	// touched collects works that actually gained an intro / cover / screenshot
	// row, so the run bumps their catalog_work.updated_at once at the end and the
	// public changes feed learns the work is worth re-pulling. Dedup hits and
	// dry-runs contribute nothing, so a second --apply moves no watermark.
	touched []int64
}

// Run resolves the candidate set, forecasts (dry) or backfills (apply) the
// selected media, and reference-pings freshly-uploaded hashes. Returns a
// loggable summary.
func Run(ctx context.Context, cfg *config.Config, opts Opts) (map[string]any, error) {
	if opts.DSN == "" {
		return nil, fmt.Errorf("catalog DSN is required (--dsn); refusing to guess — pass the rehearsal copy locally, the live catalog only in the production run")
	}
	if opts.DlsiteDSN == "" {
		return nil, fmt.Errorf("--dlsite-dsn is required (dlsite staging DB — reads product_json/page_json)")
	}
	if !opts.Kinds.any() {
		return nil, fmt.Errorf("no media kinds selected")
	}
	if opts.Kinds.needsMirror() && opts.MirrorDir == "" {
		return nil, fmt.Errorf("--mirror-dir is required for cover/screenshot (local mirror root <root>/<workno>/<filename>)")
	}

	clientCfg := cfg.CatalogImageClient
	needImage := opts.Apply && opts.Kinds.needsImage()
	if needImage && (clientCfg.ClientID == "" || clientCfg.ClientSecret == "") {
		return nil, fmt.Errorf("catalog image client not configured (set KUN_CATALOG_IMAGE_CLIENT_ID/SECRET); refusing to --apply cover/screenshot")
	}

	db, err := openGorm(opts.DSN)
	if err != nil {
		return nil, fmt.Errorf("connect catalog db: %w", err)
	}
	if sqlDB, e := db.DB(); e == nil {
		defer sqlDB.Close()
	}
	dldb, err := openGorm(opts.DlsiteDSN)
	if err != nil {
		return nil, fmt.Errorf("connect dlsite db: %w", err)
	}
	if sqlDB, e := dldb.DB(); e == nil {
		defer sqlDB.Close()
	}

	reg, err := resolveRegistry(ctx, db)
	if err != nil {
		return nil, err
	}
	cands, err := loadCandidates(ctx, db, reg, opts.Kinds, opts.Limit, opts.Offset)
	if err != nil {
		return nil, fmt.Errorf("load candidates: %w", err)
	}
	workIDs := make([]int64, len(cands))
	for i, c := range cands {
		workIDs[i] = c.WorkID
	}
	exist, err := preloadExisting(ctx, db, workIDs, reg.dlsiteSource, langJa)
	if err != nil {
		return nil, err
	}
	bodylessCands, claimedCands := laneSplit(cands)
	slog.Info("dlsite-media candidates", "candidates", len(cands),
		"bodyless", bodylessCands, "claimed", claimedCands, "apply", opts.Apply,
		"kinds", fmt.Sprintf("intro=%t cover=%t screenshot=%t", opts.Kinds.Intro, opts.Kinds.Cover, opts.Kinds.Screenshot),
		"offset", opts.Offset, "limit", opts.Limit)

	r := &runner{db: db, sourceID: reg.dlsiteSource, gap: opts.UploadGap, exist: exist}
	if needImage {
		r.cli = imageclient.New(imageclient.Config{
			BaseURL:      resolveBaseURL(cfg, clientCfg, opts.ImageBaseURL),
			CDNBase:      cfg.ImageService.CDNBase,
			ClientID:     clientCfg.ClientID,
			ClientSecret: clientCfg.ClientSecret,
			Timeout:      defaultTimeout,
		})
		hctx, hcancel := context.WithTimeout(ctx, 5*time.Second)
		defer hcancel()
		if err := r.cli.Health(hctx); err != nil {
			return nil, fmt.Errorf("image_service unreachable at %s: %w", resolveBaseURL(cfg, clientCfg, opts.ImageBaseURL), err)
		}
	}

	quota := r.process(ctx, opts, cands, dldb)

	if err := repository.TouchWorks(ctx, r.db, r.touched); err != nil {
		return nil, fmt.Errorf("touch works: %w", err)
	}

	if opts.Apply {
		for _, b := range chunk(r.pingHashes, 1000) {
			if _, err := r.cli.ReferencePing(ctx, b); err != nil {
				slog.Warn("dlsite-media reference-ping failed", "err", err)
			}
		}
	}

	sum := r.summary(opts, len(cands))
	sum["candidates_bodyless"] = bodylessCands
	sum["candidates_claimed"] = claimedCands
	slog.Info("dlsite-media done", "summary", sum)
	if quota {
		return sum, fmt.Errorf("image quota exceeded — aborted (rerun to resume; idempotent)")
	}
	return sum, nil
}

// process walks the candidates in chunks, loading each chunk's dlsite metadata in
// one query, and dispatches the enabled kinds. Returns quota=true if the image
// quota was hit (the run aborts).
func (r *runner) process(ctx context.Context, opts Opts, cands []candidate, dldb *gorm.DB) (quota bool) {
	for _, batch := range chunk(cands, metaChunk) {
		if err := ctx.Err(); err != nil {
			return false
		}
		worknos := make([]string, len(batch))
		for i, c := range batch {
			worknos[i] = c.Workno
		}
		metas, err := loadDlsiteMeta(ctx, dldb, worknos)
		if err != nil {
			r.c.errors++
			slog.Error("load dlsite meta chunk", "err", err)
			continue
		}
		for _, c := range batch {
			if err := ctx.Err(); err != nil {
				return false
			}
			m := metas[c.Workno]
			if opts.Kinds.Intro {
				r.writeIntro(ctx, c, m, opts.Apply)
			}
			if opts.Kinds.Cover {
				if r.writeCover(ctx, opts.MirrorDir, c, m, opts.Apply) {
					return true
				}
			}
			if opts.Kinds.Screenshot {
				if r.writeScreenshots(ctx, opts.MirrorDir, c, m, opts.Apply) {
					return true
				}
			}
		}
	}
	return false
}

func (r *runner) summary(opts Opts, candidates int) map[string]any {
	s := map[string]any{
		"apply":      opts.Apply,
		"candidates": candidates,
		"errors":     r.c.errors,
	}
	if opts.Kinds.Intro {
		s["intro"] = map[string]any{
			"written": r.c.introWritten, "would_write": r.c.introWould,
			"already_exists": r.c.introExists, "no_text": r.c.introNoText,
		}
	}
	if opts.Kinds.Cover {
		s["cover"] = map[string]any{
			"uploaded": r.c.coverUploaded, "would_upload": r.c.coverWould,
			"already_exists": r.c.coverExists, "placeholder_skip": r.c.coverPlaceholder,
			"missing_file": r.c.coverMissing, "rejected": r.c.coverRejected,
			"refused_claimed": r.c.coverRefused, "dedup": r.c.coverDedup,
		}
	}
	if opts.Kinds.Screenshot {
		s["screenshot"] = map[string]any{
			"uploaded": r.c.shotUploaded, "would_upload": r.c.shotWould,
			"already_exists": r.c.shotExists, "missing_file": r.c.shotMissing,
			"rejected": r.c.shotRejected, "no_samples": r.c.shotNoSamples, "dedup": r.c.shotDedup,
		}
	}
	return s
}

// laneSplit counts the two candidate lanes so a run reports how much of its work
// is the bodyless backlog vs. the claimed screenshot lane (refs/proj/125).
func laneSplit(cands []candidate) (bodyless, claimed int) {
	for _, c := range cands {
		if isBodyless(c.Site) {
			bodyless++
			continue
		}
		claimed++
	}
	return bodyless, claimed
}

func openGorm(dsn string) (*gorm.DB, error) {
	return database.OpenJob(dsn)
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

func chunk[T any](src []T, n int) [][]T {
	var out [][]T
	for i := 0; i < len(src); i += n {
		out = append(out, src[i:min(i+n, len(src))])
	}
	return out
}
