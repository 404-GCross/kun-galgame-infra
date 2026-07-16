package service

import (
	"context"
	"fmt"
	"os"
	"testing"

	"api/internal/platform/ai/dbtest"
	"api/internal/platform/ai/migrate"
	"api/internal/platform/ai/model"
	"api/internal/platform/ai/upstream"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Integration tests against a real Postgres (the trust/community convention):
// TEST_DATABASE_DSN or a local default; a missing database skips the whole
// package. Schema comes from migrate.Run — the exact production migration.

var testDB *gorm.DB

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		dsn = "host=localhost port=5432 user=postgres password=postgres dbname=kun_ai_test sslmode=disable"
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: cannot connect to test database: %v\n", err)
		os.Exit(0)
	}
	sqlDB, _ := db.DB()
	release := dbtest.AcquireSuiteLock(sqlDB)

	if err := migrate.Run(db); err != nil {
		release()
		fmt.Fprintf(os.Stderr, "SKIP: ai migration failed: %v\n", err)
		os.Exit(0)
	}
	testDB = db
	code := m.Run()
	release()
	os.Exit(code)
}

// cleanTables truncates the mutable tables so ordering-independent probes start
// fresh.
func cleanTables(t *testing.T) {
	t.Helper()
	for _, table := range []string{"ai_usage", "ai_route_budget"} {
		if err := testDB.Exec("TRUNCATE " + table + " RESTART IDENTITY CASCADE").Error; err != nil {
			t.Fatalf("truncate %s: %v", table, err)
		}
	}
}

// fakeUpstream is a deterministic upstreamClient: it records whether it was
// dialled (calls) so the degraded / over-budget paths can assert "no call".
type fakeUpstream struct {
	configured bool
	model      string
	result     upstream.ChatResult
	err        error
	calls      int
}

func (f *fakeUpstream) Configured() bool { return f.configured }
func (f *fakeUpstream) Model() string    { return f.model }
func (f *fakeUpstream) ChatJSON(_ context.Context, _, _ string, _ int) (upstream.ChatResult, error) {
	f.calls++
	if f.err != nil {
		return upstream.ChatResult{}, f.err
	}
	return f.result, nil
}

// insertBudget writes a budget row (cap nil = an explicit no-cap override row).
func insertBudget(t *testing.T, route, site string, cap *int64) {
	t.Helper()
	if err := testDB.Create(&model.AIRouteBudget{Route: route, Site: site, DailyCostCapMicro: cap}).Error; err != nil {
		t.Fatalf("insert budget %s/%s: %v", route, site, err)
	}
}

// insertUsageCost writes a usage row with an explicit cost_micro dated now, to
// simulate accumulated daily spend for the budget fuse.
func insertUsageCost(t *testing.T, site, route string, cost int64) {
	t.Helper()
	if err := testDB.Create(&model.AIUsage{
		Site: site, Route: route, Status: model.StatusOK, CostMicro: cost,
	}).Error; err != nil {
		t.Fatalf("insert usage: %v", err)
	}
}

// usageRows returns the metered rows for a site+route ordered by id.
func usageRows(t *testing.T, site, route string) []model.AIUsage {
	t.Helper()
	var rows []model.AIUsage
	if err := testDB.Where("site = ? AND route = ?", site, route).Order("id").Find(&rows).Error; err != nil {
		t.Fatalf("load usage rows: %v", err)
	}
	return rows
}

func ptr[T any](v T) *T { return &v }
