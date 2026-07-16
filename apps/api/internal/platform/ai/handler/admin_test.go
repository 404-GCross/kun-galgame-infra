package handler

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"api/internal/middleware"
	aiModel "api/internal/platform/ai/model"
	aiPerm "api/internal/platform/ai/perm"
	"api/internal/platform/ai/service"
	"api/pkg/oidctoken"
	"api/pkg/utils"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file exercises the admin usage dashboard end-to-end against the shared
// kun_ai_test database (TestMain in handler_test.go migrates it): the JWT +
// ai.usage_view Fiber gate (admin/ren pass, moderator 403, anonymous 401), the
// site×route×channel summary aggregates, and the budget upsert / NULL-clear.

const adminTestSecret = "ai-admin-test-secret"

// TestSetupAdmin_RegistersOperations is the spec smoke: SetupAdmin with nil deps
// (the gen-openapi -ai-admin path) registers all four operations. GET+PUT
// /budgets share one path entry.
func TestSetupAdmin_RegistersOperations(t *testing.T) {
	api := SetupAdmin(fiber.New(), nil, nil)
	paths := api.OpenAPI().Paths
	for _, p := range []string{
		"/api/v1/admin/ai/usage/summary",
		"/api/v1/admin/ai/usage/daily",
		"/api/v1/admin/ai/budgets",
	} {
		assert.NotNilf(t, paths[p], "operation %s must be registered", p)
	}
}

// buildAdminApp wires the real admin gate (JWTAuth + ai.usage_view) over
// testDB-backed services, exactly like cmd/ai.
func buildAdminApp() *fiber.App {
	app := fiber.New()
	verifier := oidctoken.NewVerifier(adminTestSecret, nil) // HS256-only (no JWKS)
	app.Use("/api/v1/admin/ai",
		middleware.JWTAuth(verifier),
		middleware.RequirePermission(aiPerm.Resolver, aiPerm.UsageView))
	SetupAdmin(app, service.NewStatsService(testDB), service.NewBudgetService(testDB))
	return app
}

func adminToken(t *testing.T, roles ...string) string {
	t.Helper()
	tok, err := utils.GenerateAccessToken(adminTestSecret, utils.TokenClaims{ID: 1, Roles: roles}, time.Hour)
	require.NoError(t, err)
	return tok
}

// TestAdminGate pins the permission-first gate: anonymous → 401, a content
// moderator → 403 (usage cost is ops, not moderation), admin & ren → 200.
func TestAdminGate(t *testing.T) {
	truncateUsage(t)
	app := buildAdminApp()

	get := func(auth string) int {
		req := httptest.NewRequest("GET", "/api/v1/admin/ai/usage/summary", nil)
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		}
		resp, err := app.Test(req)
		require.NoError(t, err)
		return resp.StatusCode
	}

	assert.Equal(t, fiber.StatusUnauthorized, get(""), "anonymous → 401")
	assert.Equal(t, fiber.StatusForbidden, get(adminToken(t, "moderator")), "moderator → 403")
	assert.Equal(t, fiber.StatusForbidden, get(adminToken(t, "user")), "user → 403")
	assert.Equal(t, fiber.StatusOK, get(adminToken(t, "admin")), "admin → 200")
	assert.Equal(t, fiber.StatusOK, get(adminToken(t, "ren")), "ren → 200")
}

func truncateUsage(t *testing.T) {
	t.Helper()
	require.NoError(t, testDB.Exec("TRUNCATE ai_usage RESTART IDENTITY").Error)
}

// seedUsage inserts one metered row. CreatedAt is left zero so the DB default
// now() lands it inside every window; Status 0 (ok) is written explicitly (the
// column is NOT NULL with no default — the meaningful zero).
func seedUsage(t *testing.T, site, channel string, status int16, prompt, comp int, cost int64) {
	t.Helper()
	row := aiModel.AIUsage{
		Site: site, Route: aiModel.RouteModerateText, Status: status, Channel: channel,
		PromptTokens: prompt, CompletionTokens: comp, CostMicro: cost,
	}
	require.NoError(t, testDB.Create(&row).Error)
}

// TestSummaryAggregates seeds a known ledger and asserts the site×route×channel
// grouping, the status distribution, and the totalled overview + error rate.
func TestSummaryAggregates(t *testing.T) {
	truncateUsage(t)
	// letmoe served twice OK on a real channel, once degraded.
	seedUsage(t, "letmoe", "deepseek-chat", aiModel.StatusOK, 10, 20, 100)
	seedUsage(t, "letmoe", "deepseek-chat", aiModel.StatusOK, 10, 20, 100)
	seedUsage(t, "letmoe", "", aiModel.StatusDegraded, 0, 0, 0)
	// kungal hit one upstream error.
	seedUsage(t, "kungal", "", aiModel.StatusUpstreamError, 0, 0, 0)

	sum, err := service.NewStatsService(testDB).Summary(context.Background(), "30d")
	require.NoError(t, err)

	require.Len(t, sum.Rows, 3, "three (site,route,channel) groups")
	// The busiest group leads (ORDER BY calls DESC).
	top := sum.Rows[0]
	assert.Equal(t, "letmoe", top.Site)
	assert.Equal(t, aiModel.RouteModerateText, top.Route)
	assert.Equal(t, "deepseek-chat", top.Channel)
	assert.Equal(t, int64(2), top.Calls)
	assert.Equal(t, int64(20), top.PromptTokens)
	assert.Equal(t, int64(40), top.CompletionTokens)
	assert.Equal(t, int64(200), top.CostMicro)
	assert.Equal(t, int64(2), top.OK)

	o := sum.Overview
	assert.Equal(t, int64(4), o.Calls)
	assert.Equal(t, int64(2), o.OK)
	assert.Equal(t, int64(1), o.UpstreamError)
	assert.Equal(t, int64(1), o.Degraded)
	assert.Equal(t, int64(0), o.BudgetDenied)
	assert.Equal(t, int64(200), o.CostMicro)
	assert.InDelta(t, 0.5, o.ErrorRate, 1e-9, "non-OK fraction = 2/4")
	assert.Equal(t, "30d", sum.Window)
}

// TestDailySeries asserts the day×route trend rolls up the seeded rows.
func TestDailySeries(t *testing.T) {
	truncateUsage(t)
	seedUsage(t, "letmoe", "deepseek-chat", aiModel.StatusOK, 5, 5, 100)
	seedUsage(t, "letmoe", "deepseek-chat", aiModel.StatusOK, 5, 5, 100)

	series, err := service.NewStatsService(testDB).Daily(context.Background(), 14)
	require.NoError(t, err)
	assert.Equal(t, 14, series.Days)
	require.Len(t, series.Points, 1, "one day × one route")
	p := series.Points[0]
	assert.Equal(t, aiModel.RouteModerateText, p.Route)
	assert.Equal(t, int64(2), p.Calls)
	assert.Equal(t, int64(200), p.CostMicro)
}

// TestBudgetUpsertAndClear covers set → clear (NULL) → list, plus the
// unknown-route guard.
func TestBudgetUpsertAndClear(t *testing.T) {
	require.NoError(t, testDB.Exec("TRUNCATE ai_route_budget").Error)
	bs := service.NewBudgetService(testDB)
	ctx := context.Background()

	cap := int64(50000)
	set, err := bs.Upsert(ctx, aiModel.RouteModerateText, "letmoe", &cap)
	require.NoError(t, err)
	require.NotNil(t, set.DailyCostCapMicro)
	assert.Equal(t, int64(50000), *set.DailyCostCapMicro)

	// Re-upsert the SAME (route,site) with nil clears the cap to NULL (row kept).
	cleared, err := bs.Upsert(ctx, aiModel.RouteModerateText, "letmoe", nil)
	require.NoError(t, err)
	assert.Nil(t, cleared.DailyCostCapMicro, "nil cap clears to NULL")

	rows, err := bs.List(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 1, "upsert keeps one row for the (route,site)")
	assert.Nil(t, rows[0].DailyCostCapMicro)

	_, err = bs.Upsert(ctx, "bogus-route", "", &cap)
	assert.ErrorIs(t, err, service.ErrUnknownRoute, "unknown route rejected")
}

// TestBudgetUpsertOverHTTP proves the write path end-to-end through the gated
// admin face with an admin token and the house envelope.
func TestBudgetUpsertOverHTTP(t *testing.T) {
	require.NoError(t, testDB.Exec("TRUNCATE ai_route_budget").Error)
	app := buildAdminApp()

	body := `{"route":"moderate-text","site":"","daily_cost_cap_micro":123456}`
	req := httptest.NewRequest("PUT", "/api/v1/admin/ai/budgets", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken(t, "admin"))
	resp, err := app.Test(req)
	require.NoError(t, err)
	require.Equal(t, fiber.StatusOK, resp.StatusCode)

	var env struct {
		Code int `json:"code"`
		Data struct {
			Route             string `json:"route"`
			Site              string `json:"site"`
			DailyCostCapMicro *int64 `json:"daily_cost_cap_micro"`
		} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&env))
	assert.Equal(t, 0, env.Code)
	assert.Equal(t, "moderate-text", env.Data.Route)
	require.NotNil(t, env.Data.DailyCostCapMicro)
	assert.Equal(t, int64(123456), *env.Data.DailyCostCapMicro)
}
