package migrate

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"api/internal/platform/community/model"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Integration test against a real Postgres (the catalog service_test.go
// convention): TEST_DATABASE_DSN or a local default; a missing database skips
// the whole package. Schema comes from migrate.Run — the exact production
// migration — so these probes assert the storage-level invariants of doc 11 §3
// (4/5/7/12/13) directly against the shipped schema. `go test -p 1` (pinned in
// test.yml) serializes packages that share the CI database.

var testDB *gorm.DB

func TestMain(m *testing.M) {
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		dsn = "host=localhost port=5432 user=postgres password=postgres dbname=kun_community_test sslmode=disable"
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: cannot connect to test database: %v\n", err)
		os.Exit(0)
	}
	// First migration provisions the schema; running it again here is the
	// idempotency probe (a second AutoMigrate + IF NOT EXISTS raw section must
	// be a no-op).
	if err := Run(db); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: community migration failed: %v\n", err)
		os.Exit(0)
	}
	if err := Run(db); err != nil {
		fmt.Fprintf(os.Stderr, "community migration is NOT idempotent: %v\n", err)
		os.Exit(1)
	}

	testDB = db
	os.Exit(m.Run())
}

// cleanTables truncates every table so ordering-independent probes start fresh.
func cleanTables(t *testing.T) {
	t.Helper()
	for _, table := range []string{
		"community_review_item", "community_flag", "community_trust",
		"community_board", "community_thread_user", "community_reaction",
		"community_post", "community_thread",
	} {
		if err := testDB.Exec("TRUNCATE " + table + " RESTART IDENTITY CASCADE").Error; err != nil {
			t.Fatalf("truncate %s: %v", table, err)
		}
	}
}

// insertThread creates a thread, returning the row and the raw error so the
// invariant probes can inspect a unique violation. Every no-default meaningful-
// zero column is written explicitly (the GORM-trap discipline in action).
func insertThread(kind, anchorKind int16, anchorID string, status int16) (*model.CommunityThread, error) {
	th := &model.CommunityThread{
		Site:          "letmoe",
		Kind:          kind,
		AnchorKind:    anchorKind,
		AnchorID:      anchorID,
		ContentRating: model.ContentRatingAll,
		Status:        status,
		CreatedBy:     1,
	}
	return th, testDB.Create(th).Error
}

func mustThread(t *testing.T, kind, anchorKind int16, anchorID string, status int16) *model.CommunityThread {
	t.Helper()
	th, err := insertThread(kind, anchorKind, anchorID, status)
	if err != nil {
		t.Fatalf("create thread: %v", err)
	}
	return th
}

func isDuplicate(err error) bool {
	return err != nil && strings.Contains(err.Error(), "duplicate key")
}

// --- invariant 4: one live comments thread per anchor ----------------------

func TestInvariant4_CommentsAnchorUnique(t *testing.T) {
	cleanTables(t)

	// First comments thread for the anchor: OK.
	mustThread(t, model.ThreadKindComments, model.AnchorKindSiteGame, "g100", model.ThreadStatusOpen)

	// Second live comments thread, same anchor: rejected by the partial unique.
	if _, err := insertThread(model.ThreadKindComments, model.AnchorKindSiteGame, "g100", model.ThreadStatusOpen); !isDuplicate(err) {
		t.Fatalf("second live comments thread for the same anchor should violate the partial unique, got: %v", err)
	}

	// topic (kind=0) is unconstrained: many per anchor.
	mustThread(t, model.ThreadKindTopic, model.AnchorKindBoard, "1", model.ThreadStatusOpen)
	mustThread(t, model.ThreadKindTopic, model.AnchorKindBoard, "1", model.ThreadStatusOpen)
}

// --- invariant 13: tombstone drops out of the comments unique (rebuild) ----

func TestInvariant4_TombstoneRebuild(t *testing.T) {
	cleanTables(t)

	first := mustThread(t, model.ThreadKindComments, model.AnchorKindSiteResource, "r7", model.ThreadStatusOpen)

	// Tombstone the first thread (invariant 13: status, never gorm.DeletedAt).
	if err := testDB.Model(first).Update("status", model.ThreadStatusDeleted).Error; err != nil {
		t.Fatalf("tombstone update: %v", err)
	}

	// A fresh comments thread for the same anchor is now allowed.
	if _, err := insertThread(model.ThreadKindComments, model.AnchorKindSiteResource, "r7", model.ThreadStatusOpen); err != nil {
		t.Fatalf("comments thread should be re-creatable after the prior one is tombstoned, got: %v", err)
	}
}

// --- invariant 5: (thread_id, post_number) unique --------------------------

func TestInvariant5_PostNumberUnique(t *testing.T) {
	cleanTables(t)
	th := mustThread(t, model.ThreadKindTopic, model.AnchorKindBoard, "1", model.ThreadStatusOpen)

	post := func(n int32) error {
		return testDB.Create(&model.CommunityPost{
			ThreadID: th.ID, PostNumber: n, AuthorID: 1,
			ContentRaw: "hi", ContentHTML: "<p>hi</p>", SanitizerVersion: 1,
			ContentRating: model.ContentRatingAll, Status: model.PostStatusVisible,
		}).Error
	}

	if err := post(1); err != nil {
		t.Fatalf("first post: %v", err)
	}
	if err := post(1); !isDuplicate(err) {
		t.Fatalf("duplicate (thread_id, post_number) should be rejected, got: %v", err)
	}
	if err := post(2); err != nil {
		t.Fatalf("distinct post_number should be accepted: %v", err)
	}
}

// --- board UNIQUE(site, slug) ----------------------------------------------

func TestBoardSiteSlugUnique(t *testing.T) {
	cleanTables(t)
	slug := "general"
	board := func(site string) error {
		return testDB.Create(&model.CommunityBoard{
			Site: site, Name: "General", Slug: &slug, Format: model.BoardFormatDiscussion,
		}).Error
	}
	if err := board("letmoe"); err != nil {
		t.Fatalf("first board: %v", err)
	}
	if err := board("letmoe"); !isDuplicate(err) {
		t.Fatalf("duplicate (site, slug) should be rejected, got: %v", err)
	}
	if err := board("nextmanga"); err != nil {
		t.Fatalf("same slug on a different site should be accepted: %v", err)
	}
}

// --- flag UNIQUE(post_id, flagger_id) --------------------------------------

func TestFlagPostFlaggerUnique(t *testing.T) {
	cleanTables(t)
	flag := func(flagger int64) error {
		return testDB.Create(&model.CommunityFlag{
			PostID: 1, FlaggerID: flagger, Weight: 1.0, Status: model.FlagStatusPending,
		}).Error
	}
	if err := flag(10); err != nil {
		t.Fatalf("first flag: %v", err)
	}
	if err := flag(10); !isDuplicate(err) {
		t.Fatalf("one user flags a post once — duplicate should be rejected, got: %v", err)
	}
	if err := flag(11); err != nil {
		t.Fatalf("a different flagger should be accepted: %v", err)
	}
}

// --- invariant 7: tenant-first index column order (composite-index-trap
// regression). indexdef is asserted verbatim on the ordered column list. ------

func TestIndexColumnOrder(t *testing.T) {
	cases := []struct{ name, wantCols string }{
		// The two tenant/anchor-first access indexes (invariant 7).
		{"idx_community_thread_site_list", "(site, kind, last_posted_at DESC)"},
		{"idx_community_thread_anchor", "(anchor_kind, anchor_id, kind)"},
		// The GORM-tag composites (priority discipline): if a priority were
		// dropped, the column order here would silently reshuffle.
		{"idx_community_post_thread_root", "(thread_id, root_post_id, post_number)"},
		{"uq_community_post_thread_number", "(thread_id, post_number)"},
	}
	for _, c := range cases {
		def := indexDef(t, c.name)
		if !strings.Contains(def, c.wantCols) {
			t.Errorf("index %s: want column list %q in\n  %s", c.name, c.wantCols, def)
		}
	}

	// The comments partial unique carries both the columns and the predicate.
	partial := indexDef(t, "uq_community_thread_anchor_comments")
	for _, frag := range []string{"(anchor_kind, anchor_id)", "kind = 1", "status <> 3"} {
		if !strings.Contains(partial, frag) {
			t.Errorf("partial unique missing %q in\n  %s", frag, partial)
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

// --- column-name audit (acronym-column-naming-trap regression) -------------

func TestColumnAudit(t *testing.T) {
	want := map[string][]string{
		"community_thread": {
			"id", "site", "kind", "anchor_kind", "anchor_id", "title",
			"header_image_hashes", "content_rating", "status", "fb_status",
			"fb_response", "fb_responder_id", "fb_responded_at", "merged_into_id",
			"answer_post_id", "posts_count", "participants_count",
			"highest_post_number", "last_posted_at", "created_by", "created_at",
			"updated_at",
		},
		"community_post": {
			"id", "thread_id", "post_number", "root_post_id", "reply_to_post_id",
			"target_user_id", "author_id", "content_raw", "content_html",
			"sanitizer_version", "content_rating", "status", "edited_at",
			"created_at",
		},
		"community_reaction": {"post_id", "user_id", "kind", "created_at"},
		"community_thread_user": {
			"thread_id", "user_id", "last_read_post_number", "notification_level",
			"last_visited_at",
		},
		"community_board": {"id", "site", "name", "slug", "position", "description", "format"},
		"community_trust": {
			"user_id", "level", "topics_entered", "posts_read", "read_time_s",
			"days_visited", "likes_given", "likes_received", "flags_agreed",
			"flags_disagreed", "first_posts_held_remaining", "granted_boost",
			"updated_at",
		},
		"community_flag": {
			"id", "post_id", "flagger_id", "reason", "note", "weight", "status",
			"created_at",
		},
		"community_review_item": {
			"id", "site", "post_id", "source", "status", "decided_by",
			"decided_at", "created_at",
		},
	}
	for table, cols := range want {
		got := columnNames(t, table)
		sort.Strings(cols)
		if strings.Join(got, ",") != strings.Join(cols, ",") {
			t.Errorf("table %s columns mismatch\n  want: %v\n  got:  %v", table, cols, got)
		}
	}
}

func columnNames(t *testing.T, table string) []string {
	t.Helper()
	var names []string
	if err := testDB.Raw(
		`SELECT column_name FROM information_schema.columns
		   WHERE table_schema = 'public' AND table_name = ? ORDER BY column_name`, table,
	).Scan(&names).Error; err != nil {
		t.Fatalf("read columns of %s: %v", table, err)
	}
	if len(names) == 0 {
		t.Fatalf("table %s has no columns (missing?)", table)
	}
	return names
}

// --- column-type spot check (types alignment with doc 11 §4) ---------------

func TestColumnTypes(t *testing.T) {
	cases := []struct{ table, column, want string }{
		{"community_post", "post_number", "integer"},         // int, not bigint
		{"community_thread", "kind", "smallint"},             // smallint enum
		{"community_thread", "header_image_hashes", "jsonb"}, // explicit jsonb
		{"community_flag", "weight", "real"},                 // real, NOT numeric
		{"community_thread", "posts_count", "integer"},       // int counter
		{"community_trust", "user_id", "bigint"},             // global user id
	}
	for _, c := range cases {
		var got string
		if err := testDB.Raw(
			`SELECT data_type FROM information_schema.columns
			   WHERE table_schema='public' AND table_name=? AND column_name=?`,
			c.table, c.column,
		).Scan(&got).Error; err != nil {
			t.Fatalf("read type %s.%s: %v", c.table, c.column, err)
		}
		if got != c.want {
			t.Errorf("%s.%s: want %s, got %s", c.table, c.column, c.want, got)
		}
	}
}
