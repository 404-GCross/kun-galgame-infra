package tagcanon

import (
	"context"
	"fmt"
	"os"
	"testing"

	"api/internal/platform/catalog/migrate"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/seed"
	"api/internal/testsupport/dbtest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var (
	testDB  *gorm.DB
	testDSN string
)

// TestMain takes the assigned DSN and NOTHING else. It used to fall back to a
// hard-coded localhost/kun_catalog_test, which is the exact failure dbtest was
// written to prevent: a suite that silently adopts whichever database happens
// to be listening, including one another track is mid-run against.
//
// It also no longer exits the package when there is no database — the tagMap
// reader here is a pure function, and a package-level exit reported it as `ok`
// while running none of it.
func TestMain(m *testing.M) {
	var ok bool
	if testDSN, ok = dbtest.DSN(); !ok {
		fmt.Fprintln(os.Stderr, "SKIP: no TEST_DATABASE_DSN — DB-backed tagcanon tests will skip individually")
		os.Exit(m.Run())
	}
	db, err := gorm.Open(postgres.Open(testDSN), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: cannot connect to the assigned test database: %v\n", err)
		os.Exit(1)
	}
	if err := migrate.Run(db); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: catalog migrate failed: %v\n", err)
		os.Exit(1)
	}
	if err := seed.Run(db); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: catalog seed failed: %v\n", err)
		os.Exit(1)
	}
	testDB = db
	os.Exit(m.Run())
}

func cleanTagcanon(t *testing.T) {
	t.Helper()
	if testDB == nil {
		dbtest.Skip(t)
	}
	for _, table := range []string{
		"catalog_tag_source_map", "catalog_tag", "catalog_work_tag", "catalog_tag_rejection",
	} {
		require.NoError(t, testDB.Exec("TRUNCATE "+table+" RESTART IDENTITY CASCADE").Error)
	}
}

func srcID(t *testing.T, key string) int16 {
	t.Helper()
	var id int16
	require.NoError(t, testDB.Raw(`SELECT id FROM catalog_source WHERE key = ?`, key).Scan(&id).Error)
	require.NotZero(t, id, "source %s seeded", key)
	return id
}

func mkWorkTag(t *testing.T, workID int64, name string, count int, source int16) {
	t.Helper()
	require.NoError(t, testDB.Create(&model.CatalogWorkTag{WorkID: workID, Name: name, Count: count, SourceID: source}).Error)
}

func mkBodylessWork(t *testing.T, medium int16) int64 {
	t.Helper()
	w := model.CatalogWork{MediumID: medium, OLang: "ja", DisplayName: "t"}
	require.NoError(t, testDB.Create(&w).Error)
	return w.ID
}

func TestTagcanonRun(t *testing.T) {
	cleanTagcanon(t)
	ctx := context.Background()
	vndb, bgm, dl := srcID(t, "vndb"), srcID(t, "bangumi"), srcID(t, "dlsite")
	var medium int16
	require.NoError(t, testDB.Raw(`SELECT id FROM catalog_medium WHERE key='galgame'`).Scan(&medium).Error)

	wV1, wV2, wV3 := mkBodylessWork(t, medium), mkBodylessWork(t, medium), mkBodylessWork(t, medium)
	mkWorkTag(t, wV1, "奇幻", 0, vndb)
	mkWorkTag(t, wV2, "奇幻", 0, vndb)
	mkWorkTag(t, wV3, "奇幻", 0, vndb)
	mkWorkTag(t, wV1, "百合", 0, vndb)
	mkWorkTag(t, wV1, "巨乳女主角", 0, vndb)

	wB := mkBodylessWork(t, medium)
	wD := mkBodylessWork(t, medium)
	mkWorkTag(t, wB, "奇幻", 5, bgm)
	mkWorkTag(t, wB, "百合", 9, bgm)
	mkWorkTag(t, wB, "像素", 2, bgm)
	mkWorkTag(t, wB, "2024", 1, bgm)
	mkWorkTag(t, wB, "抖m", 1, bgm)
	mkWorkTag(t, wD, "奇幻", 40, dl)
	mkWorkTag(t, wD, "像素", 8, dl)
	mkWorkTag(t, wD, "手交", 3, dl)

	st, err := Run(ctx, Opts{DSN: testDSN})
	require.NoError(t, err)
	assert.Equal(t, 3, st.VndbNames, "奇幻 / 百合 / 巨乳女主角")
	assert.Equal(t, 1, st.BangumiJunk, "2024 filtered")
	require.NotEmpty(t, st.JunkSamples)
	assert.Equal(t, "number", st.JunkSamples[0].Reason)
	assert.Equal(t, 3, st.Groups, "奇幻 / 百合 / 像素")
	assert.Equal(t, 1, st.MetaGroups, "像素")
	assert.Equal(t, 1, st.TriSource, "奇幻 spans vndb+bangumi+dlsite")
	assert.Equal(t, 7, st.PlannedMaps, "奇幻 3 + 百合 2 + 像素 2")
	assert.Zero(t, st.TagsCreated+st.MapsCreated, "dry writes nothing")
	assert.EqualValues(t, 0, tagRowCount(t))

	st, err = Run(ctx, Opts{DSN: testDSN, Apply: true})
	require.NoError(t, err)
	assert.Equal(t, 3, st.TagsCreated)
	assert.Equal(t, 7, st.MapsCreated)
	assert.Zero(t, st.TagsConflict+st.MapsConflict+st.Errors)
	assert.EqualValues(t, 3, tagRowCount(t))

	var qf model.CatalogTag
	require.NoError(t, testDB.Where("name = ?", "奇幻").First(&qf).Error)
	assert.EqualValues(t, model.TagTierCore, qf.Tier)
	assert.EqualValues(t, model.TagKindContent, qf.Kind)
	var maps []model.CatalogTagSourceMap
	require.NoError(t, testDB.Where("tag_id = ?", qf.ID).Order("source_id").Find(&maps).Error)
	require.Len(t, maps, 3)
	assert.Equal(t, []int16{vndb, bgm, dl}, []int16{maps[0].SourceID, maps[1].SourceID, maps[2].SourceID})
	var qm model.CatalogTag
	require.NoError(t, testDB.Where("name = ?", "像素").First(&qm).Error)
	assert.EqualValues(t, model.TagKindMeta, qm.Kind)

	st, err = Run(ctx, Opts{DSN: testDSN, Apply: true})
	require.NoError(t, err)
	assert.Zero(t, st.TagsCreated+st.MapsCreated+st.Errors, "second pass writes zero")
	assert.Equal(t, 3, st.TagsConflict)
	assert.Equal(t, 7, st.MapsConflict)
	assert.EqualValues(t, 3, tagRowCount(t), "row count unchanged")
}

func TestDSNRequired(t *testing.T) {
	_, err := Run(context.Background(), Opts{})
	require.Error(t, err)
}

func tagRowCount(t *testing.T) int64 {
	t.Helper()
	var n int64
	require.NoError(t, testDB.Raw("SELECT count(*) FROM catalog_tag").Scan(&n).Error)
	return n
}
