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
	siteModel "api/internal/platform/site/model"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// The best-cover vote face over HTTP (wave 175): the ballot moves rather than
// accumulates, the withdrawal is idempotent, and the tally rides back on the
// work detail's cover rows as decoration — never as an ordering.

// voteApp wires the vote face the way cmd/catalog does (same S2S API as the
// read surface, the client the path-scoped S2SAuth would have injected).
func voteApp(client *siteModel.OAuthClient, db *gorm.DB) *fiber.App {
	app := fiber.New()
	app.Use("/api/v1/catalog", func(c fiber.Ctx) error {
		if client != nil {
			c.Locals(localClient, client)
		}
		return c.Next()
	})
	api := Setup(app, nil, nil, service.NewReadService(db), nil, nil)
	SetupCoverVotes(api, service.NewCoverVoteService(db))
	return app
}

// voteReq issues a PUT/DELETE with the asserted-actor body and returns the raw
// bytes (the tally is read out of them).
func voteReq(t *testing.T, app *fiber.App, method, path, body string) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	require.NoError(t, err)
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, raw
}

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

// coverVoteFixture wipes the vote-relevant tables and builds: one live work
// with two covers, a second live work with one cover (the mismatch case), and a
// merged-away work with a cover (the unavailable case).
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

func votePath(workID, coverID int64) string {
	return fmt.Sprintf("/api/v1/catalog/works/%d/covers/%d/vote", workID, coverID)
}

func voteBody(uid int64) string {
	return fmt.Sprintf(`{"site":"kungal","actor":{"user_id":%d}}`, uid)
}

// TestCoverVote_MovesRatherThanAccumulates is the wave's central rule: one
// ballot per user per WORK, so a second vote relocates the first.
func TestCoverVote_MovesRatherThanAccumulates(t *testing.T) {
	db := openCatalogTestDB(t)
	f := seedCoverVoteFixture(t, db)
	app := voteApp(&siteModel.OAuthClient{ID: "kungal-client", CatalogSite: "kungal"}, db)

	status, raw := voteReq(t, app, "PUT", votePath(f.work, f.coverA), voteBody(5))
	require.Equal(t, fiber.StatusOK, status, string(raw))
	assert.EqualValues(t, 1, decodeVote(t, raw).Data.VoteCount)
	assert.True(t, decodeVote(t, raw).Data.Voted)

	// A second user piles onto the same cover: counts add across users.
	status, raw = voteReq(t, app, "PUT", votePath(f.work, f.coverA), voteBody(6))
	require.Equal(t, fiber.StatusOK, status, string(raw))
	assert.EqualValues(t, 2, decodeVote(t, raw).Data.VoteCount)

	// User 5 changes their mind: the ballot MOVES — B gains, A loses, and the
	// user still holds exactly one row on this work.
	status, raw = voteReq(t, app, "PUT", votePath(f.work, f.coverB), voteBody(5))
	require.Equal(t, fiber.StatusOK, status, string(raw))
	assert.EqualValues(t, 1, decodeVote(t, raw).Data.VoteCount)

	var counts []struct {
		CoverID int64 `gorm:"column:cover_id"`
		N       int64 `gorm:"column:n"`
	}
	require.NoError(t, db.Raw(
		`SELECT cover_id, count(*) AS n FROM catalog_cover_vote GROUP BY cover_id ORDER BY cover_id`,
	).Scan(&counts).Error)
	require.Len(t, counts, 2)
	assert.EqualValues(t, 1, counts[0].N, "cover A keeps only user 6")
	assert.EqualValues(t, 1, counts[1].N, "cover B holds the moved ballot")

	var ballots int64
	require.NoError(t, db.Raw(
		`SELECT count(*) FROM catalog_cover_vote WHERE work_id = ? AND actor_uid = 5`, f.work,
	).Scan(&ballots).Error)
	assert.EqualValues(t, 1, ballots, "the unique key holds: one row per (work, user)")

	// Voting does not touch the editorial columns — the whole advisory premise.
	var editorial []struct {
		SortOrder      int  `gorm:"column:sort_order"`
		PortraitPinned bool `gorm:"column:portrait_pinned"`
	}
	require.NoError(t, db.Raw(
		`SELECT sort_order, portrait_pinned FROM catalog_work_cover WHERE work_id = ? ORDER BY id`, f.work,
	).Scan(&editorial).Error)
	require.Len(t, editorial, 2)
	assert.Equal(t, 0, editorial[0].SortOrder, "votes never write sort_order")
	assert.Equal(t, 1, editorial[1].SortOrder, "votes never write sort_order")
	assert.False(t, editorial[0].PortraitPinned, "votes never write portrait_pinned")
	assert.False(t, editorial[1].PortraitPinned, "votes never write portrait_pinned")
}

// TestCoverUnvote_Idempotent: withdrawing a ballot nobody cast is a success,
// because the caller's intent is already satisfied.
func TestCoverUnvote_Idempotent(t *testing.T) {
	db := openCatalogTestDB(t)
	f := seedCoverVoteFixture(t, db)
	app := voteApp(&siteModel.OAuthClient{ID: "kungal-client", CatalogSite: "kungal"}, db)

	status, raw := voteReq(t, app, "PUT", votePath(f.work, f.coverA), voteBody(5))
	require.Equal(t, fiber.StatusOK, status, string(raw))

	status, raw = voteReq(t, app, "DELETE", votePath(f.work, f.coverA), voteBody(5))
	require.Equal(t, fiber.StatusOK, status, string(raw))
	env := decodeVote(t, raw)
	assert.EqualValues(t, 0, env.Data.VoteCount)
	assert.False(t, env.Data.Voted)

	// Again: same answer, no 404.
	status, raw = voteReq(t, app, "DELETE", votePath(f.work, f.coverA), voteBody(5))
	require.Equal(t, fiber.StatusOK, status, string(raw))
	assert.EqualValues(t, 0, decodeVote(t, raw).Data.VoteCount)

	var rows int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM catalog_cover_vote`).Scan(&rows).Error)
	assert.EqualValues(t, 0, rows)
}

// TestCoverVote_Refusals: the four ways a ballot is not accepted.
func TestCoverVote_Refusals(t *testing.T) {
	db := openCatalogTestDB(t)
	f := seedCoverVoteFixture(t, db)
	app := voteApp(&siteModel.OAuthClient{ID: "kungal-client", CatalogSite: "kungal"}, db)

	for _, tc := range []struct {
		name, path string
		want       int
	}{
		{"cover of another work", votePath(f.work, f.foreign), fiber.StatusNotFound},
		{"cover that does not exist", votePath(f.work, 9_000_001), fiber.StatusNotFound},
		{"work that does not exist", votePath(9_000_002, f.coverA), fiber.StatusNotFound},
		{"merged-away work", votePath(f.merged, f.mergedCover), fiber.StatusNotFound},
	} {
		status, raw := voteReq(t, app, "PUT", tc.path, voteBody(5))
		assert.Equalf(t, tc.want, status, "%s: %s", tc.name, string(raw))
	}

	// A soft-deleted work is a not-found too (the same refusal as never having
	// existed), and its covers are unreachable through it.
	require.NoError(t, db.Exec(`UPDATE catalog_work SET deleted_at = now() WHERE id = ?`, f.other).Error)
	status, raw := voteReq(t, app, "PUT", votePath(f.other, f.foreign), voteBody(5))
	assert.Equal(t, fiber.StatusNotFound, status, string(raw))
	require.NoError(t, db.Exec(`UPDATE catalog_work SET deleted_at = NULL WHERE id = ?`, f.other).Error)

	// A vote is never system-attributed: uid 0 (and any non-positive uid) is
	// refused. The asserted-actor schema catches it on the wire before the
	// service does — the service's own refusal is pinned in the service suite.
	status, raw = voteReq(t, app, "PUT", votePath(f.work, f.coverA), voteBody(0))
	assert.Equal(t, fiber.StatusUnprocessableEntity, status, string(raw))

	// Casting a ballot is a write, so the client's site binding is enforced.
	unbound := voteApp(&siteModel.OAuthClient{ID: "other-client", CatalogSite: "letmoe"}, db)
	status, raw = voteReq(t, unbound, "PUT", votePath(f.work, f.coverA), voteBody(5))
	assert.Equal(t, fiber.StatusForbidden, status, string(raw))

	var rows int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM catalog_cover_vote`).Scan(&rows).Error)
	assert.EqualValues(t, 0, rows, "no refusal left a row behind")
}

// TestCoverVote_ReadFaceTally: the counts ride back on the work detail's cover
// rows, and `voted` answers only when the caller identifies a viewer.
func TestCoverVote_ReadFaceTally(t *testing.T) {
	db := openCatalogTestDB(t)
	f := seedCoverVoteFixture(t, db)
	app := voteApp(&siteModel.OAuthClient{ID: "kungal-client", CatalogSite: "kungal"}, db)

	for _, uid := range []int64{5, 6} {
		status, raw := voteReq(t, app, "PUT", votePath(f.work, f.coverA), voteBody(uid))
		require.Equal(t, fiber.StatusOK, status, string(raw))
	}

	type coverRow struct {
		ID        int64 `json:"id"`
		VoteCount int   `json:"vote_count"`
		Voted     bool  `json:"voted"`
	}
	readCovers := func(url string) []coverRow {
		resp, err := app.Test(httptest.NewRequest("GET", url, nil))
		require.NoError(t, err)
		raw, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, fiber.StatusOK, resp.StatusCode, string(raw))
		var body struct {
			Data struct {
				Covers []coverRow `json:"covers"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(raw, &body), string(raw))
		return body.Data.Covers
	}

	anon := readCovers(fmt.Sprintf("/api/v1/catalog/works/%d", f.work))
	require.Len(t, anon, 2)
	assert.Equal(t, f.coverA, anon[0].ID, "the cover id is on the wire — it is what a vote addresses")
	assert.Equal(t, 2, anon[0].VoteCount)
	assert.Equal(t, 0, anon[1].VoteCount, "an unvoted cover is zero, not absent")
	for _, c := range anon {
		assert.False(t, c.Voted, "no uid asked, so `voted` is omitted")
	}

	mine := readCovers(fmt.Sprintf("/api/v1/catalog/works/%d?uid=6", f.work))
	require.Len(t, mine, 2)
	assert.True(t, mine[0].Voted, "user 6's ballot is on cover A")
	assert.False(t, mine[1].Voted)

	stranger := readCovers(fmt.Sprintf("/api/v1/catalog/works/%d?uid=999", f.work))
	assert.Equal(t, 2, stranger[0].VoteCount, "the count is everybody's")
	assert.False(t, stranger[0].Voted, "the flag is only the asker's")
}

// TestCoverVote_FKCascade: deleting a cover takes its votes with it, with zero
// Go code involved — the referential rule is the schema's.
func TestCoverVote_FKCascade(t *testing.T) {
	db := openCatalogTestDB(t)
	f := seedCoverVoteFixture(t, db)
	app := voteApp(&siteModel.OAuthClient{ID: "kungal-client", CatalogSite: "kungal"}, db)

	status, raw := voteReq(t, app, "PUT", votePath(f.work, f.coverA), voteBody(5))
	require.Equal(t, fiber.StatusOK, status, string(raw))
	status, raw = voteReq(t, app, "PUT", votePath(f.work, f.coverB), voteBody(6))
	require.Equal(t, fiber.StatusOK, status, string(raw))

	require.NoError(t, db.Exec(`DELETE FROM catalog_work_cover WHERE id = ?`, f.coverA).Error)

	var remaining []int64
	require.NoError(t, db.Raw(`SELECT cover_id FROM catalog_cover_vote`).Scan(&remaining).Error)
	require.Len(t, remaining, 1, "the deleted cover's vote is gone")
	assert.Equal(t, f.coverB, remaining[0])
}
