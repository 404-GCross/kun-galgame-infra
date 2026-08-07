package seriesorder

import (
	"context"
	"fmt"
	"os"
	"testing"

	"api/internal/platform/catalog/migrate"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/seed"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

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

func clean(t *testing.T) {
	t.Helper()
	for _, table := range []string{
		"catalog_series_member", "catalog_series",
		"catalog_work_relation", "catalog_release", "catalog_work",
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

// mkWork creates a galgame work, optionally with a dated release (y=0 → none).
func mkWork(t *testing.T, medium int16, name string, y, mo, d int16) int64 {
	t.Helper()
	w := &model.CatalogWork{MediumID: medium, OLang: "ja", DisplayName: name}
	require.NoError(t, testDB.Create(w).Error)
	if y != 0 {
		rel := &model.CatalogRelease{WorkID: w.ID, ReleasedY: &y}
		if mo != 0 {
			rel.ReleasedM = &mo
		}
		if d != 0 {
			rel.ReleasedD = &d
		}
		require.NoError(t, testDB.Create(rel).Error)
	}
	return w.ID
}

func mkEdge(t *testing.T, a, b, typ int64) {
	t.Helper()
	require.NoError(t, testDB.Create(&model.CatalogWorkRelation{AWorkID: a, BWorkID: b, RelationTypeID: typ}).Error)
}

func mkSeries(t *testing.T, sourceKey, externalID, name string, works ...int64) int64 {
	t.Helper()
	var srcID int16
	require.NoError(t, testDB.Raw(`SELECT id FROM catalog_source WHERE key = ?`, sourceKey).Scan(&srcID).Error)
	require.NotZero(t, srcID)
	s := &model.CatalogSeries{DisplayName: name, SourceID: srcID, ExternalID: externalID}
	require.NoError(t, testDB.Create(s).Error)
	for _, w := range works {
		require.NoError(t, testDB.Create(&model.CatalogSeriesMember{SeriesID: s.ID, WorkID: w}).Error)
	}
	return s.ID
}

// TestAssignOrdersByReleaseDate pins the ordering rule: earliest dated release
// first, fuzzy dates read at the start of their year, undated members last, and
// work id breaking every tie.
func TestAssignOrdersByReleaseDate(t *testing.T) {
	clean(t)
	med := mediumID(t)
	// Created in an order that does NOT match the answer, so a result that
	// merely echoes insertion order cannot pass.
	undated := mkWork(t, med, "Undated", 0, 0, 0)
	late := mkWork(t, med, "Late", 2010, 3, 1)
	yearOnly := mkWork(t, med, "Year only", 2010, 0, 0) // 2010 → before 2010-03-01
	early := mkWork(t, med, "Early", 2004, 12, 24)
	undated2 := mkWork(t, med, "Undated too", 0, 0, 0)

	ids := []int64{undated, late, yearOnly, early, undated2}
	facts, err := LoadFacts(context.Background(), testDB, ids)
	require.NoError(t, err)
	got := facts.Assign(ids, model.SeriesMemberKindUnknown)

	want := []int64{early, yearOnly, late, undated, undated2}
	require.Len(t, got, len(want))
	for i, w := range want {
		require.Equal(t, w, got[i].WorkID, "position %d", i+1)
		require.Equal(t, int16(i+1), got[i].Position)
	}
}

// TestAssignKindFromEdges pins the kind rule: the a-side of fandisc_of/
// side_story_of takes that role, any other participation in a series-ish edge
// is a main entry, and a member no in-series edge touches takes the caller's
// fallback.
func TestAssignKindFromEdges(t *testing.T) {
	clean(t)
	med := mediumID(t)
	base := mkWork(t, med, "Base", 2000, 1, 1)
	sequel := mkWork(t, med, "Sequel", 2001, 1, 1)
	fd := mkWork(t, med, "FD", 2002, 1, 1)
	side := mkWork(t, med, "Side", 2003, 1, 1)
	lonely := mkWork(t, med, "Lonely", 2004, 1, 1)
	outsider := mkWork(t, med, "Outsider", 1999, 1, 1)

	mkEdge(t, sequel, base, RelSequelOf)
	mkEdge(t, fd, base, RelFandiscOf)
	mkEdge(t, side, base, RelSideStoryOf)
	// An edge that LEAVES the member set must not classify lonely.
	mkEdge(t, lonely, outsider, RelFandiscOf)

	members := []int64{base, sequel, fd, side, lonely}
	facts, err := LoadFacts(context.Background(), testDB, append(members, outsider))
	require.NoError(t, err)
	byWork := map[int64]int16{}
	for _, a := range facts.Assign(members, model.SeriesMemberKindUnknown) {
		byWork[a.WorkID] = a.Kind
	}
	require.Equal(t, model.SeriesMemberKindMain, byWork[base], "the target of every role edge is a main entry")
	require.Equal(t, model.SeriesMemberKindMain, byWork[sequel])
	require.Equal(t, model.SeriesMemberKindFandisc, byWork[fd])
	require.Equal(t, model.SeriesMemberKindSideStory, byWork[side])
	require.Equal(t, model.SeriesMemberKindUnknown, byWork[lonely], "an out-of-series edge is no evidence")

	// The derived lane's fallback classifies the same lonely member as main.
	byWork = map[int64]int16{}
	for _, a := range facts.Assign(members, model.SeriesMemberKindMain) {
		byWork[a.WorkID] = a.Kind
	}
	require.Equal(t, model.SeriesMemberKindMain, byWork[lonely])
}

// TestBackfillDryThenApplyThenZero is the acceptance shape: a dry run forecasts
// exactly what the apply writes, and a second apply is a zero-write.
func TestBackfillDryThenApplyThenZero(t *testing.T) {
	clean(t)
	med := mediumID(t)
	a := mkWork(t, med, "A", 2005, 6, 1)
	b := mkWork(t, med, "B", 2007, 6, 1)
	c := mkWork(t, med, "C", 0, 0, 0)
	mkEdge(t, b, a, RelSequelOf)
	sid := mkSeries(t, "dlsite", "SRI-1", "A series", a, b, c)

	ctx := context.Background()
	dry, err := BackfillWithDB(ctx, testDB, BackfillOpts{})
	require.NoError(t, err)
	require.Equal(t, 1, dry.Series)
	require.Equal(t, 3, dry.Members)
	require.Equal(t, 3, dry.MembersChanged)
	require.Zero(t, dry.TouchedWorks)

	var stillZero int64
	require.NoError(t, testDB.Raw(`SELECT count(*) FROM catalog_series_member WHERE position <> 0`).Scan(&stillZero).Error)
	require.Zero(t, stillZero, "a dry run must not write")

	app, err := BackfillWithDB(ctx, testDB, BackfillOpts{Apply: true})
	require.NoError(t, err)
	require.Equal(t, dry.MembersChanged, app.MembersChanged)
	require.Equal(t, 3, app.TouchedWorks)

	var rows []struct {
		WorkID   int64 `gorm:"column:work_id"`
		Position int16 `gorm:"column:position"`
		Kind     int16 `gorm:"column:kind"`
	}
	require.NoError(t, testDB.Raw(`SELECT work_id, position, kind FROM catalog_series_member
		WHERE series_id = ? ORDER BY position`, sid).Scan(&rows).Error)
	require.Len(t, rows, 3)
	require.Equal(t, a, rows[0].WorkID)
	require.Equal(t, b, rows[1].WorkID)
	require.Equal(t, c, rows[2].WorkID, "the undated member sorts last")
	require.Equal(t, model.SeriesMemberKindMain, rows[0].Kind)
	require.Equal(t, model.SeriesMemberKindUnknown, rows[2].Kind, "the dlsite lane never guesses a kind")

	second, err := BackfillWithDB(ctx, testDB, BackfillOpts{Apply: true})
	require.NoError(t, err)
	require.Zero(t, second.MembersChanged, "a steady-state re-run must write nothing")
	require.Zero(t, second.TouchedWorks, "and touch nothing")
}

// TestBackfillSkipsTheDerivedLane pins the ownership split: the derived builder
// assigns its own facets with its own fallback kind, so the backfill must leave
// that lane alone.
func TestBackfillSkipsTheDerivedLane(t *testing.T) {
	clean(t)
	med := mediumID(t)
	a := mkWork(t, med, "D1", 2005, 1, 1)
	b := mkWork(t, med, "D2", 2006, 1, 1)
	mkSeries(t, "derived", "comp:1", "Derived", a, b)

	st, err := BackfillWithDB(context.Background(), testDB, BackfillOpts{Apply: true})
	require.NoError(t, err)
	require.Zero(t, st.Series)
	require.Zero(t, st.MembersChanged)
}
