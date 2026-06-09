package repository

import (
	"context"
	"os"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// TestRegistrationCounts_Integration exercises the real Postgres aggregation
// (incl. the `AT TIME ZONE ?` parameter binding). Skipped unless TEST_PG_DSN
// points at a populated kun_galgame_infra DB.
func TestRegistrationCounts_Integration(t *testing.T) {
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set TEST_PG_DSN to run (e.g. postgres://postgres:pass@localhost:5432/kun_galgame_infra)")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	repo := NewUserRepository(db)
	ctx := context.Background()
	const tz = "Asia/Shanghai"

	byDay, err := repo.RegistrationCountsByDay(ctx, time.Now().AddDate(0, 0, -14), tz)
	if err != nil {
		t.Fatalf("RegistrationCountsByDay: %v", err)
	}
	if len(byDay) == 0 {
		t.Fatal("RegistrationCountsByDay returned no days")
	}
	t.Logf("byDay: %d days, e.g. %v", len(byDay), byDay)

	loc := time.FixedZone(tz, 8*3600)
	now := time.Now().In(loc)
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	byHour, err := repo.RegistrationCountsByHour(ctx, dayStart, dayStart.AddDate(0, 0, 1), tz)
	if err != nil {
		t.Fatalf("RegistrationCountsByHour: %v", err)
	}
	for h := range byHour {
		if h < 0 || h > 23 {
			t.Fatalf("hour out of range: %d", h)
		}
	}
	t.Logf("byHour today: %d buckets", len(byHour))

	total, err := repo.CountAll(ctx)
	if err != nil {
		t.Fatalf("CountAll: %v", err)
	}
	if total == 0 {
		t.Fatal("CountAll returned 0")
	}
	t.Logf("CountAll: %d", total)
}
