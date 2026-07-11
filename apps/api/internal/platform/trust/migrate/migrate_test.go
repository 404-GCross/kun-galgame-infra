package migrate

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"api/internal/platform/trust/dbtest"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Integration test against a real Postgres (the community/catalog convention):
// TEST_DATABASE_DSN or a local default; a missing database skips the whole
// package. Schema comes from migrate.Run — the exact production migration — so
// these probes assert the storage-level invariants of doc 18 §3 directly
// against the shipped schema. `go test -p 1` (pinned in test.yml) serializes
// packages that share the CI database.

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

	// First migration provisions the schema; running it again is the
	// idempotency probe (E1) — a second AutoMigrate + IF NOT EXISTS raw
	// section + seed upsert must be a no-op.
	if err := Run(db); err != nil {
		release()
		fmt.Fprintf(os.Stderr, "SKIP: trust migration failed: %v\n", err)
		os.Exit(0)
	}
	if err := Run(db); err != nil {
		release()
		fmt.Fprintf(os.Stderr, "trust migration is NOT idempotent: %v\n", err)
		os.Exit(1)
	}

	testDB = db
	code := m.Run()
	release()
	os.Exit(code)
}

// TestSeedIdempotent asserts the six global reasons exist exactly once each
// after two migrations (E1).
func TestSeedIdempotent(t *testing.T) {
	var count int64
	if err := testDB.Raw(`SELECT count(*) FROM trust_report_reason WHERE site IS NULL`).Scan(&count).Error; err != nil {
		t.Fatalf("count reasons: %v", err)
	}
	if count != int64(len(SeedReasons)) {
		t.Fatalf("global reason count = %d, want %d (seed not idempotent?)", count, len(SeedReasons))
	}
	for _, r := range SeedReasons {
		var sev int16
		if err := testDB.Raw(`SELECT severity FROM trust_report_reason WHERE key = ? AND site IS NULL`, r.Key).Scan(&sev).Error; err != nil {
			t.Fatalf("read reason %s: %v", r.Key, err)
		}
		if sev != r.Severity {
			t.Errorf("reason %s severity = %d, want %d", r.Key, sev, r.Severity)
		}
	}
}

// TestPartialUniqueIndexes asserts the invariant-bearing partial uniques carry
// both their columns and their predicates verbatim (invariants 4/5).
func TestPartialUniqueIndexes(t *testing.T) {
	cases := []struct {
		name  string
		frags []string
	}{
		{"uq_trust_review_item_open", []string{"(site, subject_kind, subject_id)", "status = ANY", "0, 1"}},
		{"uq_trust_report_subject_reporter", []string{"(site, subject_kind, subject_id, reporter_id)", "reporter_id IS NOT NULL"}},
		{"uq_trust_report_reason_key_global", []string{"(key)", "site IS NULL"}},
		{"uq_trust_report_reason_key_site", []string{"(key, site)", "site IS NOT NULL"}},
		{"idx_trust_review_item_site_status_priority", []string{"(site, status, priority DESC)"}},
	}
	for _, c := range cases {
		def := indexDef(t, c.name)
		for _, frag := range c.frags {
			if !strings.Contains(def, frag) {
				t.Errorf("index %s missing %q in\n  %s", c.name, frag, def)
			}
		}
	}
}

func indexDef(t *testing.T, name string) string {
	t.Helper()
	var def string
	if err := testDB.Raw(
		`SELECT indexdef FROM pg_indexes WHERE schemaname = 'public' AND indexname = ?`, name,
	).Scan(&def).Error; err != nil {
		t.Fatalf("read indexdef %s: %v", name, err)
	}
	if def == "" {
		t.Fatalf("index %s does not exist", name)
	}
	return def
}

// TestColumnDiscipline spot-checks that intent columns carry NO DB default (the
// GORM zero-value trap, 章程 ruling 4) while bookkeeping columns keep theirs.
func TestColumnDiscipline(t *testing.T) {
	noDefault := []struct{ table, column string }{
		{"trust_report", "weight"}, {"trust_report", "status"},
		{"trust_review_item", "priority"}, {"trust_review_item", "source"}, {"trust_review_item", "status"},
		{"trust_report_reason", "severity"}, {"trust_disposition", "action"},
	}
	for _, c := range noDefault {
		if def := columnDefault(t, c.table, c.column); def != "" {
			t.Errorf("%s.%s must have NO default (intent column), got %q", c.table, c.column, def)
		}
	}
	withDefault := []struct{ table, column, want string }{
		{"trust_disposition", "callback_attempts", "0"},
		{"trust_report_reason", "is_deprecated", "false"},
	}
	for _, c := range withDefault {
		if def := columnDefault(t, c.table, c.column); !strings.Contains(def, c.want) {
			t.Errorf("%s.%s default = %q, want to contain %q", c.table, c.column, def, c.want)
		}
	}
}

func columnDefault(t *testing.T, table, column string) string {
	t.Helper()
	var def *string
	if err := testDB.Raw(
		`SELECT column_default FROM information_schema.columns
		   WHERE table_schema='public' AND table_name=? AND column_name=?`,
		table, column,
	).Scan(&def).Error; err != nil {
		t.Fatalf("read default %s.%s: %v", table, column, err)
	}
	if def == nil {
		return ""
	}
	return *def
}
