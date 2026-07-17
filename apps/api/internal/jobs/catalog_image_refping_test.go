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
	hCoverB     = "dddd444444444444444444444444444444444444444444444444444444444444"
	hCoverSha   = "eeee555555555555555555555555555555555555555555555555555555555555"
	hCharCoverX = "ffff666666666666666666666666666666666666666666666666666666666666"
)

// migrateCatalogRefpingTables migrates the small set the catalog refping query
// touches (character portraits + work covers) plus the registry + work rows the
// cover FK needs. All in one AutoMigrate call so GORM orders the FKs. Then wipes
// them for a clean slate.
func migrateCatalogRefpingTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.AutoMigrate(
		&catalogmodel.CatalogMedium{}, &catalogmodel.CatalogSource{},
		&catalogmodel.CatalogWork{}, &catalogmodel.CatalogCharacter{},
		&catalogmodel.CatalogWorkCover{}))
	require.NoError(t, db.Exec(`TRUNCATE catalog_work_cover, catalog_character RESTART IDENTITY CASCADE`).Error)
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

// TestCatalogRefping_IncludesBodylessAndShadowedCovers pins the step-53 byte
// fuse: the refping hash universe UNIONs catalog_work_cover — including a cover
// row on a CLAIMED work (a SHADOWED bodyless cover, §8.B shadow-never-delete).
// Missing the shadowed row would let GC eat a live image (the 66k-frozen class).
func TestCatalogRefping_IncludesBodylessAndShadowedCovers(t *testing.T) {
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

	// A BODYLESS work with a native cover (hCoverB).
	bodyless := &catalogmodel.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "bodyless"}
	require.NoError(t, db.Create(bodyless).Error)
	require.NoError(t, db.Create(&catalogmodel.CatalogWorkCover{
		WorkID: bodyless.ID, ImageHash: hCoverB, SourceID: 1}).Error)

	// A CLAIMED work (site=galgame_wiki) that still carries a native cover row —
	// a SHADOWED cover the read face's strict XOR ignores, but whose bytes remain
	// in catalog scope and so MUST still be pinged (hCoverSha).
	claimed := &catalogmodel.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "claimed",
		Site: sp("galgame_wiki"), ProductWorkID: func() *int64 { v := int64(999); return &v }()}
	require.NoError(t, db.Create(claimed).Error)
	require.NoError(t, db.Create(&catalogmodel.CatalogWorkCover{
		WorkID: claimed.ID, ImageHash: hCoverSha, SourceID: 1}).Error)

	got, err := collectCatalogRefpingHashes(ctx, db)
	require.NoError(t, err)
	sort.Strings(got)

	want := []string{hCharCoverX, hCoverB, hCoverSha}
	sort.Strings(want)
	assert.Equal(t, want, got, "character portrait + bodyless cover + SHADOWED claimed cover all pinged")
}
