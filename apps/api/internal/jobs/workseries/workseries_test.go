package workseries

import (
	"context"
	"fmt"
	"os"
	"testing"

	"api/internal/platform/catalog/migrate"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/seed"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Integration test: catalog Gold schema + a minimal dlsite mirror fixture in
// its OWN schema (workseries_dl) via a search_path DSN (the workratings /
// workplaytime pattern — public.works belongs to importer_test.go).
var (
	testDB    *gorm.DB
	testDSN   string
	dlTestDSN string
)

func TestMain(m *testing.M) {
	testDSN = os.Getenv("TEST_DATABASE_DSN")
	if testDSN == "" {
		testDSN = "host=localhost port=5432 user=postgres password=postgres dbname=kun_catalog_test sslmode=disable"
	}
	db, err := gorm.Open(postgres.Open(testDSN), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
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
	for _, ddl := range []string{
		`CREATE SCHEMA IF NOT EXISTS workseries_dl`,
		`CREATE TABLE IF NOT EXISTS workseries_dl.works (workno text PRIMARY KEY, product_json jsonb)`,
	} {
		if err := db.Exec(ddl).Error; err != nil {
			fmt.Fprintf(os.Stderr, "SKIP: mirror fixture failed: %v\n", err)
			os.Exit(0)
		}
	}
	dlTestDSN = testDSN + " options='-csearch_path=workseries_dl'"
	testDB = db
	os.Exit(m.Run())
}

func clean(t *testing.T) {
	t.Helper()
	for _, table := range []string{
		"catalog_series_member", "catalog_series", "catalog_external_ref",
		"catalog_release", "catalog_work", "workseries_dl.works",
	} {
		require.NoError(t, testDB.Exec("TRUNCATE "+table+" RESTART IDENTITY CASCADE").Error)
	}
}

func mediumID(t *testing.T) int16 {
	t.Helper()
	var id int16
	require.NoError(t, testDB.Raw(`SELECT id FROM catalog_medium WHERE key = 'galgame'`).Scan(&id).Error)
	require.NotZero(t, id)
	return id
}

// mkAnchoredWork creates a galgame work + release + dlsite release anchor.
func mkAnchoredWork(t *testing.T, medium int16, name, workno string) int64 {
	t.Helper()
	w := model.CatalogWork{MediumID: medium, OLang: "ja", DisplayName: name}
	require.NoError(t, testDB.Create(&w).Error)
	rel := model.CatalogRelease{WorkID: w.ID, Kind: model.ReleaseKindDigital}
	require.NoError(t, testDB.Create(&rel).Error)
	require.NoError(t, testDB.Create(&model.CatalogExternalRef{
		EntityType: model.EntityTypeRelease, EntityID: rel.ID, SourceID: 4,
		ExternalID: workno, LinkKind: model.LinkKindExact, MatchedBy: "rule:test",
	}).Error)
	return w.ID
}

func mkMirrorWork(t *testing.T, workno, seriesID, seriesName string) {
	t.Helper()
	require.NoError(t, testDB.Exec(`INSERT INTO workseries_dl.works (workno, product_json)
		VALUES (?, jsonb_build_object('series_id', ?::text, 'series_name', ?::text))`, workno, seriesID, seriesName).Error)
}

// TestImportWorkSeries pins the whole lane: the >=2 materialization gate, the
// refresh semantics (rename in place / member add + stale delete / sub-gate
// series deleted), dry-run zero writes and second-apply idempotence.
func TestImportWorkSeries(t *testing.T) {
	clean(t)
	medium := mediumID(t)

	wA := mkAnchoredWork(t, medium, "s1-a", "RJ100")
	wB := mkAnchoredWork(t, medium, "s1-b", "RJ101")
	wC := mkAnchoredWork(t, medium, "solo", "RJ200")
	mkAnchoredWork(t, medium, "noseries", "RJ300")
	mkMirrorWork(t, "RJ100", "SRI001", "テスト系列")
	mkMirrorWork(t, "RJ101", "SRI001", "テスト系列")
	mkMirrorWork(t, "RJ200", "SRI002", "一人系列") // single member — gated out
	mkMirrorWork(t, "RJ300", "", "")

	ctx := context.Background()
	opts := Opts{DSN: testDSN, DlsiteDSN: dlTestDSN}

	// Dry: plan only.
	st, err := Run(ctx, opts)
	require.NoError(t, err)
	assert.Equal(t, 4, st.AnchoredWorks)
	assert.Equal(t, 1, st.SeriesEligible, "single-member series gated out")
	assert.Equal(t, 2, st.MembersWanted)
	assert.Equal(t, 1, st.SeriesCreated)
	assert.Equal(t, 2, st.MembersAdded)
	var n int64
	require.NoError(t, testDB.Table("catalog_series").Count(&n).Error)
	assert.Zero(t, n, "dry run must not write")

	// Apply.
	opts.Apply = true
	st, err = Run(ctx, opts)
	require.NoError(t, err)
	assert.Equal(t, 1, st.SeriesCreated)
	assert.Equal(t, 2, st.MembersAdded)
	assert.Zero(t, st.Errors)
	var se model.CatalogSeries
	require.NoError(t, testDB.Where("external_id = 'SRI001'").First(&se).Error)
	assert.Equal(t, "テスト系列", se.DisplayName)
	require.NoError(t, testDB.Table("catalog_series_member").Where("series_id = ?", se.ID).Count(&n).Error)
	assert.EqualValues(t, 2, n)

	// The reaper also maintains the wave-184 ordering facets: a freshly created
	// series lands ordered, never at the 0 sentinel.
	var positions []int16
	require.NoError(t, testDB.Table("catalog_series_member").Where("series_id = ?", se.ID).
		Order("position").Pluck("position", &positions).Error)
	assert.Equal(t, []int16{1, 2}, positions)
	assert.Equal(t, 2, st.OrderChanged)

	// Second apply: zero writes — including the ordering pass, which recomputes
	// every series every run and must therefore prove it changes nothing.
	st, err = Run(ctx, opts)
	require.NoError(t, err)
	assert.Zero(t, st.SeriesCreated+st.SeriesRenamed+st.SeriesDeleted+st.MembersAdded+st.MembersStale)
	assert.Zero(t, st.OrderChanged)

	// Refresh: rename + membership move (wB leaves, wC joins) + verify stale.
	require.NoError(t, testDB.Exec(`UPDATE workseries_dl.works SET product_json =
		jsonb_set(product_json, '{series_name}', '"新名前"') WHERE workno = 'RJ100'`).Error)
	require.NoError(t, testDB.Exec(`UPDATE workseries_dl.works SET product_json =
		jsonb_build_object('series_id', '', 'series_name', '') WHERE workno = 'RJ101'`).Error)
	require.NoError(t, testDB.Exec(`UPDATE workseries_dl.works SET product_json =
		jsonb_build_object('series_id', 'SRI001', 'series_name', '新名前') WHERE workno = 'RJ200'`).Error)
	st, err = Run(ctx, opts)
	require.NoError(t, err)
	assert.Equal(t, 1, st.SeriesRenamed)
	assert.Equal(t, 1, st.MembersAdded, "wC joins")
	assert.Equal(t, 1, st.MembersStale, "wB leaves")
	se = model.CatalogSeries{}
	require.NoError(t, testDB.Where("external_id = 'SRI001'").First(&se).Error)
	assert.Equal(t, "新名前", se.DisplayName)
	var members []int64
	require.NoError(t, testDB.Table("catalog_series_member").Where("series_id = ?", se.ID).Order("work_id").Pluck("work_id", &members).Error)
	assert.Equal(t, []int64{wA, wC}, members)
	_ = wB

	// Sub-gate: RJ200 leaves too → series drops below 2 → deleted entirely.
	require.NoError(t, testDB.Exec(`UPDATE workseries_dl.works SET product_json =
		jsonb_build_object('series_id', '', 'series_name', '') WHERE workno = 'RJ200'`).Error)
	st, err = Run(ctx, opts)
	require.NoError(t, err)
	assert.Equal(t, 1, st.SeriesDeleted)
	require.NoError(t, testDB.Table("catalog_series").Count(&n).Error)
	assert.Zero(t, n)
	require.NoError(t, testDB.Table("catalog_series_member").Count(&n).Error)
	assert.Zero(t, n, "members cascade with the series")
}
