package handler

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAdminHuma_RenGate_Forbidden locks in the authorization rewire: the
// browsing / mutating admin ops (list, delete, reclaim) must return 403 when the
// caller lacks the "ren" role — and must do so BEFORE any DB access (nil deps are
// never reached because requireRen runs first). The prefix is already admin-gated
// at the Fiber layer; this is the extra ren restriction, now enforced in-handler
// because Huma routes don't inherit the /admin group middleware.
func TestAdminHuma_RenGate_Forbidden(t *testing.T) {
	s := &AdminHumaServer{h: NewAdmin(nil, nil, nil, 0)} // nil deps: ren check precedes them
	ctx := context.Background()                          // no roles lifted → not ren

	assert403 := func(name string, err error) {
		t.Helper()
		var he *houseError
		require.Truef(t, errors.As(err, &he), "%s: expected houseError, got %v", name, err)
		assert.Equalf(t, http.StatusForbidden, he.status, "%s must 403 without the ren role", name)
	}

	_, err := s.list(ctx, &adminListInput{})
	assert403("list", err)
	_, err = s.delete(ctx, &adminUUIDInput{UUID: "x"})
	assert403("delete", err)
	_, err = s.reclaim(ctx, &adminUUIDInput{UUID: "x"})
	assert403("reclaim", err)
}

// TestAdminHasRole covers the ctx role lookup that AdminAuthBridge feeds.
func TestAdminHasRole(t *testing.T) {
	ctx := context.WithValue(context.Background(), ctxKeyAdminRoles, []string{"admin", "ren"})
	assert.True(t, adminHasRole(ctx, "ren"))
	assert.True(t, adminHasRole(ctx, "admin"))
	assert.False(t, adminHasRole(ctx, "moderator"))
	assert.False(t, adminHasRole(context.Background(), "ren"), "no roles in ctx → no role")
}

// TestSetupAdmin_RegistersOperations is a smoke test that all four admin
// operations are registered (also what cmd/gen-openapi -admin exports).
func TestSetupAdmin_RegistersOperations(t *testing.T) {
	api := SetupAdmin(fiber.New(), NewAdmin(nil, nil, nil, 0))
	paths := api.OpenAPI().Paths
	for _, p := range []string{
		"/api/v1/admin/artifact/list",
		"/api/v1/admin/artifact/stats",
		"/api/v1/admin/artifact/{uuid}",
		"/api/v1/admin/artifact/{uuid}/reclaim",
	} {
		assert.NotNilf(t, paths[p], "operation %s must be registered", p)
	}
}
