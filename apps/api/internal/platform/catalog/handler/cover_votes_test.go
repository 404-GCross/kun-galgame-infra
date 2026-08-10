package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/service"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type voteEnvelope struct {
	Data struct {
		CoverID   int64 `json:"cover_id"`
		VoteCount int64 `json:"vote_count"`
		Voted     bool  `json:"voted"`
	} `json:"data"`
}

func decodeVote(t *testing.T, raw []byte) voteEnvelope {
	t.Helper()
	var env voteEnvelope
	require.NoError(t, json.Unmarshal(raw, &env), string(raw))
	return env
}

type coverVoteFixture struct {
	work, other, merged     int64
	coverA, coverB, foreign int64
	mergedCover             int64
}

func seedCoverVoteFixture(t *testing.T, db *gorm.DB) coverVoteFixture {
	t.Helper()
	for _, tbl := range []string{"catalog_cover_vote", "catalog_work_cover", "catalog_work_title", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	newWork := func(name string, status int16) int64 {
		w := model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: name, Status: status}
		require.NoError(t, db.Create(&w).Error)
		return w.ID
	}
	newCover := func(workID int64, hash string, order int) int64 {
		c := model.CatalogWorkCover{WorkID: workID, ImageHash: hash, SortOrder: order, SourceID: 2}
		require.NoError(t, db.Create(&c).Error)
		return c.ID
	}
	f := coverVoteFixture{
		work:   newWork("投票対象", model.WorkStatusLive),
		other:  newWork("別作品", model.WorkStatusLive),
		merged: newWork("統合済み", model.WorkStatusMerged),
	}
	f.coverA = newCover(f.work, strings.Repeat("a", 64), 0)
	f.coverB = newCover(f.work, strings.Repeat("b", 64), 1)
	f.foreign = newCover(f.other, strings.Repeat("c", 64), 0)
	f.mergedCover = newCover(f.merged, strings.Repeat("d", 64), 0)
	return f
}

func castVote(t *testing.T, db *gorm.DB, f coverVoteFixture, coverID, uid int64) {
	t.Helper()
	require.NoError(t, db.Create(&model.CatalogCoverVote{
		WorkID: f.work, CoverID: coverID, ActorUID: uid, Site: "kungal",
	}).Error)
}

func TestCoverVote_S2SReadFaceTally(t *testing.T) {
	db := openCatalogTestDB(t)
	f := seedCoverVoteFixture(t, db)
	castVote(t, db, f, f.coverA, 5)
	castVote(t, db, f, f.coverA, 6)
	app := readApp(service.NewReadService(db), nil)

	readCovers := func(url string) []map[string]any {
		t.Helper()
		resp, err := app.Test(httptest.NewRequest("GET", url, nil))
		require.NoError(t, err)
		raw, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, fiber.StatusOK, resp.StatusCode, string(raw))
		var body struct {
			Data struct {
				Covers []map[string]any `json:"covers"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(raw, &body), string(raw))
		return body.Data.Covers
	}

	covers := readCovers(fmt.Sprintf("/api/v1/catalog/works/%d", f.work))
	require.Len(t, covers, 2)
	assert.EqualValues(t, f.coverA, covers[0]["id"], "the cover id is on the wire — it is what a vote addresses")
	assert.EqualValues(t, 2, covers[0]["vote_count"])
	assert.EqualValues(t, 0, covers[1]["vote_count"], "an unvoted cover is zero, not absent")
	for _, c := range covers {
		_, has := c["voted"]
		assert.False(t, has, "the S2S detail states no per-viewer flag")
	}

	withUID := readCovers(fmt.Sprintf("/api/v1/catalog/works/%d?uid=6", f.work))
	assert.Equal(t, covers, withUID)
	assert.Equal(t, covers, readCovers(fmt.Sprintf("/api/v1/catalog/works/%d?uid=0", f.work)))
}

func TestCoverVote_FKCascade(t *testing.T) {
	db := openCatalogTestDB(t)
	f := seedCoverVoteFixture(t, db)
	castVote(t, db, f, f.coverA, 5)
	castVote(t, db, f, f.coverB, 6)

	require.NoError(t, db.Exec(`DELETE FROM catalog_work_cover WHERE id = ?`, f.coverA).Error)

	var remaining []int64
	require.NoError(t, db.Raw(`SELECT cover_id FROM catalog_cover_vote`).Scan(&remaining).Error)
	require.Len(t, remaining, 1, "the deleted cover's vote is gone")
	assert.Equal(t, f.coverB, remaining[0])
}
