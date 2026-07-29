package vndbscores

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"api/internal/jobs/galgametouch"
	catalogmigrate "api/internal/platform/catalog/migrate"
	catalogmodel "api/internal/platform/catalog/model"
	catalogseed "api/internal/platform/catalog/seed"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/vndb"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Both families live in one database here, exactly as they do in production —
// the catalog side supplies catalog_work (the claim + the changes-feed
// watermark), the galgame side supplies galgame_vndb_meta.
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
	if err := catalogmigrate.Run(db); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: catalog migrate failed: %v\n", err)
		os.Exit(0)
	}
	if err := catalogseed.Run(db); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: catalog seed failed: %v\n", err)
		os.Exit(0)
	}
	if err := db.AutoMigrate(&model.GalgameSeries{}, &model.Galgame{}, &model.GalgameVNDBMeta{}); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: galgame migrate failed: %v\n", err)
		os.Exit(0)
	}
	testDB = db
	os.Exit(m.Run())
}

var backdated = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

// nextVNDB hands out vndb ids that cannot collide with rows another package
// left in the shared test database.
var nextVNDB = 800_000 + os.Getpid()%90_000

// mkGalgame inserts a vndb-anchored galgame and returns it as a run candidate.
func mkGalgame(t *testing.T) candidate {
	t.Helper()
	nextVNDB++
	vid := fmt.Sprintf("v%d", nextVNDB)
	g := model.Galgame{VNDBID: vid, UserID: 1}
	require.NoError(t, testDB.Create(&g).Error)
	return candidate{ID: g.ID, VNDBID: vid}
}

// claim registers the galgame as a claimed catalog_work and backdates the
// work's updated_at so any stamp is unambiguous.
func claim(t *testing.T, galgameID int) int64 {
	t.Helper()
	site := "galgame_wiki"
	pid := int64(galgameID)
	w := catalogmodel.CatalogWork{
		MediumID: 1, OLang: "ja", DisplayName: fmt.Sprintf("scores-%d", galgameID),
		Site: &site, ProductWorkID: &pid,
	}
	require.NoError(t, testDB.Create(&w).Error)
	backdate(t, w.ID)
	return w.ID
}

func backdate(t *testing.T, workID int64) {
	t.Helper()
	require.NoError(t, testDB.Exec(
		`UPDATE catalog_work SET updated_at = ? WHERE id = ?`, backdated, workID).Error)
}

func updatedAt(t *testing.T, workID int64) time.Time {
	t.Helper()
	var ts time.Time
	require.NoError(t, testDB.Raw(`SELECT updated_at FROM catalog_work WHERE id = ?`, workID).Scan(&ts).Error)
	return ts
}

func syncedAt(t *testing.T, galgameID int) time.Time {
	t.Helper()
	var ts time.Time
	require.NoError(t, testDB.Raw(
		`SELECT synced_at FROM galgame_vndb_meta WHERE galgame_id = ?`, galgameID).Scan(&ts).Error)
	return ts
}

func ratingOf(v float64) *float64 { return &v }

// TestApplyBatchStampsOnlyRealChanges is the wave's central invariant: the
// nightly full-anchor upsert refreshes synced_at on every fetched row, but the
// changes feed only ever hears about rows whose (rating, vote_count) actually
// moved. Stamping on synced_at instead would push every claimed work through
// the feed each night.
func TestApplyBatchStampsOnlyRealChanges(t *testing.T) {
	ctx := context.Background()
	tou := galgametouch.New(testDB)
	ap := &applier{db: testDB, toucher: tou, apply: true}

	g := mkGalgame(t)
	work := claim(t, g.ID)
	t0 := time.Now().Add(-48 * time.Hour)

	// 1. A row that did not exist is a change.
	scores := map[string]vndb.VNScore{g.VNDBID: {Rating: ratingOf(82.34), VoteCount: 1234}}
	res, err := ap.applyBatch(ctx, []candidate{g}, scores, t0)
	require.NoError(t, err)
	assert.Equal(t, 1, res.written)
	assert.Equal(t, 1, tou.Count(), "a new score row stamps its work")
	assert.True(t, updatedAt(t, work).After(backdated))

	// 2. Identical values on the next run: synced_at moves, the work does not.
	backdate(t, work)
	t1 := t0.Add(time.Hour)
	res, err = ap.applyBatch(ctx, []candidate{g}, scores, t1)
	require.NoError(t, err)
	assert.Equal(t, 1, res.written, "the row is still rewritten — the watermark must not go stale")
	assert.Equal(t, 1, tou.Count(), "unchanged values stamp nothing")
	assert.True(t, updatedAt(t, work).Equal(backdated), "synced_at alone must never reach the feed")
	assert.True(t, syncedAt(t, g.ID).After(t0), "synced_at is refreshed regardless")

	// 3. A moved rating is a change.
	backdate(t, work)
	scores[g.VNDBID] = vndb.VNScore{Rating: ratingOf(85.5), VoteCount: 1234}
	_, err = ap.applyBatch(ctx, []candidate{g}, scores, t1.Add(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 2, tou.Count(), "a changed rating stamps the work")
	assert.True(t, updatedAt(t, work).After(backdated))

	// 4. A moved vote count is a change even with the rating held still.
	backdate(t, work)
	scores[g.VNDBID] = vndb.VNScore{Rating: ratingOf(85.5), VoteCount: 1300}
	_, err = ap.applyBatch(ctx, []candidate{g}, scores, t1.Add(2*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 3, tou.Count(), "a changed vote count stamps the work")
	assert.True(t, updatedAt(t, work).After(backdated))

	// 5. Losing every vote flips the rating to NULL — also a change.
	backdate(t, work)
	scores[g.VNDBID] = vndb.VNScore{Rating: ratingOf(85.5), VoteCount: 0}
	res, err = ap.applyBatch(ctx, []candidate{g}, scores, t1.Add(3*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 1, res.zeroVote)
	assert.Equal(t, 4, tou.Count(), "value → NULL stamps the work")
	assert.True(t, updatedAt(t, work).After(backdated))

	// 6. And the NULL state is itself stable.
	backdate(t, work)
	_, err = ap.applyBatch(ctx, []candidate{g}, scores, t1.Add(4*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, 4, tou.Count(), "a repeated zero-vote row stamps nothing")
	assert.True(t, updatedAt(t, work).Equal(backdated))
}

// TestApplyBatchUnclaimedAndMissing covers the two rows that must never reach
// the feed: a galgame the catalog has not claimed (no mapping — no touch, no
// error), and a VN that VNDB did not return at all (no write, so nothing to
// announce).
func TestApplyBatchUnclaimedAndMissing(t *testing.T) {
	ctx := context.Background()
	tou := galgametouch.New(testDB)
	ap := &applier{db: testDB, toucher: tou, apply: true}
	now := time.Now().Add(-24 * time.Hour)

	unclaimed := mkGalgame(t) // deliberately never claimed
	res, err := ap.applyBatch(ctx, []candidate{unclaimed},
		map[string]vndb.VNScore{unclaimed.VNDBID: {Rating: ratingOf(70), VoteCount: 9}}, now)
	require.NoError(t, err)
	assert.Equal(t, 1, res.written, "the score row is still written")
	assert.Zero(t, tou.Count(), "an unclaimed galgame maps to no work")

	missing := mkGalgame(t)
	work := claim(t, missing.ID)
	res, err = ap.applyBatch(ctx, []candidate{missing}, map[string]vndb.VNScore{}, now)
	require.NoError(t, err)
	assert.Equal(t, 1, res.apiMissing)
	assert.Zero(t, res.written)
	assert.Zero(t, tou.Count())
	assert.True(t, updatedAt(t, work).Equal(backdated), "a VN VNDB dropped stamps nothing")

	var rows int64
	require.NoError(t, testDB.Model(&model.GalgameVNDBMeta{}).
		Where("galgame_id = ?", missing.ID).Count(&rows).Error)
	assert.Zero(t, rows, "api_missing writes no row at all")
}

// TestApplyBatchDryRun: Apply=false counts what it saw and writes nothing —
// no score row, no stamp. The nil Toucher makes that structural.
func TestApplyBatchDryRun(t *testing.T) {
	ctx := context.Background()
	ap := &applier{db: testDB, toucher: nil, apply: false}

	g := mkGalgame(t)
	work := claim(t, g.ID)

	res, err := ap.applyBatch(ctx, []candidate{g},
		map[string]vndb.VNScore{g.VNDBID: {Rating: ratingOf(77.7), VoteCount: 42}}, time.Now())
	require.NoError(t, err)
	assert.Equal(t, 1, res.written, "a dry run still reports what it would write")

	var rows int64
	require.NoError(t, testDB.Model(&model.GalgameVNDBMeta{}).
		Where("galgame_id = ?", g.ID).Count(&rows).Error)
	assert.Zero(t, rows, "dry run writes no score row")
	assert.True(t, updatedAt(t, work).Equal(backdated), "dry run stamps no work")
}

func TestSameRating(t *testing.T) {
	a, b := 82.34, 82.34
	assert.True(t, sameRating(nil, nil))
	assert.True(t, sameRating(&a, &b))
	assert.False(t, sameRating(nil, &a))
	assert.False(t, sameRating(&a, nil))
	c := 82.35
	assert.False(t, sameRating(&a, &c))
}

// TestUpsertIdempotent verifies the ON CONFLICT (galgame_id) upsert: writing the
// same galgame twice UPDATEs in place (no row growth), and a zero-vote write
// persists a NULL rating rather than a fake zero.
func TestUpsertIdempotent(t *testing.T) {
	g := mkGalgame(t)
	// Scope assertions to this galgame_id so leftover rows don't interfere.
	countRows := func() int64 {
		var n int64
		require.NoError(t, testDB.Model(&model.GalgameVNDBMeta{}).Where("galgame_id = ?", g.ID).Count(&n).Error)
		return n
	}

	r1 := 70.0
	require.NoError(t, upsert(testDB, model.GalgameVNDBMeta{
		GalgameID: g.ID, VNDBID: g.VNDBID, Rating: &r1, VoteCount: 10, SyncedAt: time.Now(),
	}))
	require.Equal(t, int64(1), countRows(), "first write inserts one row")

	// Second write, same galgame_id, updated values → UPDATE, not a new row.
	r2 := 85.5
	require.NoError(t, upsert(testDB, model.GalgameVNDBMeta{
		GalgameID: g.ID, VNDBID: g.VNDBID, Rating: &r2, VoteCount: 20, SyncedAt: time.Now(),
	}))
	require.Equal(t, int64(1), countRows(), "second upsert must UPDATE, not add a row")

	var got model.GalgameVNDBMeta
	require.NoError(t, testDB.First(&got, "galgame_id = ?", g.ID).Error)
	require.NotNil(t, got.Rating)
	require.Equal(t, 85.5, *got.Rating)
	require.Equal(t, 20, got.VoteCount)

	// A subsequent zero-vote write persists NULL rating (never a fake zero).
	require.NoError(t, upsert(testDB, model.GalgameVNDBMeta{
		GalgameID: g.ID, VNDBID: g.VNDBID, Rating: nil, VoteCount: 0, SyncedAt: time.Now(),
	}))
	require.Equal(t, int64(1), countRows(), "zero-vote write still updates in place")
	var got2 model.GalgameVNDBMeta
	require.NoError(t, testDB.First(&got2, "galgame_id = ?", g.ID).Error)
	require.Nil(t, got2.Rating, "zero-vote must persist NULL rating")
	require.Equal(t, 0, got2.VoteCount)
}
