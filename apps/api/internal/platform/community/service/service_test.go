package service

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"api/internal/platform/community/dbtest"
	"api/internal/platform/community/migrate"
	"api/internal/platform/community/model"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Integration tests against a real Postgres (the catalog service_test.go
// convention): TEST_DATABASE_DSN or a local default; a missing database skips
// the whole package. Schema comes from migrate.Run — the exact production
// migration. `go test -p 1` (pinned in test.yml) serializes packages sharing
// the CI database.

var testDB *gorm.DB

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		dsn = "host=localhost port=5432 user=postgres password=postgres dbname=kun_community_test sslmode=disable"
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: cannot connect to test database: %v\n", err)
		os.Exit(0)
	}
	// Serialize against the sibling community test packages (migrate/handler)
	// that share this database — otherwise parallel `go test` TRUNCATEs race.
	sqlDB, _ := db.DB()
	release := dbtest.AcquireSuiteLock(sqlDB)

	if err := migrate.Run(db); err != nil {
		release()
		fmt.Fprintf(os.Stderr, "SKIP: community migration failed: %v\n", err)
		os.Exit(0)
	}
	testDB = db
	code := m.Run()
	release()
	os.Exit(code)
}

func cleanTables(t *testing.T) {
	t.Helper()
	for _, table := range []string{
		"community_review_item", "community_flag", "community_reaction",
		"community_thread_user", "community_board", "community_trust",
		"community_post", "community_thread",
	} {
		if err := testDB.Exec("TRUNCATE " + table + " RESTART IDENTITY CASCADE").Error; err != nil {
			t.Fatalf("truncate %s: %v", table, err)
		}
	}
}

// recordingSink captures emitted events for assertion.
type recordingSink struct {
	mu     sync.Mutex
	events []Event
}

func (r *recordingSink) Emit(e Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recordingSink) count(kind string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.events {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

// seedTrust upserts a trust row with an explicit hold count — raw SQL because
// first_posts_held_remaining carries a DB default that GORM would omit for a
// zero value.
func seedTrust(t *testing.T, userID int64, level int16, holds int32) {
	t.Helper()
	if err := testDB.Exec(
		`INSERT INTO community_trust (user_id, level, first_posts_held_remaining) VALUES (?, ?, ?)
		 ON CONFLICT (user_id) DO UPDATE SET level = EXCLUDED.level,
		   first_posts_held_remaining = EXCLUDED.first_posts_held_remaining`,
		userID, level, holds,
	).Error; err != nil {
		t.Fatalf("seed trust: %v", err)
	}
}

// getThread reloads a thread for assertions.
func getThread(t *testing.T, id int64) *model.CommunityThread {
	t.Helper()
	var th model.CommunityThread
	if err := testDB.First(&th, id).Error; err != nil {
		t.Fatalf("reload thread %d: %v", id, err)
	}
	return &th
}

// openTopic opens a topic by an established (TL1, no holds) author so the flow
// under test is not perturbed by the sandbox.
func openTopic(t *testing.T, ts *ThreadService, site string, author int64, anchorID, body string) *model.CommunityThread {
	t.Helper()
	seedTrust(t, author, model.TrustLevelBasic, 0)
	th, _, err := ts.OpenTopic(context.Background(), OpenThreadParams{
		Site: site, AuthorID: author, AnchorKind: model.AnchorKindBoard, AnchorID: anchorID,
		Title: "t", ContentRating: model.ContentRatingAll, BodyRaw: body,
	})
	if err != nil {
		t.Fatalf("open topic: %v", err)
	}
	return th
}
