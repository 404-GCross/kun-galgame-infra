package handler

import (
	"context"
	"fmt"
	"testing"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/catalog/model"
	"api/internal/platform/editing"
	siteModel "api/internal/platform/site/model"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserEditActor_ThirdPartyClientNeverReachesTrustedTier(t *testing.T) {
	owner := uint(4040)
	firstParty := &siteModel.OAuthClient{ID: "letmoe-client", CatalogSite: "letmoe"}
	thirdParty := &siteModel.OAuthClient{ID: "app", CatalogSite: "letmoe", OwnerUserID: &owner}

	actorThrough := func(client *siteModel.OAuthClient, roles ...string) dto.EditActor {
		ctx := context.WithValue(context.Background(), ctxKeyUserID, int64(4242))
		ctx = context.WithValue(ctx, ctxKeyUserRoles, roles)
		ctx = context.WithValue(ctx, ctxKeyClient, client)
		actor, site, he := userEditActor(ctx)
		require.Nil(t, he)
		require.Equal(t, "letmoe", site, "the tenant is unaffected by the cap")
		require.EqualValues(t, 4242, actor.UserID, "the person is unaffected by the cap")
		return actor
	}

	for _, role := range []string{"admin", "ren"} {
		assert.Equal(t, editing.TrustedTier, actorThrough(firstParty, "user", role).TrustTier,
			"%s keeps its standing on the product's own client", role)
		assert.EqualValues(t, 0, actorThrough(thirdParty, "user", role).TrustTier,
			"%s may not LEND its standing to a third-party application", role)
		assert.Equal(t, []string{"user", role}, actorThrough(thirdParty, "user", role).Roles)
	}
}

func TestUserEdit_ThirdPartyClientFilesAtTierZero(t *testing.T) {
	db := openCatalogTestDB(t)
	work := seedUserEditWork(t, db)
	app := userEditApp(t, db, userEditClients())

	status, raw := userEditReq(t, app, "POST", UserPrefix+"/edit/proposals",
		userTokenRoles(t, 860, ScopeCatalogEdit, "thirdparty-letmoe", "user", "admin"),
		userProposalBody(work, "第三者アプリからの提案", "third-party"))
	assert.Equal(t, fiber.StatusForbidden, status,
		"a third-party token writes at tier 0, so letmoe's trusted lane refuses it: %s", raw)

	var rows int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM edit_proposal WHERE site = 'letmoe'`).Scan(&rows).Error)
	assert.EqualValues(t, 0, rows, "nothing was filed")

	status, raw = userEditReq(t, app, "POST", UserPrefix+"/edit/proposals",
		userTokenRoles(t, 860, ScopeCatalogEdit, "letmoe-client", "user", "admin"),
		userProposalBody(work, "自社クライアントからの提案", "first-party"))
	require.Equal(t, fiber.StatusOK, status, string(raw))

	require.NoError(t, db.Raw(`SELECT count(*) FROM edit_proposal WHERE site = 'letmoe'`).Scan(&rows).Error)
	assert.EqualValues(t, 1, rows, "only the first-party filing landed")
}

func TestUserEdit_ThirdPartyClientStillProposesOnAnOpenTenant(t *testing.T) {
	db := openCatalogTestDB(t)
	work := seedUserEditWork(t, db)
	app := userEditApp(t, db, userEditClients())

	status, raw := userEditReq(t, app, "POST", UserPrefix+"/edit/proposals",
		userToken(t, 861, ScopeCatalogEdit, "thirdparty-kungal"),
		userProposalBody(work, "第三者アプリの通常提案", "open lane"))
	require.Equal(t, fiber.StatusOK, status, string(raw))

	env := decodeUserCreate(t, raw)
	assert.Equal(t, "kungal", env.Data.Proposal.Site)
	assert.EqualValues(t, 861, env.Data.Proposal.ProposerUID)
	assert.False(t, env.Data.Merged, "an untrusted third-party filing queues; it never automerges")

	status, raw = userEditReq(t, app, "POST", UserPrefix+"/edit/proposals",
		userTokenRoles(t, 862, ScopeCatalogEdit, "thirdparty-kungal", "user", "admin"),
		userProposalBody(work, "第三者アプリの審査者提案", "review key"))
	require.Equal(t, fiber.StatusOK, status, string(raw))
	env = decodeUserCreate(t, raw)
	assert.False(t, env.Data.Merged,
		"a review key does not automerge from a third-party application, open tenant or not")
	assert.Equal(t, "open", env.Data.Proposal.Status)
	queued := env.Data.Proposal.ID

	for _, path := range []string{"/merge", "/decline"} {
		status, raw = userEditReq(t, app, "POST",
			fmt.Sprintf("%s/edit/proposals/%d%s", UserPrefix, queued, path),
			userTokenRoles(t, 862, ScopeCatalogEdit, "thirdparty-kungal", "user", "admin"), `{"note":"x"}`)
		assert.Equalf(t, fiber.StatusForbidden, status,
			"%s through a third-party application must be refused: %s", path, raw)
	}
	status, raw = userEditReq(t, app, "POST",
		fmt.Sprintf("%s/edit/proposals/%d/amendments", UserPrefix, queued),
		userTokenRoles(t, 862, ScopeCatalogEdit, "thirdparty-kungal", "user", "admin"),
		`{"set":{"catalog.work.display_name":"改竄"}}`)
	assert.Equal(t, fiber.StatusForbidden, status, string(raw))

	status, raw = userEditReq(t, app, "POST",
		fmt.Sprintf("%s/edit/proposals/%d/withdraw", UserPrefix, queued),
		userTokenRoles(t, 862, ScopeCatalogEdit, "thirdparty-kungal", "user", "admin"), "")
	require.Equal(t, fiber.StatusOK, status, string(raw))

	status, raw = userEditReq(t, app, "POST", UserPrefix+"/edit/proposals",
		userTokenRoles(t, 862, ScopeCatalogEdit, "kungal-client", "user", "admin"),
		userProposalBody(work, "自社クライアントの審査者編集", "first-party review key"))
	require.Equal(t, fiber.StatusOK, status, string(raw))
	env = decodeUserCreate(t, raw)
	assert.True(t, env.Data.Merged, "the first-party reviewer still direct-edits")
}

func TestUserClaims_ThirdPartyClientIsNotAModerationSurface(t *testing.T) {
	db := openCatalogTestDB(t)
	resetClaims(t, db)
	app := userClaimApp(db)
	work := seedClaimedWork(t, db, model.ClaimStatePending, 862)

	for _, action := range []string{"approve", "decline", "ban", "unban"} {
		status, raw := userEditReq(t, app, "POST", userClaimActionPath(work, action),
			userTokenRoles(t, 900, ScopeCatalogEdit, "thirdparty-kungal", "user", "moderator"),
			`{"reason":"x"}`)
		assert.Equalf(t, fiber.StatusForbidden, status,
			"%s through a third-party application must be refused: %s", action, raw)
	}
	assert.Equal(t, model.ClaimStateKeyPending, claimStateOf(t, db, work), "no refusal decided anything")

	status, raw := userEditReq(t, app, "POST", userClaimActionPath(work, "approve"),
		moderatorToken(t, 900), `{}`)
	require.Equal(t, fiber.StatusOK, status, string(raw))
	assert.Equal(t, model.ClaimStateKeyLive, claimStateOf(t, db, work))
}
