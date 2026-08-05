package jobs

// This test needs a CATALOG database (catalog_character), which is a different
// database from the galgame-wiki DB that TEST_DATABASE_DSN points at in this
// package. It is therefore gated on its own env var, TEST_CATALOG_DATABASE_DSN,
// and skips when unset (CI / a wiki-only run still passes). Run it explicitly:
//
//	TEST_CATALOG_DATABASE_DSN="host=127.0.0.1 port=5432 user=postgres password=... dbname=kun_catalog_test sslmode=disable" \
//	  go test ./internal/jobs/ -run TestCatalogRefping

import (
	"context"
	"os"
	"sort"
	"testing"

	catalogmodel "api/internal/platform/catalog/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// hashes used across the catalog refping test (sha-256 = 64 hex chars).
const (
	hCharA      = "aaaa111111111111111111111111111111111111111111111111111111111111"
	hCharB      = "bbbb222222222222222222222222222222222222222222222222222222222222"
	hCharC      = "cccc333333333333333333333333333333333333333333333333333333333333"
	hCharD      = "3333999999999999999999999999999999999999999999999999999999999999"
	hCoverB     = "dddd444444444444444444444444444444444444444444444444444444444444"
	hCoverSha   = "eeee555555555555555555555555555555555555555555555555555555555555"
	hCharCoverX = "ffff666666666666666666666666666666666666666666666666666666666666"
	hShotB      = "1111777777777777777777777777777777777777777777777777777777777777"
	hShotSha    = "2222888888888888888888888888888888888888888888888888888888888888"
	hLogoA      = "4444aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	hLogoB      = "5555bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	hPhotoA     = "6666cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	hPhotoB     = "7777dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
)

// migrateCatalogRefpingTables migrates the small set the catalog refping query
// touches (character portraits + work covers + work screenshots + label logos)
// plus the registry + work rows the media FKs need. All in one AutoMigrate call
// so GORM orders the FKs. Then wipes them for a clean slate.
func migrateCatalogRefpingTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.AutoMigrate(
		&catalogmodel.CatalogMedium{}, &catalogmodel.CatalogSource{},
		&catalogmodel.CatalogWork{}, &catalogmodel.CatalogCharacter{},
		&catalogmodel.CatalogWorkCover{}, &catalogmodel.CatalogWorkScreenshot{},
		&catalogmodel.CatalogOrg{}, &catalogmodel.CatalogLabel{},
		&catalogmodel.CatalogPerson{}))
	// Truncate catalog_work too (CASCADE clears its cover/screenshot children):
	// the claimed work carries a claim-unique (medium, site, product_work_id), so
	// leaving it behind collides on a second run against the same DB.
	require.NoError(t, db.Exec(`TRUNCATE catalog_work, catalog_character, catalog_label, catalog_person RESTART IDENTITY CASCADE`).Error)
	// A medium + source the work / cover FKs reference (upsert — the DB may be
	// shared with the fully-seeded handler tests).
	require.NoError(t, db.Exec(`INSERT INTO catalog_medium (id, key, name_cn) VALUES (1,'galgame','G') ON CONFLICT (id) DO NOTHING`).Error)
	require.NoError(t, db.Exec(`INSERT INTO catalog_source (id, key, trust_tier) VALUES (1,'user',0) ON CONFLICT (id) DO NOTHING`).Error)
}

func TestCatalogRefping_CollectsLiveNonNullPortraitHashes(t *testing.T) {
	dsn := os.Getenv("TEST_CATALOG_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_CATALOG_DATABASE_DSN not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	migrateCatalogRefpingTables(t, db) // also empties catalog_work_cover so only portraits count here

	ctx := context.Background()
	mk := func(hash *string) *catalogmodel.CatalogCharacter {
		c := &catalogmodel.CatalogCharacter{DisplayName: "x", ImageHash: hash}
		require.NoError(t, db.Create(c).Error)
		return c
	}
	sp := func(s string) *string { return &s }

	mk(sp(hCharA)) // live, hCharA
	mk(sp(hCharA)) // live, duplicate of hCharA → deduped
	mk(sp(hCharC)) // live, hCharC
	mk(nil)        // no portrait → excluded
	mk(sp(""))     // empty hash → excluded
	softDeleted := mk(sp(hCharB))
	require.NoError(t, db.Delete(softDeleted).Error) // soft-deleted → excluded

	got, err := collectCatalogRefpingHashes(ctx, db)
	require.NoError(t, err)
	sort.Strings(got)

	want := []string{hCharA, hCharC}
	sort.Strings(want)
	assert.Equal(t, want, got, "only live, non-empty, non-deleted image_hash values, deduped")
}

// TestCatalogRefping_IncludesBodylessAndShadowedMedia pins the step-53/54 byte
// fuse: the refping hash universe UNIONs catalog_work_cover AND
// catalog_work_screenshot — including media rows on a CLAIMED work (SHADOWED
// bodyless media, §8.B shadow-never-delete). Missing a shadowed row would let GC
// eat a live image (the 66k-frozen class).
func TestCatalogRefping_IncludesBodylessAndShadowedMedia(t *testing.T) {
	dsn := os.Getenv("TEST_CATALOG_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_CATALOG_DATABASE_DSN not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	migrateCatalogRefpingTables(t, db)

	ctx := context.Background()
	sp := func(s string) *string { return &s }

	// A live character portrait (hCharCoverX).
	require.NoError(t, db.Create(&catalogmodel.CatalogCharacter{DisplayName: "x", ImageHash: sp(hCharCoverX)}).Error)

	// A BODYLESS work with a native cover (hCoverB) + a native screenshot (hShotB).
	bodyless := &catalogmodel.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "bodyless"}
	require.NoError(t, db.Create(bodyless).Error)
	require.NoError(t, db.Create(&catalogmodel.CatalogWorkCover{
		WorkID: bodyless.ID, ImageHash: hCoverB, SourceID: 1}).Error)
	require.NoError(t, db.Create(&catalogmodel.CatalogWorkScreenshot{
		WorkID: bodyless.ID, ImageHash: hShotB, SourceID: 1}).Error)

	// A CLAIMED work (site=galgame_wiki) that still carries native media rows —
	// a SHADOWED cover (hCoverSha) + a SHADOWED screenshot (hShotSha) the read
	// face's strict XOR ignores, but whose bytes remain in catalog scope and so
	// MUST still be pinged.
	claimed := &catalogmodel.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "claimed",
		Site: sp("galgame_wiki"), ProductWorkID: func() *int64 { v := int64(999); return &v }()}
	require.NoError(t, db.Create(claimed).Error)
	require.NoError(t, db.Create(&catalogmodel.CatalogWorkCover{
		WorkID: claimed.ID, ImageHash: hCoverSha, SourceID: 1}).Error)
	require.NoError(t, db.Create(&catalogmodel.CatalogWorkScreenshot{
		WorkID: claimed.ID, ImageHash: hShotSha, SourceID: 1}).Error)

	got, err := collectCatalogRefpingHashes(ctx, db)
	require.NoError(t, err)
	sort.Strings(got)

	want := []string{hCharCoverX, hCoverB, hCoverSha, hShotB, hShotSha}
	sort.Strings(want)
	assert.Equal(t, want, got, "character portrait + bodyless cover/screenshot + SHADOWED claimed cover/screenshot all pinged")
}

// TestCatalogRefping_IncludesFullBodyFigures pins the second character image
// column into the keep-alive universe.
//
// A character carries TWO independent images — the bust (image_hash) and the
// full-body figure (figure_hash) — and figure_hash was added a wave after this
// job existed. Leaving it out of the union breaks nothing observable: uploads
// succeed, the read face serves the URL, and the bytes are quietly collected
// once the TTL elapses. This test is the only thing standing between that and
// a silent loss discovered a year later.
func TestCatalogRefping_IncludesFullBodyFigures(t *testing.T) {
	dsn := os.Getenv("TEST_CATALOG_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_CATALOG_DATABASE_DSN not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	migrateCatalogRefpingTables(t, db)

	sp := func(s string) *string { return &s }
	mk := func(img, fig *string) *catalogmodel.CatalogCharacter {
		c := &catalogmodel.CatalogCharacter{DisplayName: "x", ImageHash: img, FigureHash: fig}
		require.NoError(t, db.Create(c).Error)
		return c
	}

	mk(sp(hCharA), sp(hCharB)) // both slots on one character → both kept alive
	mk(nil, sp(hCharC))        // figure only, no bust → still kept alive
	mk(sp(hCharA), nil)        // bust only
	mk(nil, sp(""))            // empty figure → excluded
	gone := mk(nil, sp(hCharD))
	require.NoError(t, db.Delete(gone).Error) // soft-deleted → excluded

	got, err := collectCatalogRefpingHashes(context.Background(), db)
	require.NoError(t, err)
	sort.Strings(got)

	want := []string{hCharA, hCharB, hCharC}
	sort.Strings(want)
	assert.Equal(t, want, got, "both image_hash and figure_hash must be kept alive")
}

// TestCatalogRefping_IncludesLabelLogos pins the wave-170 byte fuse: label brand
// logos (catalog_label.logo_hash) are catalog-scope bytes with exactly one home
// row, so they must join the keep-alive union. Unlike the character/work
// columns, logo_hash is NOT NULL DEFAULT empty-string — the "no logo" value is
// the empty string, not NULL, and a filter that only excluded NULL would ping it
// forever and report
// it not_found (which trips the all-not-found alarm).
func TestCatalogRefping_IncludesLabelLogos(t *testing.T) {
	dsn := os.Getenv("TEST_CATALOG_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_CATALOG_DATABASE_DSN not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	migrateCatalogRefpingTables(t, db)

	mk := func(logo string) *catalogmodel.CatalogLabel {
		l := &catalogmodel.CatalogLabel{DisplayName: "brand", LogoHash: logo}
		require.NoError(t, db.Create(l).Error)
		return l
	}

	mk(hLogoA) // live logo
	mk(hLogoA) // duplicate → deduped
	mk("")     // no logo (the NOT NULL default) → excluded
	gone := mk(hLogoB)
	require.NoError(t, db.Delete(gone).Error) // soft-deleted → excluded

	got, err := collectCatalogRefpingHashes(context.Background(), db)
	require.NoError(t, err)
	sort.Strings(got)

	assert.Equal(t, []string{hLogoA}, got, "live, non-empty label logos only, deduped")
}

// TestCatalogRefping_IncludesPersonPhotos pins the wave-172 byte fuse. A person
// photograph is a catalog-scope image with exactly one home row, stored NOT NULL
// DEFAULT empty-string like the label logo — so the "no photo" value is "" and a
// NULL-only filter would ping it forever and report it not_found. Leaving the
// column out of the union breaks nothing observable: uploads succeed, the read
// faces serve the URL, and the bytes are collected a year later.
func TestCatalogRefping_IncludesPersonPhotos(t *testing.T) {
	dsn := os.Getenv("TEST_CATALOG_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_CATALOG_DATABASE_DSN not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	migrateCatalogRefpingTables(t, db)

	mk := func(photo string) *catalogmodel.CatalogPerson {
		p := &catalogmodel.CatalogPerson{DisplayName: "person", PhotoHash: photo}
		require.NoError(t, db.Create(p).Error)
		return p
	}

	mk(hPhotoA) // live photo
	mk(hPhotoA) // duplicate → deduped
	mk("")      // no photo (the NOT NULL default) → excluded
	gone := mk(hPhotoB)
	require.NoError(t, db.Delete(gone).Error) // soft-deleted → excluded

	got, err := collectCatalogRefpingHashes(context.Background(), db)
	require.NoError(t, err)
	sort.Strings(got)

	assert.Equal(t, []string{hPhotoA}, got, "live, non-empty person photos only, deduped")
}
