package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"api/internal/middleware"
	"api/internal/platform/catalog/editspec"
	"api/internal/platform/catalog/model"
	"api/internal/platform/catalog/perm"
	"api/internal/platform/catalog/service"
	"api/internal/platform/editing"
	"api/pkg/oidctoken"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// The editing engine on the user-token face (wave 177). Every test here asks
// one question in a different place: did the actor come from the token? The
// engine's own rules (what a policy means, when a proposal automerges) are
// already pinned by the editspec and S2S suites and are NOT re-litigated —
// what is new is that roles now arrive as claims instead of as body fields.

// userEditApp wires the face as cmd/catalog does: JWTAuth + UserGate on the
// prefix, then the user Huma API over the SAME engine and per-family resolvers
// the S2S face uses.
func userEditApp(t *testing.T, db *gorm.DB, clients fakeClientLookup) *fiber.App {
	t.Helper()
	reg := editing.NewRegistry()
	require.NoError(t, editspec.RegisterWork(reg, db))

	app := fiber.New()
	verifier := oidctoken.NewVerifier(userTestSecret, nil) // HS256-only (no JWKS)
	app.Use(UserPrefix, middleware.JWTAuth(verifier), UserGate(clients))
	SetupUser(app, service.NewCoverVoteService(db), editing.NewEngine(db, reg),
		PermResolvers{"catalog": perm.Resolver})
	return app
}

// seedUserEditWork gives each test a clean engine state and one live work on
// the kungal tenant (whose overlay is propose=open + automerge=review — the
// posture a browser-borne editor actually meets).
func seedUserEditWork(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	for _, tbl := range []string{"edit_proposal_amendment", "edit_proposal", "edit_revision", "catalog_work_title", "catalog_work"} {
		require.NoError(t, db.Exec("TRUNCATE "+tbl+" RESTART IDENTITY CASCADE").Error)
	}
	work := &model.CatalogWork{MediumID: 1, OLang: "ja", DisplayName: "利用者面テスト", Status: model.WorkStatusLive}
	require.NoError(t, db.Create(work).Error)
	return work.ID
}

// seedUserEditOwnedWork is seedUserEditWork plus the wave-178 fact: the work
// carries an owner uid, exactly as the submission mint / claim birth stamps it.
func seedUserEditOwnedWork(t *testing.T, db *gorm.DB, ownerUID int64) int64 {
	t.Helper()
	work := seedUserEditWork(t, db)
	require.NoError(t, db.Exec(
		`UPDATE catalog_work SET owner_user_id = ?, site = 'kungal', product_work_id = ? WHERE id = ?`,
		ownerUID, work, work).Error)
	return work
}

func userEditClients() fakeClientLookup {
	return fakeClientLookup{
		"kungal-client": {ID: "kungal-client", CatalogSite: "kungal"},
		"letmoe-client": {ID: "letmoe-client", CatalogSite: "letmoe"},
	}
}

// userEditReq issues a request with an optional bearer token and JSON body.
func userEditReq(t *testing.T, app *fiber.App, method, path, token, body string) (int, []byte) {
	t.Helper()
	var req = httptest.NewRequest(method, path, nil)
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := app.Test(req)
	require.NoError(t, err)
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, raw
}

func userProposalBody(workID int64, name, note string) string {
	return fmt.Sprintf(
		`{"entity_type":"catalog.work","entity_id":%d,"note":%q,"patch":{"catalog.work.display_name":%q}}`,
		workID, note, name)
}

type userCreateEnvelope struct {
	Data struct {
		Proposal struct {
			ID          int64  `json:"id"`
			Status      string `json:"status"`
			ProposerUID int64  `json:"proposer_uid"`
			Site        string `json:"site"`
			Note        string `json:"note"`
		} `json:"proposal"`
		Merged   bool `json:"merged"`
		Revision *struct {
			Action   string `json:"action"`
			ActorUID int64  `json:"actor_uid"`
			Site     string `json:"site"`
		} `json:"revision"`
	} `json:"data"`
}

func decodeUserCreate(t *testing.T, raw []byte) userCreateEnvelope {
	t.Helper()
	var env userCreateEnvelope
	require.NoError(t, json.Unmarshal(raw, &env), string(raw))
	return env
}

// TestUserEdit_BehindTheSameGate: the new routes are not a second door. The
// eight-row refusal matrix is the gate's own test (TestUserCoverVote_Refusals);
// all that needs saying here is that these paths sit behind the same gate.
func TestUserEdit_BehindTheSameGate(t *testing.T) {
	db := openCatalogTestDB(t)
	work := seedUserEditWork(t, db)
	app := userEditApp(t, db, userEditClients())

	status, raw := userEditReq(t, app, "POST", UserPrefix+"/edit/proposals", "",
		userProposalBody(work, "無認証", ""))
	assert.Equal(t, fiber.StatusUnauthorized, status, string(raw))

	status, raw = userEditReq(t, app, "POST",
		fmt.Sprintf("%s/edit/proposals/1/withdraw", UserPrefix), "", "")
	assert.Equal(t, fiber.StatusUnauthorized, status, string(raw))

	status, raw = userEditReq(t, app, "GET", UserPrefix+"/edit/schema/catalog.work", "", "")
	assert.Equal(t, fiber.StatusUnauthorized, status, string(raw))

	var rows int64
	require.NoError(t, db.Raw(`SELECT count(*) FROM edit_proposal`).Scan(&rows).Error)
	assert.EqualValues(t, 0, rows, "no refusal filed anything")
}

// TestUserEdit_ProposerAndSiteComeFromTheToken is the wave's doctrine pinned on
// the row that lands in edit_proposal: a plain user's edit files an open
// proposal whose proposer is the token's `id` and whose tenant is the token
// client's catalog_site — neither value was on the wire.
func TestUserEdit_ProposerAndSiteComeFromTheToken(t *testing.T) {
	db := openCatalogTestDB(t)
	work := seedUserEditWork(t, db)
	app := userEditApp(t, db, userEditClients())

	status, raw := userEditReq(t, app, "POST", UserPrefix+"/edit/proposals",
		userToken(t, 501, ScopeCatalogEdit, "kungal-client"),
		userProposalBody(work, "利用者提案", "typo"))
	require.Equal(t, fiber.StatusOK, status, string(raw))

	env := decodeUserCreate(t, raw)
	assert.False(t, env.Data.Merged, "a plain user's kungal proposal waits for review")
	assert.Nil(t, env.Data.Revision)
	assert.Equal(t, "open", env.Data.Proposal.Status)
	assert.EqualValues(t, 501, env.Data.Proposal.ProposerUID, "the proposer is the token's id claim")
	assert.Equal(t, "kungal", env.Data.Proposal.Site, "the tenant is the token client's catalog_site")
	assert.Equal(t, "typo", env.Data.Proposal.Note)

	var row struct {
		ProposerUID int64  `gorm:"column:proposer_uid"`
		Site        string `gorm:"column:site"`
	}
	require.NoError(t, db.Raw(
		`SELECT proposer_uid, site FROM edit_proposal WHERE id = ?`, env.Data.Proposal.ID,
	).Scan(&row).Error)
	assert.EqualValues(t, 501, row.ProposerUID)
	assert.Equal(t, "kungal", row.Site)

	// The work itself is untouched: an open proposal is not an edit.
	var after model.CatalogWork
	require.NoError(t, db.First(&after, work).Error)
	assert.Equal(t, "利用者面テスト", after.DisplayName)

	// The engine's ordinary refusals reach the wire through this face too.
	status, raw = userEditReq(t, app, "POST", UserPrefix+"/edit/proposals",
		userToken(t, 501, ScopeCatalogEdit, "kungal-client"),
		fmt.Sprintf(`{"entity_type":"catalog.work","entity_id":%d,"patch":{"catalog.work.ghost":"x"}}`, work))
	assert.Equal(t, fiber.StatusUnprocessableEntity, status, string(raw))
}

// TestUserEdit_AdminTokenAutomerges proves the roles really flow from the token
// through the family's permission resolver: the SAME request that waits for
// review above lands directly when the token carries the admin role.
func TestUserEdit_AdminTokenAutomerges(t *testing.T) {
	db := openCatalogTestDB(t)
	work := seedUserEditWork(t, db)
	app := userEditApp(t, db, userEditClients())

	status, raw := userEditReq(t, app, "POST", UserPrefix+"/edit/proposals",
		userTokenRoles(t, 777, ScopeCatalogEdit, "kungal-client", "user", "admin"),
		userProposalBody(work, "管理者直編", ""))
	require.Equal(t, fiber.StatusOK, status, string(raw))

	env := decodeUserCreate(t, raw)
	assert.True(t, env.Data.Merged, "an admin token carries the review capability")
	require.NotNil(t, env.Data.Revision)
	assert.Equal(t, "direct", env.Data.Revision.Action)
	assert.EqualValues(t, 777, env.Data.Revision.ActorUID, "the revision's actor is the token's id")
	assert.Equal(t, "kungal", env.Data.Revision.Site)

	var after model.CatalogWork
	require.NoError(t, db.First(&after, work).Error)
	assert.Equal(t, "管理者直編", after.DisplayName)
}

// TestUserEdit_Withdraw: taking a proposal back needs no body, because the only
// identity in play is the token's — and it is checked twice, once against the
// proposal's tenant and once against its proposer.
func TestUserEdit_Withdraw(t *testing.T) {
	db := openCatalogTestDB(t)
	work := seedUserEditWork(t, db)
	app := userEditApp(t, db, userEditClients())

	file := func(uid uint) int64 {
		status, raw := userEditReq(t, app, "POST", UserPrefix+"/edit/proposals",
			userToken(t, uid, ScopeCatalogEdit, "kungal-client"),
			userProposalBody(work, fmt.Sprintf("提案%d", uid), ""))
		require.Equal(t, fiber.StatusOK, status, string(raw))
		return decodeUserCreate(t, raw).Data.Proposal.ID
	}
	withdrawPath := func(id int64) string {
		return fmt.Sprintf("%s/edit/proposals/%d/withdraw", UserPrefix, id)
	}

	id := file(601)

	// Another tenant's token is refused before the proposer check is reached:
	// the proposal is not that tenant's to close.
	status, raw := userEditReq(t, app, "POST", withdrawPath(id),
		userToken(t, 601, ScopeCatalogEdit, "letmoe-client"), "")
	assert.Equal(t, fiber.StatusForbidden, status, string(raw))

	// Same tenant, different user: the engine's proposer check refuses it.
	status, raw = userEditReq(t, app, "POST", withdrawPath(id),
		userToken(t, 602, ScopeCatalogEdit, "kungal-client"), "")
	assert.Equal(t, fiber.StatusForbidden, status, string(raw))

	var stillOpen int16
	require.NoError(t, db.Raw(`SELECT status FROM edit_proposal WHERE id = ?`, id).Scan(&stillOpen).Error)
	assert.Equal(t, editing.StatusOpen, stillOpen, "neither refusal closed it")

	// The proposer's own token closes it.
	status, raw = userEditReq(t, app, "POST", withdrawPath(id),
		userToken(t, 601, ScopeCatalogEdit, "kungal-client"), "")
	require.Equal(t, fiber.StatusOK, status, string(raw))
	var closed struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &closed))
	assert.Equal(t, "withdrawn", closed.Data.Status)

	// Withdrawing a closed proposal is a 409, and an unknown id a 404 — the
	// engine's answers, mapped by the S2S face's own error taste.
	status, _ = userEditReq(t, app, "POST", withdrawPath(id),
		userToken(t, 601, ScopeCatalogEdit, "kungal-client"), "")
	assert.Equal(t, fiber.StatusConflict, status)
	status, _ = userEditReq(t, app, "POST", withdrawPath(9_000_003),
		userToken(t, 601, ScopeCatalogEdit, "kungal-client"), "")
	assert.Equal(t, fiber.StatusNotFound, status)
}

// TestUserEdit_SchemaProjectsTheToken: the projection is the CALLER's, and the
// caller cannot be named on the wire. Two tokens, same entity type, different
// answers — and the actor query parameters the S2S op accepts do nothing here.
func TestUserEdit_SchemaProjectsTheToken(t *testing.T) {
	db := openCatalogTestDB(t)
	work := seedUserEditWork(t, db)
	app := userEditApp(t, db, userEditClients())

	type schemaEnvelope struct {
		Data struct {
			EntityType string `json:"entity_type"`
			Fields     []struct {
				Key            string `json:"key"`
				CanPropose     bool   `json:"can_propose"`
				CanReview      bool   `json:"can_review"`
				WouldAutomerge bool   `json:"would_automerge"`
			} `json:"fields"`
		} `json:"data"`
	}
	get := func(path, token string) schemaEnvelope {
		status, raw := userEditReq(t, app, "GET", path, token, "")
		require.Equal(t, fiber.StatusOK, status, string(raw))
		var env schemaEnvelope
		require.NoError(t, json.Unmarshal(raw, &env), string(raw))
		require.NotEmpty(t, env.Data.Fields)
		return env
	}
	anyReviewable := func(env schemaEnvelope) (review, automerge bool) {
		for _, f := range env.Data.Fields {
			review = review || f.CanReview
			automerge = automerge || f.WouldAutomerge
		}
		return
	}

	base := fmt.Sprintf("%s/edit/schema/catalog.work?entity_id=%d", UserPrefix, work)

	plain := get(base, userToken(t, 501, ScopeCatalogEdit, "kungal-client"))
	assert.Equal(t, "catalog.work", plain.Data.EntityType)
	review, automerge := anyReviewable(plain)
	assert.False(t, review, "a plain user reviews nothing on kungal")
	assert.False(t, automerge)
	proposable := false
	for _, f := range plain.Data.Fields {
		proposable = proposable || f.CanPropose
	}
	assert.True(t, proposable, "kungal's overlay lets any logged-in user propose")

	admin := get(base, userTokenRoles(t, 777, ScopeCatalogEdit, "kungal-client", "user", "admin"))
	review, automerge = anyReviewable(admin)
	assert.True(t, review, "the admin role arrives as a claim and resolves to the review perm")
	assert.True(t, automerge)

	// Asking to be somebody else changes nothing: this op declares no actor
	// parameters, so the strings below are inert query noise.
	spoofed := get(base+"&user_id=777&roles=admin&trust_tier=4&is_entity_owner=true&site=letmoe",
		userToken(t, 501, ScopeCatalogEdit, "kungal-client"))
	assert.Equal(t, plain.Data.Fields, spoofed.Data.Fields,
		"no query parameter can promote the caller")
}

// ---- the moderation ops (wave 178) -----------------------------------------

func userEditPath(id int64, verb string) string {
	return fmt.Sprintf("%s/edit/proposals/%d/%s", UserPrefix, id, verb)
}

// fileUserProposal files an open proposal on kungal as a plain user and returns
// its id — the subject every moderation test below decides on.
func fileUserProposal(t *testing.T, app *fiber.App, work int64, uid uint, name string) int64 {
	t.Helper()
	status, raw := userEditReq(t, app, "POST", UserPrefix+"/edit/proposals",
		userToken(t, uid, ScopeCatalogEdit, "kungal-client"),
		userProposalBody(work, name, ""))
	require.Equal(t, fiber.StatusOK, status, string(raw))
	env := decodeUserCreate(t, raw)
	require.False(t, env.Data.Merged, "a plain user's kungal proposal must stay open")
	return env.Data.Proposal.ID
}

// TestUserEdit_OwnershipIsACatalogFact is the wave's doctrine: the entry's
// creator reviews their own entry on the user face, and NOTHING on the wire
// said so. The uid is the token's, the ownership is the catalog's own column,
// and the engine puts the two together.
func TestUserEdit_OwnershipIsACatalogFact(t *testing.T) {
	db := openCatalogTestDB(t)
	work := seedUserEditOwnedWork(t, db, 901)
	app := userEditApp(t, db, userEditClients())

	// The schema projection already answers it: the owner may review, a
	// stranger with the same (empty) roles may not.
	canReview := func(uid uint) bool {
		status, raw := userEditReq(t, app, "GET",
			fmt.Sprintf("%s/edit/schema/catalog.work?entity_id=%d", UserPrefix, work),
			userToken(t, uid, ScopeCatalogEdit, "kungal-client"), "")
		require.Equal(t, fiber.StatusOK, status, string(raw))
		var env struct {
			Data struct {
				Fields []struct {
					CanReview bool `json:"can_review"`
				} `json:"fields"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(raw, &env), string(raw))
		require.NotEmpty(t, env.Data.Fields)
		for _, f := range env.Data.Fields {
			if f.CanReview {
				return true
			}
		}
		return false
	}
	assert.True(t, canReview(901), "the owner's own token projects can_review (kungal overlay: OwnerReview)")
	assert.False(t, canReview(902), "a stranger's token projects nothing of the sort")

	// And it holds on the write: a stranger's merge is refused, the owner's lands.
	id := fileUserProposal(t, app, work, 902, "他人の提案")
	status, raw := userEditReq(t, app, "POST", userEditPath(id, "merge"),
		userToken(t, 903, ScopeCatalogEdit, "kungal-client"), `{}`)
	assert.Equal(t, fiber.StatusForbidden, status, string(raw))

	status, raw = userEditReq(t, app, "POST", userEditPath(id, "merge"),
		userToken(t, 901, ScopeCatalogEdit, "kungal-client"), `{"note":"my entry"}`)
	require.Equal(t, fiber.StatusOK, status, string(raw))

	var after model.CatalogWork
	require.NoError(t, db.First(&after, work).Error)
	assert.Equal(t, "他人の提案", after.DisplayName, "the owner's merge landed the patch")

	// A second merge of the same proposal is a 409, and an unknown id a 404.
	status, _ = userEditReq(t, app, "POST", userEditPath(id, "merge"),
		userToken(t, 901, ScopeCatalogEdit, "kungal-client"), `{}`)
	assert.Equal(t, fiber.StatusConflict, status)
	status, _ = userEditReq(t, app, "POST", userEditPath(9_000_178, "merge"),
		userToken(t, 901, ScopeCatalogEdit, "kungal-client"), `{}`)
	assert.Equal(t, fiber.StatusNotFound, status)
}

// TestUserEdit_ModerationNeedsAuthority: the four moderation ops are gated by
// the SAME per-field review rule the S2S face applies — a plain token is
// refused everywhere, an admin token (roles from the claims) is not.
func TestUserEdit_ModerationNeedsAuthority(t *testing.T) {
	db := openCatalogTestDB(t)
	work := seedUserEditWork(t, db) // no owner: authority can only come from roles
	app := userEditApp(t, db, userEditClients())

	plain := func(uid uint) string { return userToken(t, uid, ScopeCatalogEdit, "kungal-client") }
	admin := func(uid uint) string {
		return userTokenRoles(t, uid, ScopeCatalogEdit, "kungal-client", "user", "admin")
	}
	amendBody := `{"set":{"catalog.work.display_name":"審査済み"},"note":"fix"}`
	revertBody := func(seq int) string {
		return fmt.Sprintf(
			`{"entity_type":"catalog.work","entity_id":%d,"to_seq":%d,"note":"undo"}`, work, seq)
	}

	// A plain user may propose, and may do nothing else.
	id := fileUserProposal(t, app, work, 502, "平民の提案")
	for _, tc := range []struct{ path, body string }{
		{userEditPath(id, "amendments"), amendBody},
		{userEditPath(id, "merge"), `{"note":"x"}`},
		{userEditPath(id, "decline"), `{"note":"x"}`},
		// revert is refused for the same reason, and is asserted below once the
		// entity has a revision to be put back to (with none, the engine's 404
		// answers first).
	} {
		status, raw := userEditReq(t, app, "POST", tc.path, plain(502), tc.body)
		assert.Equal(t, fiber.StatusForbidden, status, "%s: %s", tc.path, raw)
	}
	var stillOpen int16
	require.NoError(t, db.Raw(`SELECT status FROM edit_proposal WHERE id = ?`, id).Scan(&stillOpen).Error)
	assert.Equal(t, editing.StatusOpen, stillOpen, "no refusal decided anything")

	// The same requests with an admin token: amend, then decline.
	status, raw := userEditReq(t, app, "POST", userEditPath(id, "amendments"), admin(777), amendBody)
	require.Equal(t, fiber.StatusOK, status, string(raw))
	var amended struct {
		Data struct {
			AmenderUID int64          `json:"amender_uid"`
			Set        map[string]any `json:"set"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &amended), string(raw))
	assert.EqualValues(t, 777, amended.Data.AmenderUID, "the amender is the token's id")
	assert.Equal(t, "審査済み", amended.Data.Set["catalog.work.display_name"])

	// The detail read returns the amendment and the effective patch.
	status, raw = userEditReq(t, app, "GET",
		fmt.Sprintf("%s/edit/proposals/%d", UserPrefix, id), plain(502), "")
	require.Equal(t, fiber.StatusOK, status, string(raw))
	var detail struct {
		Data struct {
			EffectivePatch map[string]any `json:"effective_patch"`
			Amendments     []struct {
				Seq int `json:"seq"`
			} `json:"amendments"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &detail), string(raw))
	assert.Len(t, detail.Data.Amendments, 1)
	assert.Equal(t, "審査済み", detail.Data.EffectivePatch["catalog.work.display_name"])

	status, raw = userEditReq(t, app, "POST", userEditPath(id, "decline"), admin(777), `{"note":"no"}`)
	require.Equal(t, fiber.StatusOK, status, string(raw))
	var closed struct {
		Data struct {
			Status       string `json:"status"`
			DecisionNote string `json:"decision_note"`
			DecidedByUID *int64 `json:"decided_by_uid"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &closed), string(raw))
	assert.Equal(t, "declined", closed.Data.Status)
	assert.Equal(t, "no", closed.Data.DecisionNote)
	require.NotNil(t, closed.Data.DecidedByUID)
	assert.EqualValues(t, 777, *closed.Data.DecidedByUID, "the decider is the token's id")

	// Revert: the admin direct-edits twice (revisions 1 and 2), then puts the
	// entity back — through the same review authority a merge needs.
	for _, name := range []string{"第一版", "第二版"} {
		status, raw = userEditReq(t, app, "POST", UserPrefix+"/edit/proposals",
			admin(777), userProposalBody(work, name, ""))
		require.Equal(t, fiber.StatusOK, status, string(raw))
		require.True(t, decodeUserCreate(t, raw).Data.Merged)
	}
	status, raw = userEditReq(t, app, "POST", UserPrefix+"/edit/revert", plain(502), revertBody(1))
	assert.Equal(t, fiber.StatusForbidden, status, string(raw))

	status, raw = userEditReq(t, app, "POST", UserPrefix+"/edit/revert", admin(777), revertBody(1))
	require.Equal(t, fiber.StatusOK, status, string(raw))
	var reverted struct {
		Data struct {
			Revision struct {
				Action   string `json:"action"`
				ActorUID int64  `json:"actor_uid"`
				Site     string `json:"site"`
			} `json:"revision"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(raw, &reverted), string(raw))
	assert.Equal(t, "reverted", reverted.Data.Revision.Action)
	assert.EqualValues(t, 777, reverted.Data.Revision.ActorUID)
	assert.Equal(t, "kungal", reverted.Data.Revision.Site, "the tenant came from the token's client")

	var after model.CatalogWork
	require.NoError(t, db.First(&after, work).Error)
	assert.Equal(t, "第一版", after.DisplayName)
}

// TestUserEdit_ModerationIsFencedAndUnspoofable: every new op refuses a
// proposal filed on another tenant, and no request field can name a different
// actor — the two ways this face could have leaked back into "the caller says
// who it is".
func TestUserEdit_ModerationIsFencedAndUnspoofable(t *testing.T) {
	db := openCatalogTestDB(t)
	work := seedUserEditOwnedWork(t, db, 901)
	app := userEditApp(t, db, userEditClients())

	id := fileUserProposal(t, app, work, 902, "越境される提案")

	// A letmoe token — admin roles and all — cannot touch a kungal proposal.
	letmoeAdmin := userTokenRoles(t, 901, ScopeCatalogEdit, "letmoe-client", "user", "admin")
	for _, tc := range []struct {
		method, path, body string
	}{
		{"GET", fmt.Sprintf("%s/edit/proposals/%d", UserPrefix, id), ""},
		{"POST", userEditPath(id, "amendments"), `{"set":{"catalog.work.display_name":"越境"}}`},
		{"POST", userEditPath(id, "merge"), `{}`},
		{"POST", userEditPath(id, "decline"), `{"note":"x"}`},
	} {
		status, raw := userEditReq(t, app, tc.method, tc.path, letmoeAdmin, tc.body)
		assert.Equal(t, fiber.StatusForbidden, status, "%s %s: %s", tc.method, tc.path, raw)
	}

	// Spoofing: the S2S bodies' identity fields do not exist here, so a request
	// carrying them is rejected outright rather than obeyed. What matters is
	// only that it never decides anything — the status may be 422 (the schema
	// knows no such field) or 403 (the caller's real authority), never 200.
	for _, body := range []string{
		`{"note":"x","actor":{"user_id":901,"roles":["admin"],"is_entity_owner":true}}`,
		`{"note":"x","site":"letmoe"}`,
	} {
		status, raw := userEditReq(t, app, "POST", userEditPath(id, "merge"),
			userToken(t, 903, ScopeCatalogEdit, "kungal-client"), body)
		assert.NotEqual(t, fiber.StatusOK, status, string(raw))
	}
	status, raw := userEditReq(t, app, "POST", UserPrefix+"/edit/revert",
		userToken(t, 903, ScopeCatalogEdit, "kungal-client"),
		fmt.Sprintf(`{"entity_type":"catalog.work","entity_id":%d,"to_seq":1,"note":"x","actor":{"user_id":901}}`, work))
	assert.NotEqual(t, fiber.StatusOK, status, string(raw))

	var stillOpen int16
	require.NoError(t, db.Raw(`SELECT status FROM edit_proposal WHERE id = ?`, id).Scan(&stillOpen).Error)
	assert.Equal(t, editing.StatusOpen, stillOpen, "nothing above decided the proposal")

	// The token's OWN user still reads its own tenant's proposal.
	status, raw = userEditReq(t, app, "GET",
		fmt.Sprintf("%s/edit/proposals/%d", UserPrefix, id),
		userToken(t, 902, ScopeCatalogEdit, "kungal-client"), "")
	require.Equal(t, fiber.StatusOK, status, string(raw))
	status, _ = userEditReq(t, app, "GET",
		fmt.Sprintf("%s/edit/proposals/%d", UserPrefix, 9_000_178),
		userToken(t, 902, ScopeCatalogEdit, "kungal-client"), "")
	assert.Equal(t, fiber.StatusNotFound, status)
}
