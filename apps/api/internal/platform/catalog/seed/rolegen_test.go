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
	assert.Len(t, sources(), 11)
	// 13 pinned by refs/proj/02 + 3 symmetric character/setting-variation keys
	// added in step 30 (shares_character / alternative_setting / alternative_version).
	assert.Len(t, relationTypes(), 16)

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
}
