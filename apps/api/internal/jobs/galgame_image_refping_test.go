package jobs

// Test requirements:
//
// These tests share the wiki test DB with the galgame/service package
// tests. Postgres truncate/insert sequences race when `go test` runs
// both packages in parallel (the default — packages run with
// GOMAXPROCS parallelism). Always use `-p 1` when running these
// alongside service tests, e.g.:
//
//   TEST_DATABASE_DSN=... go test -p 1 ./internal/platform/galgame/... ./internal/jobs/...
//
// Running this package alone is safe at any -p value.

import (
	"context"
	"fmt"
	"os"
	"sort"
	"sync/atomic"
	"testing"

	"api/internal/platform/galgame/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// dsnEnv resolves the TEST_DATABASE_DSN used elsewhere in the galgame
// test suite. Empty → tests are skipped (CI without a DB still passes).
func dsnEnv(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN not set")
	}
	return dsn
}

// openTestDB connects + AutoMigrates the tables this file touches.
// Kept self-contained so /jobs tests don't pull in service/test fixtures.
//
// Truncation is in ONE statement with CASCADE so cross-package test
// pollution (e.g. rows left by the service-package tests in
// galgame_tag_relation / galgame_link / …) is wiped together — partial
// truncation by table list was racy against the FK graph and caused
// intermittent "duplicate key" / "record not found" failures.
func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(postgres.Open(dsnEnv(t)), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.GalgameSeries{},
		&model.GalgameTag{},
		&model.GalgameTagAlias{},
		&model.GalgameOfficial{},
		&model.GalgameOfficialAlias{},
		&model.GalgameEngine{},
		&model.Galgame{},
		&model.GalgameCover{},
		&model.GalgameScreenshot{},
		&model.GalgameRevision{},
		&model.GalgamePR{},
	))
	// Single statement → atomic w.r.t. FK chain; CASCADE propagates to
	// any leftover dependent rows from the service package's tests.
	require.NoError(t, db.Exec(`
		TRUNCATE
			galgame_pr,
			galgame_revision,
			galgame_screenshot,
			galgame_cover,
			galgame
		RESTART IDENTITY CASCADE
	`).Error)
	return db
}

// makeGalgame inserts a minimal published galgame and returns its id.
// PR5 retired banner_image_hash, so the banner is set via a separate
// INSERT into galgame_cover (sort_order=0) when the caller needs one.
func makeGalgame(t *testing.T, db *gorm.DB) int {
	t.Helper()
	g := model.Galgame{
		VNDBID: fmt.Sprintf("v%d", os.Getpid()+int(testCounter.Add(1))),
		UserID: 1,
	}
	require.NoError(t, db.Create(&g).Error)
	return g.ID
}

// monotonic counter so any future t.Parallel runs don't collide on
// vndb_id. Uses sync/atomic.Uint32 directly — the prior in-file
// "atomicUint32" wrapper was misnamed (a.v += n is not atomic) and
// would not have survived `go test -race` once parallel tests were
// added.
var testCounter atomic.Uint32

// hash constants used across tests (sha-256 = 64 hex chars).
const (
	hCur1 = "1111111111111111111111111111111111111111111111111111111111111111"
	hCur2 = "2222222222222222222222222222222222222222222222222222222222222222"
	hCov1 = "3333333333333333333333333333333333333333333333333333333333333333"
	hCov2 = "4444444444444444444444444444444444444444444444444444444444444444"
	hShot = "5555555555555555555555555555555555555555555555555555555555555555"
	hRev  = "6666666666666666666666666666666666666666666666666666666666666666"
	hPR   = "7777777777777777777777777777777777777777777777777777777777777777"
)

func TestRefping_IncludesCurrentCoverAndScreenshotHashes(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	// Production migrate-galgame creates the pinned-cover partial unique
	// index; replicate it so the test environment matches.
	require.NoError(t, db.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS idx_galgame_cover_pinned
		ON galgame_cover(galgame_id) WHERE sort_order = 0
	`).Error)

	gid := makeGalgame(t, db)

	// Source 1: galgame_cover (pinned + secondary).
	//
	// Raw INSERTs — gorm.Create silently no-ops on composite-PK models
	// when the in-memory struct's PK fields are also seen as "default
	// values" by gorm's CreateOnConflict path (intermittent, depends on
	// dialect probing). Raw SQL gives us a stable contract.
	require.NoError(t, db.Exec(
		`INSERT INTO galgame_cover (galgame_id, image_hash, sort_order, sexual, violence, source, source_key, created) VALUES (?, ?, ?, 0, 0, '', '', NOW())`,
		gid, hCov1, 0,
	).Error)
	require.NoError(t, db.Exec(
		`INSERT INTO galgame_cover (galgame_id, image_hash, sort_order, sexual, violence, source, source_key, created) VALUES (?, ?, ?, 0, 0, '', '', NOW())`,
		gid, hCov2, 1,
	).Error)

	// Source 2: galgame_screenshot.
	require.NoError(t, db.Exec(
		`INSERT INTO galgame_screenshot (galgame_id, image_hash, sort_order, caption, sexual, violence, source, source_key, created) VALUES (?, ?, ?, '', 0, 0, '', '', NOW())`,
		gid, hShot, 0,
	).Error)

	// Sanity: confirm the inserts actually landed before we test the
	// hash collector.
	var coverCnt, shotCnt int64
	db.Model(&model.GalgameCover{}).Where("galgame_id = ?", gid).Count(&coverCnt)
	db.Model(&model.GalgameScreenshot{}).Where("galgame_id = ?", gid).Count(&shotCnt)
	require.Equal(t, int64(2), coverCnt, "covers visible after insert")
	require.Equal(t, int64(1), shotCnt, "screenshot visible after insert")

	got, err := collectRefpingHashes(ctx, db)
	require.NoError(t, err)
	sort.Strings(got)

	// Expect every current hash (cover + screenshot only — PR5 retired
	// banner_image_hash, so only galgame_cover + galgame_screenshot
	// contribute to the live-data sources here).
	want := []string{hCov1, hCov2, hShot}
	sort.Strings(want)
	assert.Equal(t, want, got)
}

func TestRefping_IncludesRevisionAndPRSnapshotHashes(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	gid := makeGalgame(t, db)

	// A historical revision whose snapshot references a hash via covers
	// — the entire hash universe should pick it up so revert never dies.
	histSnap := model.Snapshot{
		VNDBID: "v_old",
		Covers: []model.SnapshotCover{
			{ImageHash: hRev, SortOrder: 0},
		},
		Screenshots: []model.SnapshotScreenshot{
			{ImageHash: hRev, SortOrder: 0},
		},
	}
	histJSON, _ := histSnap.ToJSON()
	require.NoError(t, db.Create(&model.GalgameRevision{
		GalgameID: gid, Revision: 1, UserID: 1, Action: "created",
		Snapshot: datatypes.JSON(histJSON),
	}).Error)

	// A pending PR whose snapshot references hPR (also nowhere else).
	prSnap := model.Snapshot{
		VNDBID: "v_pr",
		Covers: []model.SnapshotCover{
			{ImageHash: hPR, SortOrder: 0},
		},
	}
	prJSON, _ := prSnap.ToJSON()
	require.NoError(t, db.Create(&model.GalgamePR{
		GalgameID: gid, UserID: 1, Status: 0, BaseRevision: 1,
		Snapshot: datatypes.JSON(prJSON),
	}).Error)

	got, err := collectRefpingHashes(ctx, db)
	require.NoError(t, err)
	set := make(map[string]bool, len(got))
	for _, h := range got {
		set[h] = true
	}
	assert.True(t, set[hRev], "revision-only hash must be in refping set")
	assert.True(t, set[hPR], "PR-only hash must be in refping set")
}
