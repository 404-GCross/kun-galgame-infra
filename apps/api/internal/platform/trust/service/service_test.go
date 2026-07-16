package service

import (
	"context"
	"fmt"
	"os"
	"testing"

	"api/internal/platform/trust/dbtest"
	"api/internal/platform/trust/migrate"
	"api/internal/platform/trust/model"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Integration tests against a real Postgres (the community convention):
// TEST_DATABASE_DSN or a local default; a missing database skips the whole
// package. Schema comes from migrate.Run — the exact production migration.

var testDB *gorm.DB

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		dsn = "host=localhost port=5432 user=postgres password=postgres dbname=kun_trust_test sslmode=disable"
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
		fmt.Fprintf(os.Stderr, "SKIP: trust migration failed: %v\n", err)
		os.Exit(0)
	}
	testDB = db
	code := m.Run()
	release()
	os.Exit(code)
}

// cleanTables truncates the mutable tables so ordering-independent probes start
// fresh. trust_report_reason is left intact (its global seeds are provisioned by
// migrate and reused across tests).
func cleanTables(t *testing.T) {
	t.Helper()
	for _, table := range []string{
		"trust_audit_log", "trust_disposition", "trust_report",
		"trust_review_item", "trust_subject_kind", "trust_scan_result",
	} {
		if err := testDB.Exec("TRUNCATE " + table + " RESTART IDENTITY CASCADE").Error; err != nil {
			t.Fatalf("truncate %s: %v", table, err)
		}
	}
}

// fakeWeigher is a deterministic Weigher: per-reporter overrides, else a default.
type fakeWeigher struct {
	weights map[int64]ReporterWeight
	def     ReporterWeight
}

func newWeigher() *fakeWeigher {
	return &fakeWeigher{weights: map[int64]ReporterWeight{}, def: ReporterWeight{Weight: 1.0}}
}

func (f *fakeWeigher) Weigh(_ context.Context, reporterID int64) (ReporterWeight, error) {
	if w, ok := f.weights[reporterID]; ok {
		return w, nil
	}
	return f.def, nil
}

func (f *fakeWeigher) set(reporterID int64, w ReporterWeight) { f.weights[reporterID] = w }

// registerKind inserts a subject-kind registry row (with an optional callback).
func registerKind(t *testing.T, site, key string, callbackURL, secret *string) {
	t.Helper()
	kind := model.TrustSubjectKind{Site: site, Key: key, CallbackURL: callbackURL, CallbackSecret: secret, IsDeprecated: false}
	if err := testDB.Create(&kind).Error; err != nil {
		t.Fatalf("register kind %s/%s: %v", site, key, err)
	}
}

// reasonID looks up the global reason id for a key (seeded by migrate).
func reasonID(t *testing.T, key string) int64 {
	t.Helper()
	var id int64
	if err := testDB.Raw(`SELECT id FROM trust_report_reason WHERE key = ? AND site IS NULL`, key).Scan(&id).Error; err != nil || id == 0 {
		t.Fatalf("reason %s: id=%d err=%v", key, id, err)
	}
	return id
}

// countReports returns how many report rows exist for a subject.
func countReports(t *testing.T, site, kind, subject string) int64 {
	t.Helper()
	var n int64
	if err := testDB.Model(&model.TrustReport{}).
		Where("site = ? AND subject_kind = ? AND subject_id = ?", site, kind, subject).
		Count(&n).Error; err != nil {
		t.Fatalf("count reports: %v", err)
	}
	return n
}

// countOpenItems returns how many OPEN (pending/claimed) items exist for a subject.
func countOpenItems(t *testing.T, site, kind, subject string) int64 {
	t.Helper()
	var n int64
	if err := testDB.Model(&model.TrustReviewItem{}).
		Where("site = ? AND subject_kind = ? AND subject_id = ? AND status IN ?",
			site, kind, subject, []int16{model.ReviewStatusPending, model.ReviewStatusClaimed}).
		Count(&n).Error; err != nil {
		t.Fatalf("count open items: %v", err)
	}
	return n
}

// getItem reloads a review item.
func getItem(t *testing.T, id int64) *model.TrustReviewItem {
	t.Helper()
	var it model.TrustReviewItem
	if err := testDB.Take(&it, id).Error; err != nil {
		t.Fatalf("reload item %d: %v", id, err)
	}
	return &it
}
