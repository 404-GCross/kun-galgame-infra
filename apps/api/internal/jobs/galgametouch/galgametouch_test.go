package galgametouch

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"api/internal/platform/catalog/migrate"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/seed"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Integration test against a real Postgres: the whole point of this package is
// one mapping SELECT against catalog_work, so there is nothing worth asserting
// without a live table.
var testDB *gorm.DB

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		dsn = "host=localhost port=5432 user=postgres password=postgres dbname=kun_catalog_test sslmode=disable"
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: cannot connect to test database: %v\n", err)
		os.Exit(0)
	}
	if err := migrate.Run(db); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: catalog migrate failed: %v\n", err)
		os.Exit(0)
	}
	if err := seed.Run(db); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: catalog seed failed: %v\n", err)
		os.Exit(0)
	}
	testDB = db
	os.Exit(m.Run())
}

var backdated = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

// claim inserts a work claimed by the given wiki galgame id and backdates its
// updated_at, so any bump is unambiguous.
func claim(t *testing.T, galgameID int) int64 {
	t.Helper()
	site := siteGalgameWiki
	pid := int64(galgameID)
	w := model.CatalogWork{
		MediumID: 1, OLang: "ja", DisplayName: fmt.Sprintf("claimed-%d", galgameID),
		Site: &site, ProductWorkID: &pid,
	}
	require.NoError(t, testDB.Create(&w).Error)
	require.NoError(t, testDB.Exec(
		`UPDATE catalog_work SET updated_at = ? WHERE id = ?`, backdated, w.ID).Error)
	return w.ID
}

func updatedAt(t *testing.T, workID int64) time.Time {
	t.Helper()
	var ts time.Time
	require.NoError(t, testDB.Raw(`SELECT updated_at FROM catalog_work WHERE id = ?`, workID).Scan(&ts).Error)
	return ts
}

// nextGalgameID hands out product_work_id values that cannot collide with rows
// another package left behind (the claim unique key is global).
var nextGalgameID = 700_000 + os.Getpid()%50_000

func galgameID() int {
	nextGalgameID++
	return nextGalgameID
}

// TestTouchClaimedOnly is the mapping contract the four sync-vndb writers lean
// on: a claimed galgame's work is bumped, and a galgame the catalog has never
// claimed is silently absent — no touch, no error. The unclaimed case is the
// steady state for the 2,000 sync-vndb drafts that were never registered.
func TestTouchClaimedOnly(t *testing.T) {
	ctx := context.Background()
	tou := New(testDB)

	claimed, unclaimed := galgameID(), galgameID()
	work := claim(t, claimed)

	require.NoError(t, tou.Touch(ctx, []int{claimed, unclaimed}))
	assert.True(t, updatedAt(t, work).After(backdated), "the claimed work is stamped")
	assert.Equal(t, 1, tou.Count(), "only the mapped work counts as touched")

	// A galgame with no claim on its own is a plain no-op.
	require.NoError(t, tou.Touch(ctx, []int{unclaimed}))
	assert.Equal(t, 1, tou.Count(), "an unmapped galgame adds nothing")
}

// TestTouchIgnoresOtherSites guards the tenant key: only site='galgame_wiki'
// claims map. A work claimed by another product that happens to carry the same
// product_work_id must never be stamped by a wiki job.
func TestTouchIgnoresOtherSites(t *testing.T) {
	ctx := context.Background()
	tou := New(testDB)

	gid := galgameID()
	mine := claim(t, gid)

	other := "moyu"
	pid := int64(gid)
	foreign := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "foreign", Site: &other, ProductWorkID: &pid}
	require.NoError(t, testDB.Create(&foreign).Error)
	require.NoError(t, testDB.Exec(`UPDATE catalog_work SET updated_at = ? WHERE id = ?`, backdated, foreign.ID).Error)

	require.NoError(t, tou.Touch(ctx, []int{gid}))
	assert.True(t, updatedAt(t, mine).After(backdated), "the galgame_wiki claim is stamped")
	assert.True(t, updatedAt(t, foreign.ID).Equal(backdated), "another tenant's work is never stamped")
}

// TestTouchSkipsSoftDeleted mirrors the changes feed's own universe: it serves
// `deleted_at IS NULL` only, so bumping a soft-deleted work would be pure noise.
func TestTouchSkipsSoftDeleted(t *testing.T) {
	ctx := context.Background()
	tou := New(testDB)

	gid := galgameID()
	work := claim(t, gid)
	require.NoError(t, testDB.Exec(
		`UPDATE catalog_work SET deleted_at = now(), updated_at = ? WHERE id = ?`, backdated, work).Error)

	require.NoError(t, tou.Touch(ctx, []int{gid}))
	assert.True(t, updatedAt(t, work).Equal(backdated), "a soft-deleted work is left alone")
	assert.Zero(t, tou.Count())
}

// TestTouchDedupsAndIgnoresEmpty covers the cheap edges: repeats collapse to one
// work, and empty / zero input never reaches the database.
func TestTouchDedupsAndIgnoresEmpty(t *testing.T) {
	ctx := context.Background()
	tou := New(testDB)

	gid := galgameID()
	work := claim(t, gid)

	require.NoError(t, tou.Touch(ctx, nil))
	require.NoError(t, tou.Touch(ctx, []int{}))
	require.NoError(t, tou.Touch(ctx, []int{0, -1}))
	assert.True(t, updatedAt(t, work).Equal(backdated), "no input means no write")
	assert.Zero(t, tou.Count())

	require.NoError(t, tou.Touch(ctx, []int{gid, gid, gid}))
	assert.Equal(t, 1, tou.Count(), "duplicates collapse to one work")
}

// TestNilToucherIsNoop is what makes "dry run stamps nothing" structural: the
// jobs simply never open a Toucher unless applying, and every call still works.
func TestNilToucherIsNoop(t *testing.T) {
	ctx := context.Background()
	gid := galgameID()
	work := claim(t, gid)

	var tou *Toucher
	require.NoError(t, tou.Touch(ctx, []int{gid}))
	tou.Close()
	assert.Zero(t, tou.Count())
	assert.True(t, updatedAt(t, work).Equal(backdated), "a dry run must not move any watermark")
}
