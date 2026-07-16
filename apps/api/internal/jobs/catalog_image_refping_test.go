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
	hCharA = "aaaa111111111111111111111111111111111111111111111111111111111111"
	hCharB = "bbbb222222222222222222222222222222222222222222222222222222222222"
	hCharC = "cccc333333333333333333333333333333333333333333333333333333333333"
)

func TestCatalogRefping_CollectsLiveNonNullPortraitHashes(t *testing.T) {
	dsn := os.Getenv("TEST_CATALOG_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_CATALOG_DATABASE_DSN not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&catalogmodel.CatalogCharacter{}))
	require.NoError(t, db.Exec(`TRUNCATE catalog_character RESTART IDENTITY CASCADE`).Error)

	ctx := context.Background()
	mk := func(hash *string) *catalogmodel.CatalogCharacter {
		c := &catalogmodel.CatalogCharacter{DisplayName: "x", ImageHash: hash}
		require.NoError(t, db.Create(c).Error)
		return c
	}
	sp := func(s string) *string { return &s }

	mk(sp(hCharA))         // live, hCharA
	mk(sp(hCharA))         // live, duplicate of hCharA → deduped
	mk(sp(hCharC))         // live, hCharC
	mk(nil)                // no portrait → excluded
	mk(sp(""))             // empty hash → excluded
	softDeleted := mk(sp(hCharB))
	require.NoError(t, db.Delete(softDeleted).Error) // soft-deleted → excluded

	got, err := collectCatalogRefpingHashes(ctx, db)
	require.NoError(t, err)
	sort.Strings(got)

	want := []string{hCharA, hCharC}
	sort.Strings(want)
	assert.Equal(t, want, got, "only live, non-empty, non-deleted image_hash values, deduped")
}
