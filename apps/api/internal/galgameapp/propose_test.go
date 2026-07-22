package galgameapp

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	catHandler "api/internal/platform/catalog/handler"
	"api/internal/platform/editing"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createResp is the minimal shape this suite reads. The create/mine envelopes
// nest under data.proposal / data.items; the get/withdraw envelopes carry a BARE
// EditProposalView at data level (data.id / data.status), so both are declared.
type createResp struct {
	Code int `json:"code"`
	Data struct {
		// create wrapper (EditProposalCreateResponse)
		Proposal struct {
			ID          int64  `json:"id"`
			ProposerUID int64  `json:"proposer_uid"`
			Site        string `json:"site"`
			Status      string `json:"status"`
		} `json:"proposal"`
		Merged bool `json:"merged"`
		// mine wrapper (EditProposalListResponse)
		Items []struct {
			ID          int64  `json:"id"`
			ProposerUID int64  `json:"proposer_uid"`
			Site        string `json:"site"`
		} `json:"items"`
		// bare EditProposalView (get / withdraw)
		ID     int64  `json:"id"`
		Status string `json:"status"`
	} `json:"data"`
}

func parseResp(t *testing.T, raw []byte) createResp {
	t.Helper()
	var r createResp
	require.NoError(t, json.Unmarshal(raw, &r), "body: %s", raw)
	return r
}

const proposeURL = "/internal/edit/proposals"

// TestProposeAuthMatrix walks the credential gates: no key, key-without-JWT,
// wrong-scope keys, scope isolation (both directions), and an unbound key.
func TestProposeAuthMatrix(t *testing.T) {
	if testDB == nil {
		t.Skip("no test database")
	}
	cleanupProposeRows(t)
	defer cleanupProposeRows(t)
	env := newProposeEnv(t)
	id := seedTestEntity(t, "orig")
	body := fmt.Sprintf(`{"entity_type":%q,"entity_id":%d,"patch":{%q:"new"}}`,
		testEntityType, id, testEntityType+".title")

	proposeKey := mintKey(t, "proposetest_p", "kungal", "internal", []string{"galgame:propose"})
	readKey := mintKey(t, "proposetest_r", "kungal", "internal", []string{"galgame:read"})
	writeKey := mintKey(t, "proposetest_w", "kungal", "internal", []string{"galgame:write"})
	unboundKey := mintKey(t, "proposetest_u", "", "internal", []string{"galgame:propose"})
	jwt := signJWT(t, 42)

	t.Run("no key → 401", func(t *testing.T) {
		status, _ := req(t, env.fiber, "POST", proposeURL, "", jwt, body)
		assert.Equal(t, fiber.StatusUnauthorized, status)
	})
	t.Run("key but no Bearer → 401 (jwtAuth)", func(t *testing.T) {
		status, _ := req(t, env.fiber, "POST", proposeURL, proposeKey, "", body)
		assert.Equal(t, fiber.StatusUnauthorized, status)
	})
	t.Run("read key (no propose scope) → 403 naming galgame:propose", func(t *testing.T) {
		status, raw := req(t, env.fiber, "POST", proposeURL, readKey, jwt, body)
		assert.Equal(t, fiber.StatusForbidden, status)
		assert.Contains(t, string(raw), "galgame:propose")
	})
	t.Run("write key (no propose scope) → 403 naming galgame:propose", func(t *testing.T) {
		status, raw := req(t, env.fiber, "POST", proposeURL, writeKey, jwt, body)
		assert.Equal(t, fiber.StatusForbidden, status)
		assert.Contains(t, string(raw), "galgame:propose")
	})
	t.Run("propose key on a read-scoped route → 403 (scope isolation)", func(t *testing.T) {
		status, _ := req(t, env.fiber, "GET", "/internal/stub-read", proposeKey, jwt, "")
		assert.Equal(t, fiber.StatusForbidden, status)
	})
	t.Run("unbound key → 403 (no site binding)", func(t *testing.T) {
		status, raw := req(t, env.fiber, "POST", proposeURL, unboundKey, jwt, body)
		assert.Equal(t, fiber.StatusForbidden, status)
		assert.Contains(t, string(raw), "not bound")
	})
	t.Run("bound propose key + JWT → 200", func(t *testing.T) {
		status, raw := req(t, env.fiber, "POST", proposeURL, proposeKey, jwt, body)
		require.Equal(t, fiber.StatusOK, status, string(raw))
	})
}

// TestProposeAssertionAndSiteInjection proves the platform DTO ignores any
// injected actor/roles/trust and any injected site: the proposal is filed as the
// JWT actor into the KEY-BOUND tenant, never the wire-supplied values.
func TestProposeAssertionAndSiteInjection(t *testing.T) {
	if testDB == nil {
		t.Skip("no test database")
	}
	cleanupProposeRows(t)
	defer cleanupProposeRows(t)
	env := newProposeEnv(t)
	id := seedTestEntity(t, "orig")
	key := mintKey(t, "proposetest_inj", "kungal", "internal", []string{"galgame:propose"})
	jwt := signJWT(t, 7)

	// The body smuggles actor (uid 9999, admin roles, trust 4, owner) AND a
	// foreign site. All must be ignored.
	body := fmt.Sprintf(`{
		"entity_type":%q,"entity_id":%d,
		"patch":{%q:"injected"},
		"actor":{"user_id":9999,"roles":["admin","ren"],"trust_tier":4,"is_entity_owner":true},
		"site":"evil-tenant","trust_tier":4,"roles":["admin"]
	}`, testEntityType, id, testEntityType+".title")

	status, raw := req(t, env.fiber, "POST", proposeURL, key, jwt, body)
	require.Equal(t, fiber.StatusOK, status, string(raw))
	r := parseResp(t, raw)
	assert.Equal(t, int64(7), r.Data.Proposal.ProposerUID, "proposer must be the JWT uid, not the injected 9999")
	assert.Equal(t, "kungal", r.Data.Proposal.Site, "site must be the key binding, not the injected tenant")
	assert.Equal(t, "open", r.Data.Proposal.Status, "injected admin/owner must NOT cause an automerge")
	assert.False(t, r.Data.Merged)
}

// TestProposeCrossFamily rejects a non-galgame entity on every family-fenced op.
func TestProposeCrossFamily(t *testing.T) {
	if testDB == nil {
		t.Skip("no test database")
	}
	cleanupProposeRows(t)
	defer cleanupProposeRows(t)
	env := newProposeEnv(t)
	key := mintKey(t, "proposetest_fam", "kungal", "internal", []string{"galgame:propose"})
	jwt := signJWT(t, 1)

	// create catalog.work → 422 (never reaches the engine).
	status, _ := req(t, env.fiber, "POST", proposeURL, key, jwt,
		`{"entity_type":"catalog.work","entity_id":1,"patch":{"catalog.work.display_name":"x"}}`)
	assert.Equal(t, fiber.StatusUnprocessableEntity, status)

	// snapshot catalog.work → 422.
	status, _ = req(t, env.fiber, "GET",
		"/internal/edit/snapshot?entity_type=catalog.work&entity_id=1", key, jwt, "")
	assert.Equal(t, fiber.StatusUnprocessableEntity, status)

	// schema catalog.work → 422.
	status, _ = req(t, env.fiber, "GET",
		"/internal/edit/schema/catalog.work?entity_id=1", key, jwt, "")
	assert.Equal(t, fiber.StatusUnprocessableEntity, status)
}

// TestProposeMineAndGetOwnership: mine forces ProposerUID to the JWT actor;
// get on another user's proposal is a flat 404 (anti-enumeration).
func TestProposeMineAndGetOwnership(t *testing.T) {
	if testDB == nil {
		t.Skip("no test database")
	}
	cleanupProposeRows(t)
	defer cleanupProposeRows(t)
	env := newProposeEnv(t)
	id := seedTestEntity(t, "orig")
	key := mintKey(t, "proposetest_mine", "kungal", "internal", []string{"galgame:propose"})
	body := func(v string) string {
		return fmt.Sprintf(`{"entity_type":%q,"entity_id":%d,"patch":{%q:%q}}`,
			testEntityType, id, testEntityType+".title", v)
	}

	// User 100 files a proposal; user 200 files another.
	s, raw := req(t, env.fiber, "POST", proposeURL, key, signJWT(t, 100), body("byhundred"))
	require.Equal(t, fiber.StatusOK, s, string(raw))
	p100 := parseResp(t, raw).Data.Proposal.ID
	s, raw = req(t, env.fiber, "POST", proposeURL, key, signJWT(t, 200), body("bytwohundred"))
	require.Equal(t, fiber.StatusOK, s, string(raw))
	p200 := parseResp(t, raw).Data.Proposal.ID

	// mine for user 100 returns ONLY 100's proposal.
	s, raw = req(t, env.fiber, "GET", proposeURL, key, signJWT(t, 100), "")
	require.Equal(t, fiber.StatusOK, s, string(raw))
	mine := parseResp(t, raw)
	require.Len(t, mine.Data.Items, 1)
	assert.Equal(t, p100, mine.Data.Items[0].ID)
	assert.Equal(t, int64(100), mine.Data.Items[0].ProposerUID)

	// get own → 200; get another's → 404 (not 403 — never reveal existence).
	s, _ = req(t, env.fiber, "GET", fmt.Sprintf("%s/%d", proposeURL, p100), key, signJWT(t, 100), "")
	assert.Equal(t, fiber.StatusOK, s)
	s, _ = req(t, env.fiber, "GET", fmt.Sprintf("%s/%d", proposeURL, p200), key, signJWT(t, 100), "")
	assert.Equal(t, fiber.StatusNotFound, s, "another user's proposal must be 404")
	s, _ = req(t, env.fiber, "GET", fmt.Sprintf("%s/999999", proposeURL), key, signJWT(t, 100), "")
	assert.Equal(t, fiber.StatusNotFound, s)
}

// TestProposeWithdraw: the proposer may withdraw; a non-proposer is rejected
// (the engine's native ErrNotProposer → 403).
func TestProposeWithdraw(t *testing.T) {
	if testDB == nil {
		t.Skip("no test database")
	}
	cleanupProposeRows(t)
	defer cleanupProposeRows(t)
	env := newProposeEnv(t)
	id := seedTestEntity(t, "orig")
	key := mintKey(t, "proposetest_wd", "kungal", "internal", []string{"galgame:propose"})

	s, raw := req(t, env.fiber, "POST", proposeURL, key, signJWT(t, 100),
		fmt.Sprintf(`{"entity_type":%q,"entity_id":%d,"patch":{%q:"v"}}`, testEntityType, id, testEntityType+".title"))
	require.Equal(t, fiber.StatusOK, s, string(raw))
	pid := parseResp(t, raw).Data.Proposal.ID
	wd := fmt.Sprintf("%s/%d/withdraw", proposeURL, pid)

	// Non-proposer (user 200) → 403.
	s, _ = req(t, env.fiber, "POST", wd, key, signJWT(t, 200), "")
	assert.Equal(t, fiber.StatusForbidden, s)

	// Proposer (user 100) → 200, proposal now withdrawn (bare view at data level).
	s, raw = req(t, env.fiber, "POST", wd, key, signJWT(t, 100), "")
	require.Equal(t, fiber.StatusOK, s, string(raw))
	assert.Equal(t, "withdrawn", parseResp(t, raw).Data.Status)
}

// TestProposeEquivalence (G4): the SAME patch filed by a plain user through the
// platform face vs directly via the engine (the S2S plain-actor path) yields an
// identical proposal row — same entity ref, patch, proposer, site, status, base.
func TestProposeEquivalence(t *testing.T) {
	if testDB == nil {
		t.Skip("no test database")
	}
	cleanupProposeRows(t)
	defer cleanupProposeRows(t)
	env := newProposeEnv(t)
	id := seedTestEntity(t, "orig")
	key := mintKey(t, "proposetest_eq", "kungal", "internal", []string{"galgame:propose"})
	patchVal := "equivalence-value"

	// Platform face.
	s, raw := req(t, env.fiber, "POST", proposeURL, key, signJWT(t, 55),
		fmt.Sprintf(`{"entity_type":%q,"entity_id":%d,"note":"n","patch":{%q:%q}}`,
			testEntityType, id, testEntityType+".title", patchVal))
	require.Equal(t, fiber.StatusOK, s, string(raw))
	var viaFace editing.Proposal
	require.NoError(t, testDB.First(&viaFace, parseResp(t, raw).Data.Proposal.ID).Error)

	// Engine directly with the S2S plain-actor context (roles ∅ ⇒ HasPerm false,
	// trust 0, not owner) — behaviorally identical to what the S2S face builds.
	plain := editing.PolicyContext{UserID: 55, Site: "kungal", HasPerm: func(string) bool { return false }}
	prop, rev, err := env.engine.CreateProposal(context.Background(), editing.CreateProposalInput{
		EntityType: testEntityType, EntityID: id, Note: "n",
		Patch: map[string]any{testEntityType + ".title": patchVal}, Actor: plain,
	})
	require.NoError(t, err)
	require.Nil(t, rev, "plain actor must never automerge")

	assert.Equal(t, prop.EntityFamily, viaFace.EntityFamily)
	assert.Equal(t, prop.EntityType, viaFace.EntityType)
	assert.Equal(t, prop.EntityID, viaFace.EntityID)
	assert.Equal(t, prop.ProposerUID, viaFace.ProposerUID)
	assert.Equal(t, prop.Site, viaFace.Site)
	assert.Equal(t, prop.Status, viaFace.Status)
	assert.Equal(t, prop.BaseRevisionSeq, viaFace.BaseRevisionSeq)
	assert.JSONEq(t, string(prop.Patch), string(viaFace.Patch))
	assert.Equal(t, "open", editing.StatusName[viaFace.Status])
}

// newS2SApp builds the S2S edit face over the SAME engine so schema/snapshot
// can be compared byte-for-byte with the platform face.
func newS2SApp(t *testing.T, engine *editing.Engine) *fiber.App {
	t.Helper()
	f := fiber.New()
	api := catHandler.Setup(f, nil, nil, nil, nil, nil)
	catHandler.SetupEdit(api, engine, catHandler.PermResolvers{familyGalgame: galgameResolver})
	return f
}

// TestProposeSchemaSnapshotByteIdentity (G3/P10): the platform schema + snapshot
// responses are BYTE-IDENTICAL to the S2S face's plain-actor responses.
func TestProposeSchemaSnapshotByteIdentity(t *testing.T) {
	if testDB == nil {
		t.Skip("no test database")
	}
	cleanupProposeRows(t)
	defer cleanupProposeRows(t)
	env := newProposeEnv(t)
	s2s := newS2SApp(t, env.engine)
	id := seedTestEntity(t, "current-title")
	key := mintKey(t, "proposetest_iso", "kungal", "internal", []string{"galgame:propose"})
	jwt := signJWT(t, 88)

	// snapshot.
	_, platSnap := req(t, env.fiber, "GET",
		fmt.Sprintf("/internal/edit/snapshot?entity_type=%s&entity_id=%d", testEntityType, id), key, jwt, "")
	_, s2sSnap := req(t, s2s, "GET",
		fmt.Sprintf("/api/v1/catalog/edit/snapshot?entity_type=%s&entity_id=%d", testEntityType, id), "", "", "")
	assert.Equal(t, stripHumaDecoration(s2sSnap), stripHumaDecoration(platSnap),
		"snapshot envelope (code/message/data) must be byte-identical to the S2S plain-actor response")

	// schema (plain actor: user_id=88, trust 0, no roles, not owner; site=kungal).
	_, platSchema := req(t, env.fiber, "GET",
		fmt.Sprintf("/internal/edit/schema/%s?entity_id=%d", testEntityType, id), key, jwt, "")
	_, s2sSchema := req(t, s2s, "GET",
		fmt.Sprintf("/api/v1/catalog/edit/schema/%s?entity_id=%d&site=kungal&user_id=88&trust_tier=0", testEntityType, id), "", "", "")
	assert.Equal(t, stripHumaDecoration(s2sSchema), stripHumaDecoration(platSchema),
		"schema envelope (code/message/data) must be byte-identical to the S2S plain-actor response")
}

// humaSchemaField matches Huma's response-body decoration: a leading
// "$schema":"…" property the humafiber adapter injects (parser-invisible — the
// BFF unmarshals into typed structs, ignoring unknown fields — and absent from
// the pure-Fiber platform face). Stripping it (and the trailing newline Huma
// writes) leaves the house {code,message,data} envelope both faces share, which
// is then compared byte-for-byte.
var humaSchemaField = regexp.MustCompile(`^\{"\$schema":"[^"]*",`)

func stripHumaDecoration(raw []byte) string {
	return strings.TrimRight(humaSchemaField.ReplaceAllString(string(raw), "{"), "\n")
}

// TestProposeE2EChain (G3): the full dev chain submit → mine → get → withdraw.
func TestProposeE2EChain(t *testing.T) {
	if testDB == nil {
		t.Skip("no test database")
	}
	cleanupProposeRows(t)
	defer cleanupProposeRows(t)
	env := newProposeEnv(t)
	id := seedTestEntity(t, "orig")
	key := mintKey(t, "proposetest_e2e", "kungal", "internal", []string{"galgame:propose"})
	jwt := signJWT(t, 321)

	// submit.
	s, raw := req(t, env.fiber, "POST", proposeURL, key, jwt,
		fmt.Sprintf(`{"entity_type":%q,"entity_id":%d,"note":"e2e","patch":{%q:"e2e-title"}}`,
			testEntityType, id, testEntityType+".title"))
	require.Equal(t, fiber.StatusOK, s, string(raw))
	created := parseResp(t, raw)
	require.Equal(t, "open", created.Data.Proposal.Status)
	pid := created.Data.Proposal.ID

	// mine.
	s, raw = req(t, env.fiber, "GET", proposeURL, key, jwt, "")
	require.Equal(t, fiber.StatusOK, s, string(raw))
	require.Len(t, parseResp(t, raw).Data.Items, 1)

	// get (own) — bare EditProposalView at data level.
	s, raw = req(t, env.fiber, "GET", fmt.Sprintf("%s/%d", proposeURL, pid), key, jwt, "")
	require.Equal(t, fiber.StatusOK, s, string(raw))
	assert.Equal(t, pid, parseResp(t, raw).Data.ID)

	// withdraw.
	s, raw = req(t, env.fiber, "POST", fmt.Sprintf("%s/%d/withdraw", proposeURL, pid), key, jwt, "")
	require.Equal(t, fiber.StatusOK, s, string(raw))
	assert.Equal(t, "withdrawn", parseResp(t, raw).Data.Status)

	// mine for open-only is now empty.
	s, raw = req(t, env.fiber, "GET", proposeURL+"?status=open", key, jwt, "")
	require.Equal(t, fiber.StatusOK, s, string(raw))
	assert.Empty(t, parseResp(t, raw).Data.Items)
}
