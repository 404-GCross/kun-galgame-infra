package galgamestats

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// The build reads galgame + the three narrow tables and writes galgame_stats.
// The test isolates all five in a dedicated schema on a single pooled
// connection (SET search_path) so unqualified table names resolve to the
// minimal fixtures and never collide with other packages' public tables.
func openFixture(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	require.NoError(t, db.Exec(`DROP SCHEMA IF EXISTS galgamestats_test CASCADE`).Error)
	require.NoError(t, db.Exec(`CREATE SCHEMA galgamestats_test`).Error)
	require.NoError(t, db.Exec(`SET search_path TO galgamestats_test`).Error)
	for _, ddl := range []string{
		`CREATE TABLE galgame (id int PRIMARY KEY, release_date date, release_precision text, release_date_tba boolean)`,
		`CREATE TABLE galgame_vndb_meta (galgame_id int PRIMARY KEY, rating double precision, vote_count int)`,
		`CREATE TABLE galgame_bangumi_meta (galgame_id int PRIMARY KEY, score double precision)`,
		`CREATE TABLE galgame_eg_meta (galgame_id int PRIMARY KEY, median int)`,
		`CREATE TABLE galgame_stats (key text PRIMARY KEY, payload jsonb NOT NULL, built_at timestamptz NOT NULL)`,
	} {
		require.NoError(t, db.Exec(ddl).Error)
	}
	t.Cleanup(func() { db.Exec(`DROP SCHEMA IF EXISTS galgamestats_test CASCADE`) })
	return db
}

func TestBuildSnapshots(t *testing.T) {
	db := openFixture(t)

	// 6 galgames; the year cohort is only date-known-to-year, non-TBA rows.
	ins := func(sql string, args ...any) { require.NoError(t, db.Exec(sql, args...).Error) }
	ins(`INSERT INTO galgame VALUES (1,'2020-05-01','day',false)`)   // 2020
	ins(`INSERT INTO galgame VALUES (2,'2020-01-01','year',false)`)  // 2020
	ins(`INSERT INTO galgame VALUES (3,'2021-06-01','month',false)`) // 2021
	ins(`INSERT INTO galgame VALUES (4,NULL,'unknown',false)`)       // excluded: no date
	ins(`INSERT INTO galgame VALUES (5,'2022-01-01','tba',true)`)    // excluded: TBA
	ins(`INSERT INTO galgame VALUES (6,'2021-03-01','day',false)`)   // 2021

	// VNDB: 3 rated (g1/g2/g6), 1 unrated (g3 rating NULL).
	ins(`INSERT INTO galgame_vndb_meta VALUES (1,80.0,100)`)
	ins(`INSERT INTO galgame_vndb_meta VALUES (2,60.0,50)`)
	ins(`INSERT INTO galgame_vndb_meta VALUES (3,NULL,0)`)
	ins(`INSERT INTO galgame_vndb_meta VALUES (6,90.0,200)`)
	// Bangumi: 2 rated (g1/g3), 1 unrated (g2 score=0).
	ins(`INSERT INTO galgame_bangumi_meta VALUES (1,8.5)`)
	ins(`INSERT INTO galgame_bangumi_meta VALUES (3,7.0)`)
	ins(`INSERT INTO galgame_bangumi_meta VALUES (2,0)`)
	// EG: 2 rated (g1/g6), 1 unrated (g2 median NULL).
	ins(`INSERT INTO galgame_eg_meta VALUES (1,75)`)
	ins(`INSERT INTO galgame_eg_meta VALUES (6,88)`)
	ins(`INSERT INTO galgame_eg_meta VALUES (2,NULL)`)

	summary, err := Build(context.Background(), db, Opts{Apply: true})
	require.NoError(t, err)
	assert.Equal(t, 6, summary["keys"])

	// coverage.
	var cov coverage
	loadPayload(t, db, keyCoverage, &cov)
	assert.Equal(t, coverage{Total: 6, VNDBRows: 4, VNDBRated: 3, BangumiRows: 3, BangumiRated: 2, EGRows: 3, EGRated: 2}, cov)

	// release_years: only cohort rows; sum ≤ total.
	var years []yearCount
	loadPayload(t, db, keyReleaseYears, &years)
	assert.Equal(t, []yearCount{{Year: 2020, Count: 2}, {Year: 2021, Count: 2}}, years)
	sum := 0
	for _, y := range years {
		sum += y.Count
	}
	assert.LessOrEqual(t, sum, cov.Total)

	// VNDB histogram: total closes with rated count; values land in the right buckets.
	var vh histogram
	loadPayload(t, db, keyHistogramVNDB, &vh)
	assert.Equal(t, 3, vh.Total, "histogram total closes with rating IS NOT NULL count")
	require.Len(t, vh.Buckets, 10)
	assert.Equal(t, 1, bucketAt(vh, 60)) // [55,64)
	assert.Equal(t, 1, bucketAt(vh, 80)) // [73,82)
	assert.Equal(t, 1, bucketAt(vh, 90)) // [82,91)
	assert.Equal(t, 3, sumBuckets(vh))

	// Bangumi / EG histograms close with their rated counts.
	var bh, eh histogram
	loadPayload(t, db, keyHistogramBGM, &bh)
	loadPayload(t, db, keyHistogramEG, &eh)
	assert.Equal(t, 2, bh.Total)
	assert.Equal(t, 2, sumBuckets(bh))
	assert.Equal(t, 2, eh.Total)
	assert.Equal(t, 2, sumBuckets(eh))

	// yearly_scores: hand-computed averages per source per year.
	var ys []yearScore
	loadPayload(t, db, keyYearlyScores, &ys)
	require.Len(t, ys, 2)
	y2020, y2021 := ys[0], ys[1]
	assert.Equal(t, 2020, y2020.Year)
	require.NotNil(t, y2020.VNDBAvg)
	assert.InDelta(t, 70.0, *y2020.VNDBAvg, 0.001) // (80+60)/2
	assert.Equal(t, 2, y2020.VNDBN)
	require.NotNil(t, y2020.BangumiAvg)
	assert.InDelta(t, 8.5, *y2020.BangumiAvg, 0.001)
	assert.Equal(t, 1, y2020.BangumiN)
	require.NotNil(t, y2020.EGAvg)
	assert.InDelta(t, 75.0, *y2020.EGAvg, 0.001)

	assert.Equal(t, 2021, y2021.Year)
	require.NotNil(t, y2021.VNDBAvg)
	assert.InDelta(t, 90.0, *y2021.VNDBAvg, 0.001) // g6 only (g3 unrated)
	assert.Equal(t, 1, y2021.VNDBN)
	require.NotNil(t, y2021.BangumiAvg)
	assert.InDelta(t, 7.0, *y2021.BangumiAvg, 0.001) // g3
	require.NotNil(t, y2021.EGAvg)
	assert.InDelta(t, 88.0, *y2021.EGAvg, 0.001) // g6

	// Idempotency: a second build keeps 6 rows and identical payloads.
	before := rawPayload(t, db, keyCoverage)
	_, err = Build(context.Background(), db, Opts{Apply: true})
	require.NoError(t, err)
	var rows int64
	require.NoError(t, db.Table("galgame_stats").Count(&rows).Error)
	assert.Equal(t, int64(6), rows, "second build must not add rows")
	assert.JSONEq(t, string(before), string(rawPayload(t, db, keyCoverage)), "payload stable across builds")

	// Empty-source resilience: dropping all EG rows yields an empty EG histogram
	// (total 0), never an error — the local-vs-production常态 difference.
	require.NoError(t, db.Exec(`DELETE FROM galgame_eg_meta`).Error)
	_, err = Build(context.Background(), db, Opts{Apply: true})
	require.NoError(t, err)
	loadPayload(t, db, keyHistogramEG, &eh)
	assert.Equal(t, 0, eh.Total)
}

func loadPayload(t *testing.T, db *gorm.DB, key string, dst any) {
	t.Helper()
	require.NoError(t, json.Unmarshal(rawPayload(t, db, key), dst))
}

func rawPayload(t *testing.T, db *gorm.DB, key string) []byte {
	t.Helper()
	var raw []byte
	require.NoError(t, db.Table("galgame_stats").Select("payload").Where("key = ?", key).Row().Scan(&raw))
	return raw
}

func bucketAt(h histogram, v float64) int {
	last := h.Buckets[len(h.Buckets)-1].Hi
	for _, b := range h.Buckets {
		if v >= b.Lo && (v < b.Hi || (v == b.Hi && b.Hi == last)) {
			return b.Count
		}
	}
	return -1
}

func sumBuckets(h histogram) int {
	n := 0
	for _, b := range h.Buckets {
		n += b.Count
	}
	return n
}
