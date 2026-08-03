package seed

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGeneratedArtifactsDrift re-runs the generation logic and asserts the
// checked-in artifacts match byte-exactly (same gate pattern as the repo's
// openapi types drift check). A red run means someone changed rolegen.go or
// refreshed the bangumicommon snapshot without re-running seed/gen — fix by
// `go run ./internal/platform/catalog/seed/gen` and reviewing the diff.
func TestGeneratedArtifactsDrift(t *testing.T) {
	generated, err := GenerateBangumiRoles()
	require.NoError(t, err)

	wantRoles, err := RenderRolesYAML(generated.Roles)
	require.NoError(t, err)
	gotRoles, err := dataFS.ReadFile("data/roles.gen.yaml")
	require.NoError(t, err)
	assert.Equal(t, string(wantRoles), string(gotRoles), "data/roles.gen.yaml drifted from generation logic")

	wantMap, err := RenderRoleMapYAML(generated.Mappings)
	require.NoError(t, err)
	gotMap, err := dataFS.ReadFile("data/bangumi_role_map.gen.yaml")
	require.NoError(t, err)
	assert.Equal(t, string(wantMap), string(gotMap), "data/bangumi_role_map.gen.yaml drifted from generation logic")
}

func TestGenerateBangumiRoles(t *testing.T) {
	generated, err := GenerateBangumiRoles()
	require.NoError(t, err)
	roles, mappings := generated.Roles, generated.Mappings

	// Deduplication really merged cross-media same-name positions (226 at
	// snapshot time), and full 246-position mapping coverage remains.
	assert.Greater(t, len(roles), 100)
	assert.Less(t, len(roles), 246)
	assert.Len(t, mappings, 246)

	// Keys globally unique; IDs sequential from 100 in key order.
	keys := make(map[string]bool, len(roles))
	for i, r := range roles {
		assert.False(t, keys[r.Key], "duplicate key %q", r.Key)
		keys[r.Key] = true
		assert.Equal(t, roleIDBase+int64(i), r.ID)
		if i > 0 {
			assert.Less(t, roles[i-1].Key, r.Key, "roles not in key order")
		}
	}

	// Every mapping points at an existing role and parses as "<type>:<position>".
	byID := make(map[int64]RoleSeed, len(roles))
	for _, r := range roles {
		byID[r.ID] = r
	}
	seenSourceRoles := make(map[string]bool, len(mappings))
	for _, m := range mappings {
		_, ok := byID[m.RoleID]
		assert.True(t, ok, "mapping %s references unknown role %d", m.SourceRole, m.RoleID)
		assert.False(t, seenSourceRoles[m.SourceRole], "duplicate mapping for %s", m.SourceRole)
		seenSourceRoles[m.SourceRole] = true
		st, pos, found := strings.Cut(m.SourceRole, ":")
		require.True(t, found)
		_, err := strconv.Atoi(st)
		assert.NoError(t, err)
		_, err = strconv.Atoi(pos)
		assert.NoError(t, err)
	}

	// Spot checks pinned to the snapshot: game position 1001 = Developer;
	// the shared-EN "Producer" group disambiguates deterministically.
	m4x1001, ok := findMapping(mappings, "4:1001")
	require.True(t, ok)
	assert.Contains(t, byID[m4x1001.RoleID].Key, "developer")
	assert.Equal(t, "开发", m4x1001.Note)
	assert.True(t, keys["producer"] && keys["producer-2"] && keys["producer-3"])
}

func findMapping(mappings []RoleMapSeed, sourceRole string) (RoleMapSeed, bool) {
	for _, m := range mappings {
		if m.SourceRole == sourceRole {
			return m, true
		}
	}
	return RoleMapSeed{}, false
}

// TestHandSeedsIntegrity guards the invariants of the pinned hand-written
// seeds (the values themselves are pinned by refs/proj/02 and reviewed there).
func TestHandSeedsIntegrity(t *testing.T) {
	assert.Len(t, media(), 7)
	// 11 pinned by refs/proj/02 + galgame_wiki (id 12, step 52 bridged-media
	// provenance) + upscale (id 13, step 53 derived-cover provenance) + cien
	// (id 14, refs/proj/83 E2b org/label link facet) + dmm (id 15, step 91
	// EG cross-reference store lane) + web (id 16, refs/plans/10 W0 generic
	// external-page catch-all for the rescued wiki links) + getchu (id 17,
	// refs/proj/167 — the character-roster source, anchored via VNDB extlinks).
	assert.Len(t, sources(), 17)
	// 13 pinned by refs/proj/02 + 3 symmetric character/setting-variation keys
	// added in step 30 (shares_character / alternative_setting / alternative_version).
	assert.Len(t, relationTypes(), 16)
	// The 48 VNDB platform codes (step 96, refs/proj/96) — the full distinct
	// set observed in src_vndb.releases_platforms; ids seed-owned, keys unique.
	assert.Len(t, platforms(), 48)
	seenPlat := map[string]struct{}{}
	for _, p := range platforms() {
		assert.NotEmpty(t, p.Key)
		assert.NotEmpty(t, p.DisplayName, "%s: display name required", p.Key)
		_, dup := seenPlat[p.Key]
		assert.False(t, dup, "%s: duplicate platform key", p.Key)
		seenPlat[p.Key] = struct{}{}
	}

	// The generated role map hard-codes the bangumi source id — keep them in sync.
	var bangumiOK bool
	for _, s := range sources() {
		if s.ID == bangumiSourceID {
			assert.Equal(t, "bangumi", s.Key)
			bangumiOK = true
		}
	}
	assert.True(t, bangumiOK)

	for _, rt := range relationTypes() {
		if rt.IsSymmetric {
			assert.Equal(t, rt.ForwardPhrase, rt.ReversePhrase, "%s: symmetric relation must render one phrase", rt.Key)
		}
	}

	// Generated artifacts load into model rows without error.
	roles, roleMap, err := loadGeneratedRoles()
	require.NoError(t, err)
	assert.Len(t, roleMap, 246)
	assert.NotEmpty(t, roles)
	for _, m := range roleMap {
		assert.Equal(t, bangumiSourceID, m.SourceID)
	}

	// Reserved-band roles (1-99) must never collide with the generated
	// vocabulary (100+) on id OR key — catalog_role.key is UNIQUE, so a hand
	// role reusing a generated key (e.g. "editor", id 177) would break the seed
	// upsert. This guard would have caught exactly that (refs/proj/80).
	roleIDs := make(map[int64]bool, len(roles))
	roleKeys := make(map[string]bool, len(roles))
	for _, r := range roles {
		roleIDs[r.ID] = true
		roleKeys[r.Key] = true
	}
	for _, h := range handRoles() {
		assert.Less(t, h.ID, int64(roleIDBase), "hand role %q must stay in the reserved band", h.Key)
		assert.False(t, roleIDs[h.ID], "hand role id %d collides with a generated role", h.ID)
		assert.False(t, roleKeys[h.Key], "hand role key %q collides with a generated role", h.Key)
		roleIDs[h.ID] = true
		roleKeys[h.Key] = true
	}

	// The three step-80 reserved slots are pinned by id + key + category.
	want := map[int64]struct{ key, cat string }{
		roleTranslator: {"translator", "other"},
		roleEditor:     {"text-editor", "other"}, // key deviates: "editor" is taken (id 177)
		roleQA:         {"qa", "other"},
	}
	for id, w := range want {
		var found bool
		for _, h := range handRoles() {
			if h.ID == id {
				found = true
				assert.Equal(t, w.key, h.Key)
				assert.Equal(t, w.cat, h.Category)
			}
		}
		assert.True(t, found, "reserved role id %d missing from handRoles", id)
	}

	// Every VNDB role now maps, each onto a known role id (translator/editor/qa
	// onto the reserved slots — no more unmapped VNDB roles).
	vm := make(map[string]int64)
	for _, m := range vndbRoleMap() {
		assert.Equal(t, vndbSourceID, m.SourceID)
		assert.True(t, roleIDs[m.RoleID], "vndb map %q → unknown role %d", m.SourceRole, m.RoleID)
		vm[m.SourceRole] = m.RoleID
	}
	assert.Equal(t, roleTranslator, vm["translator"])
	assert.Equal(t, roleEditor, vm["editor"])
	assert.Equal(t, roleQA, vm["qa"])
}
