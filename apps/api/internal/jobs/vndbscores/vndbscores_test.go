package vndbscores

import (
	"fmt"
	"os"
	"testing"
	"time"

	"api/internal/platform/galgame/model"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestUpsertIdempotent verifies the ON CONFLICT (galgame_id) upsert: writing the
// same galgame twice UPDATEs in place (no row growth), and a zero-vote write
// persists a NULL rating rather than a fake zero. Skipped without a test DB.
func TestUpsertIdempotent(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.GalgameSeries{}, &model.Galgame{}, &model.GalgameVNDBMeta{}))

	// A unique vndb_id (pid-derived) avoids colliding with rows other packages
	// may have left in the shared test DB.
	vid := fmt.Sprintf("v%d", 900000+os.Getpid()%90000)
	g := model.Galgame{VNDBID: vid, UserID: 1}
	require.NoError(t, db.Create(&g).Error)
	// Scope assertions to this galgame_id so leftover rows don't interfere.
	countRows := func() int64 {
		var n int64
		require.NoError(t, db.Model(&model.GalgameVNDBMeta{}).Where("galgame_id = ?", g.ID).Count(&n).Error)
		return n
	}

	r1 := 70.0
	require.NoError(t, upsert(db, model.GalgameVNDBMeta{
		GalgameID: g.ID, VNDBID: vid, Rating: &r1, VoteCount: 10, SyncedAt: time.Now(),
	}))
	require.Equal(t, int64(1), countRows(), "first write inserts one row")

	// Second write, same galgame_id, updated values → UPDATE, not a new row.
	r2 := 85.5
	require.NoError(t, upsert(db, model.GalgameVNDBMeta{
		GalgameID: g.ID, VNDBID: vid, Rating: &r2, VoteCount: 20, SyncedAt: time.Now(),
	}))
	require.Equal(t, int64(1), countRows(), "second upsert must UPDATE, not add a row")

	var got model.GalgameVNDBMeta
	require.NoError(t, db.First(&got, "galgame_id = ?", g.ID).Error)
	require.NotNil(t, got.Rating)
	require.Equal(t, 85.5, *got.Rating)
	require.Equal(t, 20, got.VoteCount)

	// A subsequent zero-vote write persists NULL rating (never a fake zero).
	require.NoError(t, upsert(db, model.GalgameVNDBMeta{
		GalgameID: g.ID, VNDBID: vid, Rating: nil, VoteCount: 0, SyncedAt: time.Now(),
	}))
	require.Equal(t, int64(1), countRows(), "zero-vote write still updates in place")
	var got2 model.GalgameVNDBMeta
	require.NoError(t, db.First(&got2, "galgame_id = ?", g.ID).Error)
	require.Nil(t, got2.Rating, "zero-vote must persist NULL rating")
	require.Equal(t, 0, got2.VoteCount)
}
