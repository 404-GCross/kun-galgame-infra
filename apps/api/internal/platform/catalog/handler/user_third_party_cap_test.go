// user_third_party_cap_test.go — the third-party posture cap (wave 186b).
//
// One sentence under test, twice: trust and moderation are properties of the
// PAIR (person x first-party client), not of the person alone. The same staff
// member, the same roles, the same tenant — only the application the token was
// issued through differs, and that is enough.
package handler

import (
	"context"
	"testing"

	"api/internal/platform/catalog/dto"
	"api/internal/platform/catalog/model"
	"api/internal/platform/editing"
	siteModel "api/internal/platform/site/model"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUserEditActor_ThirdPartyClientNeverReachesTrustedTier is the cap as pure
// logic, beside the wave-183 derivation it caps: the roles resolve to
// catalog.edit.trusted either way, and only the client tells the two apart.
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
		// The roles themselves are untouched: the cap removes a tier, not an
		// identity, so every permission the engine resolves from roles behaves
		// exactly as before.
		assert.Equal(t, []string{"user", role}, actorThrough(thirdParty, "user", role).Roles)
	}
}

// TestUserEdit_ThirdPartyClientFilesAtTierZero is the same cap end to end, on
// the overlay that reads the tier: letmoe's work policy is propose=trusted, so
// a capped token is refused where the very same person, on letmoe's own client,
// files successfully.
func TestUserEdit_ThirdPartyClientFilesAtTierZero(t *testing.T) {
	db := openCatalogTestDB(t)
	work := seedUserEditWork(t, db)
	app := userEditApp(t, db, userEditClients())

	// Same uid, same roles, same tenant — through the third-party application.
	status, raw := userEditReq(t, app, "POST", UserPrefix+"/edit/proposals",
		userTokenRoles(t, 860, ScopeCatalogEdit, "thirdparty-letmoe", "user", "admin"),
		userProposalBody(work, "第三者アプリからの提案", "third-party"))
	assert.Equal(t, fiber.StatusForbidden, status,
		"a third-party token writes at tier 0, so letmoe's trusted lane refuses it: %s", raw)

	var rows int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM edit_proposal WHERE site = 'letmoe'`).Scan(&rows).Error)
	assert.EqualValues(t, 0, rows, "nothing was filed")

	// The identical person on letmoe's OWN client still files: the cap removed a
	// lending path, not the person's standing.
	status, raw = userEditReq(t, app, "POST", UserPrefix+"/edit/proposals",
		userTokenRoles(t, 860, ScopeCatalogEdit, "letmoe-client", "user", "admin"),
		userProposalBody(work, "自社クライアントからの提案", "first-party"))
	require.Equal(t, fiber.StatusOK, status, string(raw))

	require.NoError(t, db.Raw(`SELECT count(*) FROM edit_proposal WHERE site = 'letmoe'`).Scan(&rows).Error)
	assert.EqualValues(t, 1, rows, "only the first-party filing landed")
}

// TestUserEdit_ThirdPartyClientStillProposesOnAnOpenTenant: the cap is a cap on
// TRUST, not a ban on editing. kungal's overlay is propose=open, so an ordinary
// member filing through a third-party application still files — and the filing
// waits in the queue, which is the posture the docs promise (proposals only).
//
// ⚠️ The role here is deliberately an ordinary `user`. A token whose roles carry
// edit.catalog.work.review AUTOMERGES on this tenant, and that path runs through
// the engine's own permission resolution (policyCtx → HasPerm over actor.Roles),
// which wave 186b does NOT cap — the cap lands on the trust tier only. Capping
// the engine's review keys per client is a separate ruling, not something to
// smuggle in behind a test fixture.
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
}

// TestUserClaims_ThirdPartyClientIsNotAModerationSurface: the second half of the
// same doctrine. Judging other people's submissions is the pair's authority too,
// so the moderator's own roles do not travel into an arbitrary application.
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

	// The same moderator, through the product's own client, still decides.
	status, raw := userEditReq(t, app, "POST", userClaimActionPath(work, "approve"),
		moderatorToken(t, 900), `{}`)
	require.Equal(t, fiber.StatusOK, status, string(raw))
	assert.Equal(t, model.ClaimStateKeyLive, claimStateOf(t, db, work))
}
