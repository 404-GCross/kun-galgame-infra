package handler

import (
	"encoding/json"
	"fmt"
	"testing"

	"api/internal/middleware"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/service"
	"api/pkg/oidctoken"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// The claims face on the user-token plane (wave 179). Every test here asks the
// same question the two waves before it asked, in the place claims live: did
// the submitter, the tenant and the reviewer come from the token — and, new to
// this wave, is the caller really the entry's owner? The lifecycle itself (what
// transitions are legal, what an event row looks like) is the service suite's
// subject and is not re-litigated.

// userClaimApp wires the face as cmd/catalog does: JWTAuth + UserGate on the
// prefix, then the user Huma API over the SAME lifecycle service the S2S face
// drives.
func userClaimApp(db *gorm.DB) *fiber.App {
	app := fiber.New()
	verifier := oidctoken.NewVerifier(userTestSecret, nil) // HS256-only (no JWKS)
	app.Use(UserPrefix, middleware.JWTAuth(verifier), UserGate(userEditClients()))
	SetupUser(app, service.NewCoverVoteService(db), nil, nil, service.NewClaimLifecycleService(db),
		service.NewReadService(db))
	return app
}

// resetClaims empties everything a claims test writes, so each starts from the
// same empty registry.
func resetClaims(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, tbl := range []string{
		"catalog_claim_event", "catalog_release", "catalog_work_title", "catalog_work",
	} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
}

// seedClaimedWork puts one work on the kungal tenant in a given claim state,
// owned by ownerUID (0 = the unowned row a pre-wave-178 claim left behind).
func seedClaimedWork(t *testing.T, db *gorm.DB, state int16, ownerUID int64) int64 {
	t.Helper()
	site := "kungal"
	w := &model.CatalogWork{
		MediumID: 1, OLang: "ja", DisplayName: "権利テスト",
		Status: model.WorkStatusLive, Site: &site, ClaimState: &state,
	}
	require.NoError(t, db.Create(w).Error)
	require.NoError(t, db.Exec(
		`UPDATE catalog_work SET product_work_id = ? WHERE id = ?`, w.ID, w.ID).Error)
	if ownerUID > 0 {
		require.NoError(t, db.Exec(
			`UPDATE catalog_work SET owner_user_id = ? WHERE id = ?`, ownerUID, w.ID).Error)
	}
	return w.ID
}

func userClaimActionPath(workID int64, action string) string {
	return fmt.Sprintf("%s/works/%d/claim-actions/%s", UserPrefix, workID, action)
}

// moderatorToken carries the role the catalog's own bundle grants
// catalog.claim.review — the review half's authority, arriving as a claim.
func moderatorToken(t *testing.T, uid uint) string {
	t.Helper()
	return userTokenRoles(t, uid, ScopeCatalogEdit, "kungal-client", "user", "moderator")
}

func claimStateOf(t *testing.T, db *gorm.DB, workID int64) string {
	t.Helper()
	var row struct {
		Site          *string
		ProductWorkID *int64
		ClaimState    *int16
	}
	require.NoError(t, db.Raw(
		`SELECT site, product_work_id, claim_state FROM catalog_work WHERE id = ?`, workID,
	).Scan(&row).Error)
	return model.ClaimStateKey(row.Site, row.ProductWorkID, row.ClaimState)
}

// TestUserClaims_BehindTheSameGate: the three new routes are not a fourth door.
// The eight-row refusal matrix is the gate's own test; all that needs saying
// here is that these paths sit behind the same gate, and that nothing lands.
func TestUserClaims_BehindTheSameGate(t *testing.T) {
	db := openCatalogTestDB(t)
	resetClaims(t, db)
	app := userClaimApp(db)
	work := seedClaimedWork(t, db, model.ClaimStateDraft, 801)

	for _, tc := range []struct {
		name, method, path, body, token string
		want                            int
	}{
		{"submit without a token", "POST", UserPrefix + "/works/submit",
			`{"fields":{"catalog.work.display_name":"無認証"}}`, "", fiber.StatusUnauthorized},
		{"act without a token", "POST", userClaimActionPath(work, "submit"), `{}`, "",
			fiber.StatusUnauthorized},
		{"mine without a token", "GET", UserPrefix + "/claims/mine", "", "",
			fiber.StatusUnauthorized},
		{"submit with the wrong scope", "POST", UserPrefix + "/works/submit",
			`{"fields":{"catalog.work.display_name":"無権限"}}`,
			userToken(t, 5, "openid profile", "kungal-client"), fiber.StatusForbidden},
		{"act with the wrong scope", "POST", userClaimActionPath(work, "submit"), `{}`,
			userToken(t, 5, "openid profile", "kungal-client"), fiber.StatusForbidden},
		{"mine with the wrong scope", "GET", UserPrefix + "/claims/mine", "",
			userToken(t, 5, "openid profile", "kungal-client"), fiber.StatusForbidden},
	} {
		status, raw := userEditReq(t, app, tc.method, tc.path, tc.token, tc.body)
		assert.Equalf(t, tc.want, status, "%s: %s", tc.name, raw)
	}

	var rows int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM catalog_claim_event`).Scan(&rows).Error)
	assert.EqualValues(t, 0, rows, "no refusal wrote an event")
	assert.Equal(t, model.ClaimStateKeyDraft, claimStateOf(t, db, work), "and none moved the claim")
}

// TestUserClaims_SubmitDerivesActorAndSite is the wave's doctrine on the row
// the mint leaves: the tenant is the token client's catalog_site and the owner
// is the token's `id` — and the S2S body's identity fields do not exist here,
// so a request carrying them cannot succeed.
func TestUserClaims_SubmitDerivesActorAndSite(t *testing.T) {
	db := openCatalogTestDB(t)
	resetClaims(t, db)
	app := userClaimApp(db)

	status, raw := userEditReq(t, app, "POST", UserPrefix+"/works/submit",
		userToken(t, 811, ScopeCatalogEdit, "kungal-client"),
		`{"fields":{"catalog.work.display_name":"利用者投稿"}}`)
	require.Equal(t, fiber.StatusOK, status, string(raw))

	var env struct {
		Data struct {
			WorkID        int64  `json:"work_id"`
			ProductWorkID int64  `json:"product_work_id"`
			ClaimState    string `json:"claim_state"`
			EventID       int64  `json:"event_id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &env), string(raw))
	assert.Equal(t, model.ClaimStateKeyPending, env.Data.ClaimState)
	assert.Equal(t, env.Data.WorkID, env.Data.ProductWorkID,
		"an omitted product_work_id makes the registry issue the identity")
	require.NotZero(t, env.Data.EventID)

	var row struct {
		Site        string `gorm:"column:site"`
		OwnerUserID *int64 `gorm:"column:owner_user_id"`
		DisplayName string `gorm:"column:display_name"`
	}
	require.NoError(t, db.Raw(
		`SELECT site, owner_user_id, display_name FROM catalog_work WHERE id = ?`, env.Data.WorkID,
	).Scan(&row).Error)
	assert.Equal(t, "kungal", row.Site, "the tenant is the token client's catalog_site")
	require.NotNil(t, row.OwnerUserID)
	assert.EqualValues(t, 811, *row.OwnerUserID, "the owner is the token's id claim")
	assert.Equal(t, "利用者投稿", row.DisplayName)

	// The birth event carries the same two values, from the same source.
	var ev struct {
		ActorUID int64  `gorm:"column:actor_uid"`
		Site     string `gorm:"column:site"`
	}
	require.NoError(t, db.Raw(
		`SELECT actor_uid, site FROM catalog_claim_event WHERE id = ?`, env.Data.EventID,
	).Scan(&ev).Error)
	assert.EqualValues(t, 811, ev.ActorUID)
	assert.Equal(t, "kungal", ev.Site)

	// Spoofing: `site` and `actor` are not fields of this body, so a request
	// naming them is rejected outright rather than obeyed. What matters is only
	// that it never mints — the status may be 422 (the schema knows no such
	// field), never 200.
	before := env.Data.WorkID
	for _, body := range []string{
		`{"site":"letmoe","fields":{"catalog.work.display_name":"偽装"}}`,
		`{"actor":{"user_id":999},"fields":{"catalog.work.display_name":"偽装"}}`,
	} {
		status, raw = userEditReq(t, app, "POST", UserPrefix+"/works/submit",
			userToken(t, 812, ScopeCatalogEdit, "kungal-client"), body)
		assert.NotEqual(t, fiber.StatusOK, status, string(raw))
	}
	var maxID int64
	require.NoError(t, db.Raw(`SELECT coalesce(max(id), 0) FROM catalog_work`).Scan(&maxID).Error)
	assert.Equal(t, before, maxID, "no spoofed submission minted anything")

	// A submission without a name is the service's 422, reaching the wire through
	// this face's mapper.
	status, raw = userEditReq(t, app, "POST", UserPrefix+"/works/submit",
		userToken(t, 812, ScopeCatalogEdit, "kungal-client"), `{"fields":{}}`)
	assert.Equal(t, fiber.StatusUnprocessableEntity, status, string(raw))
}

// TestUserClaims_OwnerActionsNeedOwnership is the wave's new tooth, on a claim
// that already HAS an owner. The S2S plane checked only the tenant, so any of a
// site's users could be made to move any of that site's claims; here the uid IS
// the token, so a taken entry is its owner's alone. (A FREE entry is the other
// half of the rule and is the next test's subject.)
func TestUserClaims_OwnerActionsNeedOwnership(t *testing.T) {
	db := openCatalogTestDB(t)
	resetClaims(t, db)
	app := userClaimApp(db)
	work := seedClaimedWork(t, db, model.ClaimStatePending, 821)

	// A different user of the same tenant — the case the S2S face would have
	// allowed — is refused, and nothing moved.
	status, raw := userEditReq(t, app, "POST", userClaimActionPath(work, "withdraw"),
		userToken(t, 822, ScopeCatalogEdit, "kungal-client"), `{}`)
	assert.Equal(t, fiber.StatusForbidden, status, string(raw))

	// A cross-tenant token is refused by the tenancy check that was already there.
	status, raw = userEditReq(t, app, "POST", userClaimActionPath(work, "withdraw"),
		userToken(t, 821, ScopeCatalogEdit, "letmoe-client"), `{}`)
	assert.Equal(t, fiber.StatusForbidden, status, string(raw))
	assert.Equal(t, model.ClaimStateKeyPending, claimStateOf(t, db, work), "neither refusal moved it")

	// The owner's own token walks it back to draft and submits it again.
	owner := userToken(t, 821, ScopeCatalogEdit, "kungal-client")
	status, raw = userEditReq(t, app, "POST", userClaimActionPath(work, "withdraw"), owner, `{}`)
	require.Equal(t, fiber.StatusOK, status, string(raw))
	assert.Equal(t, model.ClaimStateKeyDraft, claimStateOf(t, db, work))

	status, raw = userEditReq(t, app, "POST", userClaimActionPath(work, "submit"), owner, `{}`)
	require.Equal(t, fiber.StatusOK, status, string(raw))
	var res struct {
		Data struct {
			From *string `json:"from_state"`
			To   string  `json:"to_state"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &res), string(raw))
	require.NotNil(t, res.Data.From)
	assert.Equal(t, model.ClaimStateKeyDraft, *res.Data.From)
	assert.Equal(t, model.ClaimStateKeyPending, res.Data.To)

	// Re-submitting from pending is an illegal move: the 409 echoes the state
	// that blocked it and the states it would have been legal from, so a caller
	// that raced another actor can re-render without a second read.
	status, raw = userEditReq(t, app, "POST", userClaimActionPath(work, "submit"), owner, `{}`)
	require.Equal(t, fiber.StatusConflict, status, string(raw))
	var conflict struct {
		Data struct {
			CurrentState string   `json:"current_state"`
			AllowedFrom  []string `json:"allowed_from"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &conflict), string(raw))
	assert.Equal(t, model.ClaimStateKeyPending, conflict.Data.CurrentState)
	assert.NotEmpty(t, conflict.Data.AllowedFrom)

	// The subject refusals are the service's: an unknown action is a 400 and an
	// unknown work a 404.
	status, _ = userEditReq(t, app, "POST", userClaimActionPath(work, "annihilate"), owner, `{}`)
	assert.Equal(t, fiber.StatusBadRequest, status)
	status, _ = userEditReq(t, app, "POST", userClaimActionPath(9_000_179, "withdraw"), owner, `{}`)
	assert.Equal(t, fiber.StatusNotFound, status)
}

// TestUserClaims_FirstClaimantAdoptsAFreeDraft is the OTHER half of the owner
// rule, and the half the product actually runs on: the registry's bulk is
// machine-imported mirror stock sitting in `draft` with no owner (prod holds
// ~53k such kungal drafts), and the wizard's "claim this game" is a person
// publishing one of them. It must work, and it must take the entry.
func TestUserClaims_FirstClaimantAdoptsAFreeDraft(t *testing.T) {
	db := openCatalogTestDB(t)
	resetClaims(t, db)
	app := userClaimApp(db)
	draft := seedClaimedWork(t, db, model.ClaimStateDraft, 0)

	var before *int64
	require.NoError(t, db.Raw(
		`SELECT owner_user_id FROM catalog_work WHERE id = ?`, draft).Scan(&before).Error)
	require.Nil(t, before, "the imported draft starts ownerless")

	// A plain user's token — no roles, no prior relationship to the row.
	status, raw := userEditReq(t, app, "POST", userClaimActionPath(draft, "publish"),
		userToken(t, 851, ScopeCatalogEdit, "kungal-client"), `{}`)
	require.Equal(t, fiber.StatusOK, status, string(raw))
	assert.Equal(t, model.ClaimStateKeyLive, claimStateOf(t, db, draft))

	var after *int64
	require.NoError(t, db.Raw(
		`SELECT owner_user_id FROM catalog_work WHERE id = ?`, draft).Scan(&after).Error)
	require.NotNil(t, after, "moving a free claim adopts it")
	assert.EqualValues(t, 851, *after, "the owner is the token's id claim")

	// Ownership took hold: the next person is fenced out of the same row.
	status, raw = userEditReq(t, app, "POST", userClaimActionPath(draft, "withdraw"),
		userToken(t, 852, ScopeCatalogEdit, "kungal-client"), `{}`)
	assert.Equal(t, fiber.StatusForbidden, status, string(raw))
	assert.Equal(t, model.ClaimStateKeyLive, claimStateOf(t, db, draft), "the refusal moved nothing")

	// …and the adopter still owns it.
	status, raw = userEditReq(t, app, "POST", userClaimActionPath(draft, "withdraw"),
		userToken(t, 851, ScopeCatalogEdit, "kungal-client"), `{}`)
	require.Equal(t, fiber.StatusOK, status, string(raw))
	assert.Equal(t, model.ClaimStateKeyDraft, claimStateOf(t, db, draft))
}

// TestUserClaims_ReviewActionsNeedThePermission: the review half's authority is
// the token's roles through the catalog family's own resolver — the same
// resolver the S2S face consults, which the DB permission overlay hot-swaps.
func TestUserClaims_ReviewActionsNeedThePermission(t *testing.T) {
	db := openCatalogTestDB(t)
	resetClaims(t, db)
	app := userClaimApp(db)
	work := seedClaimedWork(t, db, model.ClaimStatePending, 831)

	// Being the OWNER is not review authority: the two halves are different
	// questions, and the owner half cannot approve its own submission.
	for _, action := range []string{"approve", "decline", "ban", "unban"} {
		status, raw := userEditReq(t, app, "POST", userClaimActionPath(work, action),
			userToken(t, 831, ScopeCatalogEdit, "kungal-client"), `{"reason":"x"}`)
		assert.Equalf(t, fiber.StatusForbidden, status, "%s: %s", action, raw)
	}
	assert.Equal(t, model.ClaimStateKeyPending, claimStateOf(t, db, work), "no refusal decided anything")

	// A moderator's token approves it: the role arrives as a claim and resolves
	// to catalog.claim.review.
	status, raw := userEditReq(t, app, "POST", userClaimActionPath(work, "approve"),
		moderatorToken(t, 900), `{}`)
	require.Equal(t, fiber.StatusOK, status, string(raw))
	assert.Equal(t, model.ClaimStateKeyLive, claimStateOf(t, db, work))

	// The event records the moderator as the actor and the CLAIM's own site.
	var ev struct {
		ActorUID int64  `gorm:"column:actor_uid"`
		Site     string `gorm:"column:site"`
	}
	require.NoError(t, db.Raw(
		`SELECT actor_uid, site FROM catalog_claim_event WHERE work_id = ? ORDER BY id DESC LIMIT 1`, work,
	).Scan(&ev).Error)
	assert.EqualValues(t, 900, ev.ActorUID)
	assert.Equal(t, "kungal", ev.Site)

	// Declining without a reason is the service's 422 — a decline nobody can act
	// on is exactly what this face refuses to record.
	second := seedClaimedWork(t, db, model.ClaimStatePending, 832)
	status, raw = userEditReq(t, app, "POST", userClaimActionPath(second, "decline"),
		moderatorToken(t, 900), `{}`)
	assert.Equal(t, fiber.StatusUnprocessableEntity, status, string(raw))

	status, raw = userEditReq(t, app, "POST", userClaimActionPath(second, "decline"),
		moderatorToken(t, 900), `{"reason":"重複投稿"}`)
	require.Equal(t, fiber.StatusOK, status, string(raw))
	assert.Equal(t, model.ClaimStateKeyDeclined, claimStateOf(t, db, second))

	// A moderator of ANOTHER tenant is still fenced: this face's moderator acts
	// through one product's client, and that client's site is the only one it
	// may decide on.
	third := seedClaimedWork(t, db, model.ClaimStatePending, 833)
	status, raw = userEditReq(t, app, "POST", userClaimActionPath(third, "approve"),
		userTokenRoles(t, 901, ScopeCatalogEdit, "letmoe-client", "user", "moderator"), `{}`)
	assert.Equal(t, fiber.StatusForbidden, status, string(raw))
	assert.Equal(t, model.ClaimStateKeyPending, claimStateOf(t, db, third))
}

// TestUserClaims_Mine: the list is the token's own, on the token's own site, and
// there is no parameter that could make it somebody else's.
func TestUserClaims_Mine(t *testing.T) {
	db := openCatalogTestDB(t)
	resetClaims(t, db)
	app := userClaimApp(db)

	submit := func(token, name string) int64 {
		status, raw := userEditReq(t, app, "POST", UserPrefix+"/works/submit", token,
			fmt.Sprintf(`{"fields":{"catalog.work.display_name":%q}}`, name))
		require.Equal(t, fiber.StatusOK, status, string(raw))
		var env struct {
			Data struct {
				WorkID int64 `json:"work_id"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(raw, &env), string(raw))
		return env.Data.WorkID
	}
	mine := userToken(t, 841, ScopeCatalogEdit, "kungal-client")
	a := submit(mine, "私の投稿A")
	b := submit(mine, "私の投稿B")
	submit(userToken(t, 842, ScopeCatalogEdit, "kungal-client"), "他人の投稿")
	submit(userToken(t, 841, ScopeCatalogEdit, "letmoe-client"), "別テナントの投稿")

	type minePage struct {
		Data struct {
			Items []struct {
				WorkID      int64  `json:"work_id"`
				Site        string `json:"site"`
				ClaimState  string `json:"claim_state"`
				DisplayName string `json:"display_name"`
				LastEventID int64  `json:"last_event_id"`
			} `json:"items"`
			NextBefore int64 `json:"next_before"`
			Total      int64 `json:"total"`
		} `json:"data"`
	}
	get := func(query, token string) minePage {
		status, raw := userEditReq(t, app, "GET", UserPrefix+"/claims/mine"+query, token, "")
		require.Equal(t, fiber.StatusOK, status, string(raw))
		var page minePage
		require.NoError(t, json.Unmarshal(raw, &page), string(raw))
		return page
	}

	page := get("", mine)
	require.EqualValues(t, 2, page.Data.Total, "only this user's claims, on this user's site")
	require.Len(t, page.Data.Items, 2)
	assert.Equal(t, b, page.Data.Items[0].WorkID, "most recent activity first")
	assert.Equal(t, a, page.Data.Items[1].WorkID)
	for _, it := range page.Data.Items {
		assert.Equal(t, "kungal", it.Site)
		assert.Equal(t, model.ClaimStateKeyPending, it.ClaimState)
	}
	assert.Equal(t, page.Data.Items[1].LastEventID, page.Data.NextBefore)

	// The cursor pages, and the state filter narrows.
	next := get(fmt.Sprintf("?before=%d", page.Data.NextBefore), mine)
	assert.Empty(t, next.Data.Items)
	assert.EqualValues(t, 2, next.Data.Total, "the total counts the whole list, not the page")

	assert.EqualValues(t, 2, get("?claim_state=pending", mine).Data.Total)
	assert.EqualValues(t, 0, get("?claim_state=live", mine).Data.Total)

	// The other tenant's token sees that tenant's one claim and nothing else,
	// even though the uid is the same person.
	other := get("", userToken(t, 841, ScopeCatalogEdit, "letmoe-client"))
	require.EqualValues(t, 1, other.Data.Total)
	assert.Equal(t, "letmoe", other.Data.Items[0].Site)

	// A malformed claim_state is a 400 with the house vocabulary message.
	status, raw := userEditReq(t, app, "GET", UserPrefix+"/claims/mine?claim_state=published", mine, "")
	require.Equal(t, fiber.StatusBadRequest, status, string(raw))
	var errBody struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(raw, &errBody), string(raw))
	assert.Equal(t, msgBadClaimState, errBody.Message)
}
