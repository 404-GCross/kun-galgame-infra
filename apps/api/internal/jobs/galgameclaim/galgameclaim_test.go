package galgameclaim

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"api/internal/platform/catalog/migrate"
	"api/internal/platform/catalog/seed"
	srcb "api/internal/platform/catalog/srcbangumi"
	gmodel "api/internal/platform/galgame/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// This suite covers the JOB WRAPPER only — the write gate, the summary shape,
// idempotency across nights, and the changes-feed stamp that makes a fresh claim
// visible. The registration semantics themselves (anchor grading, adoption,
// conflicts, the catalog_work_id write-back) are the Reconciler's, and are
// already pinned by catalogsync's own integration tests; re-testing them here
// would only duplicate them.
//
// Both families live in one database, exactly as production deploys them.
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
		fmt.Fprintf(os.Stderr, "SKIP: catalog migration failed: %v\n", err)
		os.Exit(0)
	}
	if err := seed.Run(db); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: catalog seeding failed: %v\n", err)
		os.Exit(0)
	}
	// The claim phase gates its bid anchor on the Silver layer, so the schema
	// must exist even when no subject row does.
	if err := srcb.EnsureSchema(db); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: src_bangumi schema failed: %v\n", err)
		os.Exit(0)
	}
	if err := db.AutoMigrate(&gmodel.Galgame{}); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: galgame automigrate failed: %v\n", err)
		os.Exit(0)
	}
	testDB = db
	os.Exit(m.Run())
}

// clean empties everything a claim run reads or writes. Identities are
// deliberately NOT restarted: the test database is shared with sibling packages
// that keep inserting into these tables, and rewinding a sequence hands them a
// primary key that is already taken.
func clean(t *testing.T) {
	t.Helper()
	for _, table := range []string{
		"catalog_external_ref", "catalog_revision", "catalog_redirect",
		"catalog_work_title", "catalog_release", "catalog_work",
		"galgame", "src_bangumi.subject",
	} {
		require.NoError(t, testDB.Exec("TRUNCATE "+table+" CASCADE").Error)
	}
}

// nextVNDB hands out vndb ids that cannot collide with rows a sibling package
// left behind in the shared test database.
var nextVNDB = 700_000 + os.Getpid()%90_000

func seedGame(t *testing.T, name string) int {
	t.Helper()
	nextVNDB++
	g := gmodel.Galgame{VNDBID: fmt.Sprintf("v%d", nextVNDB), NameJaJP: name, UserID: 1}
	require.NoError(t, testDB.Create(&g).Error)
	return g.ID
}

func count(t *testing.T, table string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, testDB.Table(table).Count(&n).Error)
	return n
}

// TestRunDryRunWritesNothing: Apply=false forecasts the claims and touches no
// table — the gate the nightly schedule is trusted with.
func TestRunDryRunWritesNothing(t *testing.T) {
	clean(t)
	seedGame(t, "ゲーム1")
	seedGame(t, "ゲーム2")

	got, err := run(context.Background(), testDB, testDB, Opts{})
	require.NoError(t, err)
	assert.Equal(t, 2, got["processed"])
	assert.Equal(t, 2, got["claimed_new"], "a dry run reports the claims it would make")
	assert.Equal(t, 2, got["vndb_exact"])
	assert.Equal(t, false, got["applied"])
	assert.Zero(t, count(t, "catalog_work"), "dry run must not write")
}

// TestRunApplyThenIdempotent is the resident-job invariant: the first night
// converges the backlog, the second claims nothing and writes nothing. It also
// pins the changes-feed evidence — a freshly claimed work carries a fresh
// updated_at with no extra stamping wired into this job.
func TestRunApplyThenIdempotent(t *testing.T) {
	clean(t)
	seedGame(t, "ゲーム1")
	seedGame(t, "ゲーム2")
	ctx := context.Background()
	before := time.Now().Add(-time.Second) // Postgres now() vs the test clock

	first, err := run(ctx, testDB, testDB, Opts{Apply: true})
	require.NoError(t, err)
	assert.Equal(t, 2, first["claimed_new"])
	assert.Equal(t, 2, first["workid_backfilled"], "each new claim backfills its wiki pointer")
	assert.Equal(t, true, first["applied"])
	assert.Equal(t, int64(2), count(t, "catalog_work"))

	// Every newly claimed work is stamped by ClaimWork's own Create — nothing in
	// this job touches updated_at, so a stale stamp here would mean the claim is
	// invisible to GET /v1/catalog/changes.
	var stale int64
	require.NoError(t, testDB.Raw(
		`SELECT count(*) FROM catalog_work WHERE site = 'galgame_wiki' AND updated_at <= ?`,
		before).Scan(&stale).Error)
	assert.Zero(t, stale, "a fresh claim must carry a fresh updated_at")

	second, err := run(ctx, testDB, testDB, Opts{Apply: true})
	require.NoError(t, err)
	assert.Zero(t, second["claimed_new"], "the second night claims nothing")
	assert.Equal(t, 2, second["already_claimed"])
	assert.Zero(t, second["workid_backfilled"], "converged pointers are not rewritten")
	assert.Equal(t, 2, second["workid_covered"])
	assert.Equal(t, int64(2), count(t, "catalog_work"), "second pass mints no work")
	assert.Equal(t, int64(2), count(t, "catalog_revision"), "second pass mints no revision")
}

// TestRunNewGameNextNight: a galgame created after the backlog converged (a
// sync-vndb draft, say) is picked up by the next run and only it.
func TestRunNewGameNextNight(t *testing.T) {
	clean(t)
	seedGame(t, "ゲーム1")
	ctx := context.Background()

	_, err := run(ctx, testDB, testDB, Opts{Apply: true})
	require.NoError(t, err)

	seedGame(t, "ゲーム2")
	got, err := run(ctx, testDB, testDB, Opts{Apply: true})
	require.NoError(t, err)
	assert.Equal(t, 2, got["processed"])
	assert.Equal(t, 1, got["claimed_new"], "only the new galgame is claimed")
	assert.Equal(t, 1, got["already_claimed"])
	assert.Equal(t, int64(2), count(t, "catalog_work"))
}
