package handler

import (
	"context"
	"fmt"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	"api/internal/middleware"
	"api/internal/platform/catalog/service"
	siteModel "api/internal/platform/site/model"
	"api/pkg/oidctoken"
	"api/pkg/utils"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// The user-token write plane over HTTP (wave 176). The subject of every test
// here is the same sentence: the actor and the tenant come from the verified
// token, and there is no body to say otherwise.

const userTestSecret = "catalog-user-face-test-secret"

// fakeClientLookup stands in for the OAuth client registry (a table in the OTHER
// database this service holds a pool onto). The face only ever asks it one
// question, so the interface — and the fake — is one method wide.
type fakeClientLookup map[string]*siteModel.OAuthClient

func (f fakeClientLookup) FindByClientID(_ context.Context, clientID string) (*siteModel.OAuthClient, error) {
	c, ok := f[clientID]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	return c, nil
}

// userVoteApp wires the face exactly as cmd/catalog does: the shared JWT
// middleware and UserGate on the prefix, then the Huma ops on the app.
func userVoteApp(db *gorm.DB, clients fakeClientLookup) *fiber.App {
	app := fiber.New()
	verifier := oidctoken.NewVerifier(userTestSecret, nil) // HS256-only (no JWKS)
	app.Use(UserPrefix, middleware.JWTAuth(verifier), UserGate(clients))
	SetupUser(app, service.NewCoverVoteService(db), nil, nil, nil, service.NewReadService(db))
	return app
}

// userToken mints the access token the OP would have issued, for the ordinary
// logged-in user.
func userToken(t *testing.T, uid uint, scope, clientID string) string {
	t.Helper()
	return userTokenRoles(t, uid, scope, clientID, "user")
}

// userTokenRoles is userToken with an explicit role set — the editing face
// (wave 177) resolves permissions from exactly these claims.
func userTokenRoles(t *testing.T, uid uint, scope, clientID string, roles ...string) string {
	t.Helper()
	tok, err := utils.GenerateAccessToken(userTestSecret, utils.TokenClaims{
		ID: uid, Scope: scope, ClientID: clientID, Roles: roles,
	}, time.Hour)
	require.NoError(t, err)
	return tok
}

func userVotePath(workID, coverID int64) string {
	return fmt.Sprintf("%s/works/%d/covers/%d/vote", UserPrefix, workID, coverID)
}

// userVoteReq issues a bodiless request with (or without) a bearer token.
func userVoteReq(t *testing.T, app *fiber.App, method, path, token string) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := app.Test(req)
	require.NoError(t, err)
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, raw
}

// TestUserCoverVote_Refusals walks the auth chain from the outside in. Each row
// is a different way of not being a client-bound catalog user, and none of them
// may leave a ballot behind.
func TestUserCoverVote_Refusals(t *testing.T) {
	db := openCatalogTestDB(t)
	f := seedCoverVoteFixture(t, db)
	clients := fakeClientLookup{
		"kungal-client": {ID: "kungal-client", CatalogSite: "kungal"},
		"no-site":       {ID: "no-site"},
	}
	app := userVoteApp(db, clients)
	path := userVotePath(f.work, f.coverA)

	for _, tc := range []struct {
		name  string
		token string
		want  int
	}{
		{"no token at all", "", fiber.StatusUnauthorized},
		{"token without the catalog:edit scope", userToken(t, 5, "openid profile", "kungal-client"), fiber.StatusForbidden},
		{"scope matched as a word, not a substring", userToken(t, 5, "catalog:editor", "kungal-client"), fiber.StatusForbidden},
		{"first-party token: no client binding", userToken(t, 5, ScopeCatalogEdit, ""), fiber.StatusForbidden},
		{"client that is not registered", userToken(t, 5, ScopeCatalogEdit, "ghost-client"), fiber.StatusForbidden},
		{"client bound to no catalog site", userToken(t, 5, ScopeCatalogEdit, "no-site"), fiber.StatusForbidden},
		{"token that names no user", userToken(t, 0, ScopeCatalogEdit, "kungal-client"), fiber.StatusUnauthorized},
	} {
		status, raw := userVoteReq(t, app, "PUT", path, tc.token)
		assert.Equalf(t, tc.want, status, "%s: %s", tc.name, string(raw))
	}

	// A garbage bearer token is the token's failure, not the user's.
	status, raw := userVoteReq(t, app, "PUT", path, "not-a-jwt")
	assert.Equal(t, fiber.StatusUnauthorized, status, string(raw))

	var rows int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM catalog_cover_vote`).Scan(&rows).Error)
	assert.EqualValues(t, 0, rows, "no refusal left a row behind")
}

// TestUserCoverVote_ActorAndSiteComeFromTheToken is the wave's whole doctrine
// pinned on the row that lands in the table: the uid is the token's `id` and
// the site is the token's client's binding — neither was on the wire.
func TestUserCoverVote_ActorAndSiteComeFromTheToken(t *testing.T) {
	db := openCatalogTestDB(t)
	f := seedCoverVoteFixture(t, db)
	app := userVoteApp(db, fakeClientLookup{
		"kungal-client": {ID: "kungal-client", CatalogSite: "kungal"},
		"letmoe-client": {ID: "letmoe-client", CatalogSite: "letmoe"},
	})

	status, raw := userVoteReq(t, app, "PUT", userVotePath(f.work, f.coverA),
		userToken(t, 77, ScopeCatalogEdit, "kungal-client"))
	require.Equal(t, fiber.StatusOK, status, string(raw))
	env := decodeVote(t, raw)
	assert.EqualValues(t, f.coverA, env.Data.CoverID)
	assert.EqualValues(t, 1, env.Data.VoteCount)
	assert.True(t, env.Data.Voted)

	var row struct {
		ActorUID int64  `gorm:"column:actor_uid"`
		Site     string `gorm:"column:site"`
		CoverID  int64  `gorm:"column:cover_id"`
	}
	require.NoError(t, db.Raw(
		`SELECT actor_uid, site, cover_id FROM catalog_cover_vote WHERE work_id = ?`, f.work,
	).Scan(&row).Error)
	assert.EqualValues(t, 77, row.ActorUID, "the actor is the token's id claim")
	assert.Equal(t, "kungal", row.Site, "the site is the token's client's catalog_site")
	assert.Equal(t, f.coverA, row.CoverID)

	// A second user, arriving through a different client, is a different tenant
	// on the same work — the counts add and the sites differ.
	status, raw = userVoteReq(t, app, "PUT", userVotePath(f.work, f.coverA),
		userToken(t, 78, ScopeCatalogEdit, "letmoe-client"))
	require.Equal(t, fiber.StatusOK, status, string(raw))
	assert.EqualValues(t, 2, decodeVote(t, raw).Data.VoteCount)

	var sites []string
	require.NoError(t, db.Raw(
		`SELECT site FROM catalog_cover_vote ORDER BY actor_uid`).Scan(&sites).Error)
	assert.Equal(t, []string{"kungal", "letmoe"}, sites)
}

// TestUserCoverVote_MovesAndWithdraws: the ballot rules are the table's, so the
// user plane inherits them unchanged — one row per (work, user), moved by a
// re-vote, and an idempotent withdrawal.
func TestUserCoverVote_MovesAndWithdraws(t *testing.T) {
	db := openCatalogTestDB(t)
	f := seedCoverVoteFixture(t, db)
	app := userVoteApp(db, fakeClientLookup{"kungal-client": {ID: "kungal-client", CatalogSite: "kungal"}})
	tok := userToken(t, 9, ScopeCatalogEdit, "kungal-client")

	status, raw := userVoteReq(t, app, "PUT", userVotePath(f.work, f.coverA), tok)
	require.Equal(t, fiber.StatusOK, status, string(raw))

	status, raw = userVoteReq(t, app, "PUT", userVotePath(f.work, f.coverB), tok)
	require.Equal(t, fiber.StatusOK, status, string(raw))
	assert.EqualValues(t, 1, decodeVote(t, raw).Data.VoteCount)

	var moved []int64
	require.NoError(t, db.Raw(
		`SELECT cover_id FROM catalog_cover_vote WHERE work_id = ? AND actor_uid = 9`, f.work,
	).Scan(&moved).Error)
	require.Len(t, moved, 1, "the ballot moved rather than accumulated")
	assert.Equal(t, f.coverB, moved[0])

	// Withdrawal names a cover for symmetry only: the ballot is per work, so
	// pointing at the OTHER cover still takes the vote back.
	status, raw = userVoteReq(t, app, "DELETE", userVotePath(f.work, f.coverA), tok)
	require.Equal(t, fiber.StatusOK, status, string(raw))
	assert.EqualValues(t, 0, decodeVote(t, raw).Data.VoteCount)
	assert.False(t, decodeVote(t, raw).Data.Voted)

	// Again: still a 200, still nothing there.
	status, raw = userVoteReq(t, app, "DELETE", userVotePath(f.work, f.coverA), tok)
	require.Equal(t, fiber.StatusOK, status, string(raw))

	var rows int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM catalog_cover_vote`).Scan(&rows).Error)
	assert.EqualValues(t, 0, rows)
}

// TestUserCoverVote_NotFound: the subject refusals are the S2S face's, because
// they are the service's.
func TestUserCoverVote_NotFound(t *testing.T) {
	db := openCatalogTestDB(t)
	f := seedCoverVoteFixture(t, db)
	app := userVoteApp(db, fakeClientLookup{"kungal-client": {ID: "kungal-client", CatalogSite: "kungal"}})
	tok := userToken(t, 5, ScopeCatalogEdit, "kungal-client")

	for _, tc := range []struct {
		name string
		path string
	}{
		{"cover of another work", userVotePath(f.work, f.foreign)},
		{"cover that does not exist", userVotePath(f.work, 9_000_001)},
		{"work that does not exist", userVotePath(9_000_002, f.coverA)},
		{"merged-away work", userVotePath(f.merged, f.mergedCover)},
	} {
		status, raw := userVoteReq(t, app, "PUT", tc.path, tok)
		assert.Equalf(t, fiber.StatusNotFound, status, "%s: %s", tc.name, string(raw))
	}
}
