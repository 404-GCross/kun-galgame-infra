package wikirescue

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const (
	// StepImageUsage is the step key of the kun_images ownership adoption. It is
	// exported because the CLI has to know whether a run needs the images pool
	// at all — every other step runs on the galgame/catalog pair.
	StepImageUsage = "o"

	// siteCatalog is the image_site_usage.site this step adopts hashes INTO. It
	// is the site_key the catalog image client already uploads under
	// (KUN_CATALOG_IMAGE_CLIENT_*), so the existing catalog-image-refping sweep
	// picks the adopted hashes up with no new wiring at all.
	siteCatalog = "catalog"

	// usageProbeChunk bounds one hash-classification round trip.
	usageProbeChunk = 5000
)

// unadoptableHash is one catalog-referenced hash that cannot be given a catalog
// usage row, with the reason. The wave expects this list to be short; it is
// reported rather than silently dropped because each entry is a dangling
// reference on the catalog read face and belongs in a data-QA queue.
type unadoptableHash struct {
	Hash   string `json:"hash"`
	Reason string `json:"reason"`
}

// usageClasses is the per-hash classification the plan is built from.
type usageClasses struct {
	already     int
	missing     int
	softDeleted int
	unadoptable []unadoptableHash
}

// WithImages attaches the kun_images pool that the image-usage step writes into.
// It is deliberately NOT a New parameter: every other step runs on the
// galgame/catalog pair, and making them all fail when a third database is
// unreachable would be a regression in a tool whose whole value is being
// re-runnable against production.
func (r *Runner) WithImages(db *gorm.DB) *Runner {
	r.images = db
	return r
}

// quietImages is the images handle with statement logging switched off. Every
// statement this step issues carries thousands of hashes, and GORM's slow-query
// warning prints the statement with its parameters inlined — roughly 130 KB per
// probe, hundreds of times, which buries the ledger the operator is actually
// reading. Errors are return values, not log lines, so nothing diagnostic is lost.
func (r *Runner) quietImages() *gorm.DB {
	return r.images.Session(&gorm.Session{Logger: gormlogger.Discard})
}

// stepImageUsage gives the catalog site an ownership row for every image hash the
// catalog face references (W2 ③,
// refs/plans/10-data-layer-retirement/01-w2-image-bytes.md).
//
// Why this exists. Image bytes are one global row per hash; site OWNERSHIP is a
// separate table, image_site_usage(hash, site), and it is what the site-scoped
// reference ping is allowed to keep alive: a client may only ping hashes its own
// site has a usage row for. Today 174,843 of the hashes the catalog face is about
// to reference are owned by galgame_wiki ALONE, kept alive by a sweep that reads
// the wiki tables. Drop those tables and the sweep has no source, the bytes go
// unreferenced, and the GC fuse (365d → soft delete, +30d → hard delete + S3
// object removal) relights. That is the June 2026 incident, on 174k images.
//
// Why a direct write is the only route. There is no re-attribution endpoint, no
// command, no migration anywhere in the image service — the only writers of
// image_site_usage are the upload upsert and the GC/admin delete cleanups. And
// "just re-upload it under the catalog client" cannot work: the hash keys the
// ORIGINAL upload bytes, which no longer exist (only the transcoded webp does),
// so a re-upload mints a different hash. Inserting the ownership rows is the
// route, and it is the one that touches nothing else.
//
// Red lines this step obeys, by construction:
//
//   - INSERT ... ON CONFLICT DO NOTHING and nothing else. No UPDATE, no DELETE,
//     no upload. The galgame_wiki rows are never read for mutation and never
//     written — they simply become an inert second owner, which is exactly right:
//     the GC looks at the GLOBAL last_referenced_at, so once catalog pings a hash
//     it is alive regardless of what the wiki lane does.
//   - the galgame_wiki image client/key is never touched, and no image-service
//     API is called. This step speaks to the database only.
//   - first_uploader_client is READ AT RUN TIME from an existing site=catalog row.
//     Hardcoding a client id would be a lie the moment the client is rotated, and
//     an invented one would make the audit table say a client uploaded something
//     it never saw.
//
// Adoption is restricted to hashes that have a LIVE images row. A hash with no
// images row (a dangling reference) or a soft-deleted one cannot be revived by an
// ownership row — the ping repository filters soft-deleted images out — so a row
// there would buy nothing and would permanently mask the not_found signal that is
// how a dangling reference gets noticed at all. Those hashes are reported instead.
func (r *Runner) stepImageUsage(ctx context.Context) (Stats, error) {
	st := Stats{Step: StepImageUsage}
	if r.images == nil {
		return st, errors.New("the image-usage step needs the kun_images pool — wire it with WithImages")
	}

	hashes, err := r.catalogFaceHashes(ctx)
	if err != nil {
		return st, err
	}
	st.Source = len(hashes)

	client, err := r.catalogUploaderClient(ctx)
	if err != nil {
		return st, err
	}

	pending, cls, err := r.classifyHashes(ctx, hashes)
	if err != nil {
		return st, err
	}
	st.Anchored = cls.already + len(pending)
	st.Parked = cls.missing + cls.softDeleted
	st.Planned = len(pending)
	st.Note = fmt.Sprintf("images db=%s site=%s client=%s already=%d missing_image_row=%d soft_deleted=%d",
		r.images.Migrator().CurrentDatabase(), siteCatalog, client, cls.already, cls.missing, cls.softDeleted)

	if err := r.park("o-unadoptable-hashes", cls.unadoptable); err != nil {
		return st, err
	}
	if !r.opts.Apply {
		return st, nil
	}

	now := time.Now().UTC()
	rows := make([][]any, 0, len(pending))
	for _, h := range pending {
		rows = append(rows, []any{h, siteCatalog, client, now, 1, now})
	}
	err = r.quietImages().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		landed, err := insertReturning(tx, "image_site_usage",
			[]string{"hash", "site", "first_uploader_client", "first_uploaded_at", "upload_count", "last_uploaded_at"},
			"", rows)
		if err != nil {
			return err
		}
		st.Written = len(landed)
		return nil
	})
	return st, err
}

// catalogFaceHashes reads every image hash the catalog face references: the two
// work media tables plus the character portraits. This is the same set the
// catalog reference ping sweeps (jobs/catalog_image_refping.go), deliberately —
// ownership has to cover exactly what the ping will ask for, or the ping reports
// not_found and the byte stays unprotected.
func (r *Runner) catalogFaceHashes(ctx context.Context) ([]string, error) {
	var hashes []string
	if err := r.catalog.WithContext(ctx).Raw(
		`SELECT DISTINCT h FROM (
		     SELECT btrim(image_hash) AS h FROM catalog_work_cover
		     UNION SELECT btrim(image_hash) FROM catalog_work_screenshot
		     UNION SELECT btrim(image_hash) FROM catalog_character
		       WHERE image_hash IS NOT NULL AND deleted_at IS NULL
		 ) t WHERE h <> ''`).Scan(&hashes).Error; err != nil {
		return nil, fmt.Errorf("read catalog face hashes: %w", err)
	}
	return hashes, nil
}

// catalogUploaderClient reads the client id already recorded for the catalog site
// (see the red lines on stepImageUsage). It fails loudly when the site has no
// rows at all: that means the catalog image client has never uploaded anything
// here, and this step has no business inventing an owner for 174k images.
func (r *Runner) catalogUploaderClient(ctx context.Context) (string, error) {
	var client string
	if err := r.images.WithContext(ctx).Raw(
		`SELECT first_uploader_client FROM image_site_usage
		 WHERE site = ? AND coalesce(first_uploader_client, '') <> ''
		 ORDER BY id LIMIT 1`, siteCatalog).Scan(&client).Error; err != nil {
		return "", fmt.Errorf("read catalog uploader client: %w", err)
	}
	if client == "" {
		return "", fmt.Errorf("no image_site_usage row for site %q carries a client — refusing to invent an owner", siteCatalog)
	}
	return client, nil
}

// classifyHashes splits the catalog-referenced hashes into the ones to adopt and
// the ones that cannot be adopted, in chunked round trips. A hash absent from the
// result set has no images row at all; the rest carry their soft-delete state and
// whether the catalog site already owns them.
func (r *Runner) classifyHashes(ctx context.Context, hashes []string) ([]string, usageClasses, error) {
	var cls usageClasses
	pending := make([]string, 0, len(hashes))
	for start := 0; start < len(hashes); start += usageProbeChunk {
		end := min(start+usageProbeChunk, len(hashes))
		batch := hashes[start:end]

		var rows []struct {
			Hash        string `gorm:"column:hash"`
			SoftDeleted bool   `gorm:"column:soft_deleted"`
			HasUsage    bool   `gorm:"column:has_usage"`
		}
		if err := r.quietImages().WithContext(ctx).Raw(
			`SELECT btrim(i.hash) AS hash,
			        (i.deleted_at IS NOT NULL) AS soft_deleted,
			        EXISTS (SELECT 1 FROM image_site_usage u
			                WHERE u.hash = i.hash AND u.site = ?) AS has_usage
			 FROM images i WHERE i.hash IN ?`, siteCatalog, batch).Scan(&rows).Error; err != nil {
			return nil, cls, fmt.Errorf("classify image hashes: %w", err)
		}

		found := make(map[string]struct{}, len(rows))
		for _, row := range rows {
			found[row.Hash] = struct{}{}
			switch {
			case row.HasUsage:
				cls.already++
			case row.SoftDeleted:
				cls.softDeleted++
				cls.unadoptable = append(cls.unadoptable, unadoptableHash{Hash: row.Hash, Reason: "soft_deleted"})
			default:
				pending = append(pending, row.Hash)
			}
		}
		for _, h := range batch {
			if _, ok := found[h]; !ok {
				cls.missing++
				cls.unadoptable = append(cls.unadoptable, unadoptableHash{Hash: h, Reason: "no_image_row"})
			}
		}
	}
	return pending, cls, nil
}
