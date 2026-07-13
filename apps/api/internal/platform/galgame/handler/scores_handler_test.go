package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"api/internal/platform/galgame/dto"
	"api/internal/platform/galgame/model"
	"api/internal/platform/galgame/repository"
	"api/internal/platform/galgame/service"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	glogger "gorm.io/gorm/logger"
)

// HTTP-level integration for the cross-source read face (step 34). The three
// score tables + galgame_stats live in a dedicated schema on one pooled
// connection (SET search_path) so unqualified table names resolve to the
// fixtures and never collide with the service package's public tables.
func newScoresTestApp(t *testing.T) (*fiber.App, *gorm.DB) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: glogger.Default.LogMode(glogger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	require.NoError(t, db.Exec(`DROP SCHEMA IF EXISTS galgame_scores_test CASCADE`).Error)
	require.NoError(t, db.Exec(`CREATE SCHEMA galgame_scores_test`).Error)
	require.NoError(t, db.Exec(`SET search_path TO galgame_scores_test`).Error)
	require.NoError(t, db.AutoMigrate(
		&model.Galgame{},
		&model.GalgameVNDBMeta{},
		&model.GalgameBangumiMeta{},
		&model.GalgameEGMeta{},
		&model.GalgameStats{},
	))
	t.Cleanup(func() { db.Exec(`DROP SCHEMA IF EXISTS galgame_scores_test CASCADE`) })

	repo := repository.NewGalgameRepository(db)
	svc := service.NewGalgameService(
		repo,
		repository.NewRevisionRepository(db),
		repository.NewPRRepository(db),
		repository.NewUserReadonlyRepository(db),
	)
	h := NewGalgameHandler(svc, nil, nil)

	app := fiber.New()
	app.Get("/api/galgame/stats", h.Stats)
	app.Get("/api/galgame/:gid/scores", h.Scores)
	return app, db
}

func doGet(t *testing.T, app *fiber.App, path, ifNoneMatch string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 10 * time.Second})
	require.NoError(t, err)
	return resp
}

func decodeScores(t *testing.T, resp *http.Response) dto.GalgameScores {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var env struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &env))
	assert.Equal(t, 0, env.Code)
	var scores dto.GalgameScores
	require.NoError(t, json.Unmarshal(env.Data, &scores))
	return scores
}

func TestScoresEndpoint(t *testing.T) {
	app, db := newScoresTestApp(t)

	rating := 78.7
	median := 86
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC) // freshest
	t3 := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)

	// g1: all three sources present, all rated.
	require.NoError(t, db.Create(&model.Galgame{ID: 1, VNDBID: "v97", UserID: 1}).Error)
	require.NoError(t, db.Create(&model.GalgameVNDBMeta{GalgameID: 1, VNDBID: "v97", Rating: &rating, VoteCount: 23277, SyncedAt: t1}).Error)
	require.NoError(t, db.Create(&model.GalgameBangumiMeta{GalgameID: 1, BID: 8801, Score: 7.4, Rank: 1234, Total: 567, SyncedAt: t2}).Error)
	require.NoError(t, db.Create(&model.GalgameEGMeta{GalgameID: 1, EGGameID: 7835, Median: &median, VoteCount: 5246, SyncedAt: t3}).Error)

	// g2: only VNDB, and unrated (rating NULL) — the object is still present.
	require.NoError(t, db.Create(&model.Galgame{ID: 2, VNDBID: "v200", UserID: 1}).Error)
	require.NoError(t, db.Create(&model.GalgameVNDBMeta{GalgameID: 2, VNDBID: "v200", Rating: nil, VoteCount: 5, SyncedAt: t1}).Error)

	// g3: no narrow rows at all → three nulls.
	require.NoError(t, db.Create(&model.Galgame{ID: 3, VNDBID: "v300", UserID: 1}).Error)

	// --- g1: three sources齐 ---
	s1 := decodeScores(t, doGet(t, app, "/api/galgame/1/scores", ""))
	require.NotNil(t, s1.VNDB)
	require.NotNil(t, s1.VNDB.Rating)
	assert.InDelta(t, 78.7, *s1.VNDB.Rating, 0.001)
	assert.Equal(t, 23277, s1.VNDB.VoteCount)
	assert.Equal(t, "https://vndb.org/v97", s1.VNDB.URL)
	require.NotNil(t, s1.Bangumi)
	require.NotNil(t, s1.Bangumi.Score)
	assert.InDelta(t, 7.4, *s1.Bangumi.Score, 0.001)
	assert.Equal(t, 1234, s1.Bangumi.Rank)
	assert.Equal(t, 567, s1.Bangumi.Total)
	assert.Equal(t, "https://bgm.tv/subject/8801", s1.Bangumi.URL)
	require.NotNil(t, s1.EG)
	require.NotNil(t, s1.EG.Median)
	assert.Equal(t, 86, *s1.EG.Median)
	assert.Equal(t, 5246, s1.EG.VoteCount)
	assert.Equal(t, "https://erogamescape.dyndns.org/~ap2/ero/toukei_kaiseki/game.php?game=7835", s1.EG.URL)
	require.NotNil(t, s1.SyncedAt)
	assert.Equal(t, t2.Format(time.RFC3339), *s1.SyncedAt, "synced_at is the freshest across sources")

	// --- g2: only vndb, unrated ---
	s2 := decodeScores(t, doGet(t, app, "/api/galgame/2/scores", ""))
	require.NotNil(t, s2.VNDB)
	assert.Nil(t, s2.VNDB.Rating, "rating is null but the source object is present")
	assert.Equal(t, 5, s2.VNDB.VoteCount)
	assert.Equal(t, "https://vndb.org/v200", s2.VNDB.URL)
	assert.Nil(t, s2.Bangumi)
	assert.Nil(t, s2.EG)
	require.NotNil(t, s2.SyncedAt)

	// --- g3: zero sources → three nulls ---
	s3 := decodeScores(t, doGet(t, app, "/api/galgame/3/scores", ""))
	assert.Nil(t, s3.VNDB)
	assert.Nil(t, s3.Bangumi)
	assert.Nil(t, s3.EG)
	assert.Nil(t, s3.SyncedAt)
}

// TestScoresBangumiUnratedNull covers the Bangumi score=0 → null-score mapping
// (the object present with a null score, votes/rank passing through).
func TestScoresBangumiUnratedNull(t *testing.T) {
	app, db := newScoresTestApp(t)
	require.NoError(t, db.Create(&model.Galgame{ID: 1, VNDBID: "", UserID: 1}).Error)
	require.NoError(t, db.Create(&model.GalgameBangumiMeta{GalgameID: 1, BID: 999, Score: 0, Rank: 0, Total: 0, SyncedAt: time.Now()}).Error)

	s := decodeScores(t, doGet(t, app, "/api/galgame/1/scores", ""))
	require.NotNil(t, s.Bangumi)
	assert.Nil(t, s.Bangumi.Score, "score 0 (unrated) surfaces as null")
	assert.Equal(t, "https://bgm.tv/subject/999", s.Bangumi.URL)
}

func TestStatsEndpointETag(t *testing.T) {
	app, db := newScoresTestApp(t)

	tEarly := time.Date(2026, 7, 10, 5, 45, 0, 0, time.UTC)
	tLate := time.Date(2026, 7, 11, 5, 45, 0, 0, time.UTC) // max → ETag basis
	cov := datatypes.JSON([]byte(`{"total":42,"vndb_rows":40,"vndb_rated":30,"bangumi_rows":0,"bangumi_rated":0,"eg_rows":0,"eg_rated":0}`))
	require.NoError(t, db.Create(&model.GalgameStats{Key: "coverage", Payload: cov, BuiltAt: tLate}).Error)
	require.NoError(t, db.Create(&model.GalgameStats{Key: "release_years", Payload: datatypes.JSON([]byte(`[{"year":2020,"count":3}]`)), BuiltAt: tEarly}).Error)

	// First GET: 200 with ETag + verbatim payloads + overall built_at = max.
	resp := doGet(t, app, "/api/galgame/stats", "")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	etag := resp.Header.Get("ETag")
	assert.NotEmpty(t, etag)
	assert.NotEmpty(t, resp.Header.Get("Cache-Control"))

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var env struct {
		Data dto.GalgameStatsData `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &env))
	assert.JSONEq(t, string(cov), string(env.Data.Coverage.Payload), "coverage payload passed through verbatim")
	assert.Equal(t, tLate.Format(time.RFC3339), env.Data.Coverage.BuiltAt)
	assert.Equal(t, tEarly.Format(time.RFC3339), env.Data.ReleaseYears.BuiltAt)
	assert.Equal(t, tLate.Format(time.RFC3339), env.Data.BuiltAt, "overall built_at = max across keys")

	// Conditional GET with the matching ETag → 304, empty body.
	resp304 := doGet(t, app, "/api/galgame/stats", etag)
	assert.Equal(t, http.StatusNotModified, resp304.StatusCode)
	b304, _ := io.ReadAll(resp304.Body)
	assert.Empty(t, b304)

	// A stale/wrong If-None-Match still gets a fresh 200.
	resp200 := doGet(t, app, "/api/galgame/stats", `W/"gstats-000"`)
	assert.Equal(t, http.StatusOK, resp200.StatusCode)
}
