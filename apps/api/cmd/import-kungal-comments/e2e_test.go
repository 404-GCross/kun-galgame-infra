package main

import (
	"fmt"
	"os"
	"testing"
	"time"

	"api/internal/platform/community/dbtest"
	"api/internal/platform/community/migrate"
	"api/internal/platform/community/model"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Hermetic end-to-end against a real Postgres (the community convention):
// TEST_DATABASE_DSN or a local default; a missing/unreachable database SKIPS the
// whole package. The target community tables live in `public`; the source
// scratch tables (galgame*) live in a dedicated `import_e2e_src` schema reached
// through a SECOND connection whose search_path is pinned to it — so `src` and
// `tgt` are genuinely two handles (the cross-database code path) and the source
// table names never collide with whatever else the shared test database holds.
// The suite advisory lock serializes against the sibling community test
// packages that TRUNCATE the same target tables.

const (
	e2eSite      = "kungal_e2e"
	e2eSrcSchema = "import_e2e_src"
)

var (
	testDB *gorm.DB // target (public)
	srcDB  *gorm.DB // source (search_path = import_e2e_src)
)

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
	sqlDB, _ := db.DB()
	release := dbtest.AcquireSuiteLock(sqlDB)
	quit := func(msg string, err error) {
		release()
		fmt.Fprintf(os.Stderr, "SKIP: %s: %v\n", msg, err)
		os.Exit(0)
	}
	if err := migrate.Run(db); err != nil {
		quit("community migration failed", err)
	}
	if err := db.Exec("CREATE SCHEMA IF NOT EXISTS " + e2eSrcSchema).Error; err != nil {
		quit("create source schema failed", err)
	}
	src, err := gorm.Open(postgres.Open(dsn+" search_path="+e2eSrcSchema),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		quit("connect source schema failed", err)
	}
	if err := createSourceTables(src); err != nil {
		quit("create source scratch tables failed", err)
	}
	testDB, srcDB = db, src

	code := m.Run()

	_ = db.Exec("DROP SCHEMA IF EXISTS " + e2eSrcSchema + " CASCADE").Error
	release()
	os.Exit(code)
}

func createSourceTables(db *gorm.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS galgame (id int PRIMARY KEY, comment_count int NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS galgame_comment (
			id int PRIMARY KEY, content varchar NOT NULL, galgame_id int NOT NULL, user_id int NOT NULL,
			parent_comment_id int, root_comment_id int, like_count int NOT NULL DEFAULT 0,
			edited timestamptz, status smallint NOT NULL DEFAULT 0,
			created timestamptz NOT NULL DEFAULT now(), updated timestamptz NOT NULL DEFAULT now())`,
		`CREATE TABLE IF NOT EXISTS galgame_comment_like (
			id int PRIMARY KEY, user_id int NOT NULL, galgame_comment_id int NOT NULL,
			created timestamptz NOT NULL DEFAULT now(), updated timestamptz NOT NULL DEFAULT now())`,
	}
	for _, s := range stmts {
		if err := db.Exec(s).Error; err != nil {
			return err
		}
	}
	return nil
}

func resetFixtureTables(t *testing.T) {
	t.Helper()
	for _, tbl := range []string{"community_reaction", "community_trust", "community_post", "community_thread"} {
		if err := testDB.Exec("TRUNCATE " + tbl + " RESTART IDENTITY CASCADE").Error; err != nil {
			t.Fatalf("truncate %s: %v", tbl, err)
		}
	}
	for _, tbl := range []string{"galgame_comment_like", "galgame_comment", "galgame"} {
		if err := srcDB.Exec("TRUNCATE " + tbl + " RESTART IDENTITY CASCADE").Error; err != nil {
			t.Fatalf("truncate %s: %v", tbl, err)
		}
	}
	_ = srcDB.Exec("DROP TABLE IF EXISTS galgame_comment_community_map").Error
}

var e2eBase = time.Date(2023, 5, 1, 12, 0, 0, 0, time.UTC)

func at(sec int) time.Time { return e2eBase.Add(time.Duration(sec) * time.Second) }

// seedFixture plants two games. Game 100 is a 5-post tree with a reply-of-reply
// (non-root parent pointer), an edited reply, and a hidden (status=1) post; game
// 200 is a lone comment. Three likes land on game 100. One author (1002) is
// pre-seeded in community_trust to prove insert-if-absent never touches it.
func seedFixture(t *testing.T) {
	t.Helper()
	srcExec := func(q string, args ...any) {
		if err := srcDB.Exec(q, args...).Error; err != nil {
			t.Fatalf("seed src %q: %v", q, err)
		}
	}
	srcExec(`INSERT INTO galgame (id, comment_count) VALUES (100, 0), (200, 0)`)

	ci := `INSERT INTO galgame_comment
		(id, content, galgame_id, user_id, parent_comment_id, root_comment_id, status, edited, created, updated)
		VALUES (?,?,?,?,?,?,?,?,?,?)`
	srcExec(ci, 1, "hello", 100, 1001, nil, nil, 0, nil, at(1), at(1))
	srcExec(ci, 2, "world", 100, 1002, nil, nil, 0, nil, at(2), at(2))
	srcExec(ci, 3, "reply to 1", 100, 1003, 1, 1, 0, at(10), at(3), at(10))
	srcExec(ci, 4, "reply to 3", 100, 1001, 3, 1, 0, nil, at(4), at(4)) // non-root parent
	srcExec(ci, 5, "hidden one", 100, 1004, nil, nil, 1, nil, at(5), at(5))
	srcExec(ci, 6, "solo", 200, 1005, nil, nil, 0, nil, at(6), at(6))

	li := `INSERT INTO galgame_comment_like (id, user_id, galgame_comment_id, created, updated) VALUES (?,?,?,?,?)`
	srcExec(li, 1, 1002, 1, at(7), at(7))
	srcExec(li, 2, 1003, 1, at(8), at(8))
	srcExec(li, 3, 1001, 3, at(9), at(9))

	// Pre-existing trust row that must survive the import untouched.
	if err := testDB.Exec(`INSERT INTO community_trust (user_id, level, first_posts_held_remaining, updated_at) VALUES (1002, 3, 5, now())`).Error; err != nil {
		t.Fatalf("seed trust: %v", err)
	}
}

func TestEndToEnd(t *testing.T) {
	resetFixtureTables(t)
	seedFixture(t)

	// ── dry run first: nothing written, numbers match the fixture ──
	dry, err := run(srcDB, testDB, e2eSite, false)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if dry.GamesTotal != 2 || dry.ThreadsToCreate != 2 || dry.PostsToInsert != 6 || dry.ReactionsToInsert != 3 {
		t.Fatalf("dry counts: %+v", dry)
	}
	if dry.TrustSeedAbsent != 4 || dry.TrustSeedPresent != 1 || dry.MapRowsToWrite != 6 {
		t.Fatalf("dry trust/map: %+v", dry)
	}
	if dry.Status1Rows != 1 || dry.DanglingParents != 0 || dry.OrphanLikes != 0 || dry.OverLenRows != 0 {
		t.Fatalf("dry anomalies: %+v", dry)
	}
	var postCountAfterDry int64
	testDB.Model(&model.CommunityPost{}).Count(&postCountAfterDry)
	if postCountAfterDry != 0 {
		t.Fatalf("dry run wrote %d posts", postCountAfterDry)
	}

	// ── apply ──
	rep, err := run(srcDB, testDB, e2eSite, true)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if rep.ThreadsToCreate != 2 || rep.PostsToInsert != 6 || rep.ReactionsToInsert != 3 ||
		rep.TrustSeedAbsent != 4 || rep.MapRowsToWrite != 6 {
		t.Fatalf("apply counts: %+v", rep)
	}

	assertGame100(t)
	assertReactions(t)
	assertMapAndCommentCount(t)
	assertTrust(t)

	// ── idempotent rerun: zero new rows ──
	rep2, err := run(srcDB, testDB, e2eSite, true)
	if err != nil {
		t.Fatalf("rerun: %v", err)
	}
	if rep2.ThreadsToCreate != 0 || rep2.PostsToInsert != 0 || rep2.ReactionsToInsert != 0 ||
		rep2.TrustSeedAbsent != 0 || rep2.MapRowsToWrite != 0 {
		t.Fatalf("rerun should be a no-op: %+v", rep2)
	}
	if rep2.PostsExisting != 6 || rep2.TrustSeedPresent != 5 || rep2.ThreadsExisting != 2 {
		t.Fatalf("rerun existing counts: %+v", rep2)
	}
	assertRowCounts(t, 2, 6, 3, 6)

	// ── resume: drop the tail of the ledger, posts remain; rerun reconciles ──
	if err := srcDB.Exec(`DELETE FROM galgame_comment_community_map WHERE old_comment_id IN (5, 6)`).Error; err != nil {
		t.Fatalf("truncate ledger tail: %v", err)
	}
	rep3, err := run(srcDB, testDB, e2eSite, true)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if rep3.PostsToInsert != 0 {
		t.Fatalf("resume must NOT re-insert posts, got PostsToInsert=%d", rep3.PostsToInsert)
	}
	if rep3.MapRowsToWrite != 2 {
		t.Fatalf("resume should backfill 2 ledger rows, got %d", rep3.MapRowsToWrite)
	}
	assertRowCounts(t, 2, 6, 3, 6) // still exactly 6 posts, 6 ledger rows
}

func assertGame100(t *testing.T) {
	t.Helper()
	var th model.CommunityThread
	if err := testDB.Where("site = ? AND anchor_id = ?", e2eSite, "100").First(&th).Error; err != nil {
		t.Fatalf("load thread 100: %v", err)
	}
	if th.Kind != model.ThreadKindComments || th.AnchorKind != model.AnchorKindSiteGame ||
		th.ContentRating != model.ContentRatingAll || th.Status != model.ThreadStatusOpen || th.Title != nil {
		t.Fatalf("thread shape wrong: %+v", th)
	}
	if th.PostsCount != 5 || th.ParticipantsCount != 4 || th.HighestPostNumber != 5 {
		t.Fatalf("counters: posts=%d participants=%d highest=%d", th.PostsCount, th.ParticipantsCount, th.HighestPostNumber)
	}
	if th.CreatedBy != 1001 || !th.CreatedAt.Equal(at(1)) {
		t.Fatalf("thread backdating: createdBy=%d createdAt=%v want 1001/%v", th.CreatedBy, th.CreatedAt.UTC(), at(1))
	}
	if th.LastPostedAt == nil || !th.LastPostedAt.Equal(at(5)) {
		t.Fatalf("last_posted_at=%v want %v", th.LastPostedAt, at(5))
	}

	posts := loadPosts(t, th.ID)
	if len(posts) != 5 {
		t.Fatalf("post count=%d want 5", len(posts))
	}
	// post_number is the (created,id) rank: c1..c5 -> 1..5.
	byNum := map[int32]model.CommunityPost{}
	for _, p := range posts {
		byNum[p.PostNumber] = p
	}
	p1, p3, p4, p5 := byNum[1], byNum[3], byNum[4], byNum[5]

	if p1.ReplyToPostID != nil || p1.RootPostID != nil || !p1.CreatedAt.Equal(at(1)) {
		t.Fatalf("post1 (top-level): reply=%v root=%v created=%v", p1.ReplyToPostID, p1.RootPostID, p1.CreatedAt.UTC())
	}
	if p1.SanitizerVersion != 1 || p1.ContentHTML == "" {
		t.Fatalf("post1 cook: version=%d html=%q", p1.SanitizerVersion, p1.ContentHTML)
	}
	// post3 replies to post1, root = post1.
	if deref64(p3.ReplyToPostID) != p1.ID || deref64(p3.RootPostID) != p1.ID {
		t.Fatalf("post3 pointers: reply=%v root=%v want %d/%d", p3.ReplyToPostID, p3.RootPostID, p1.ID, p1.ID)
	}
	if p3.EditedAt == nil || !p3.EditedAt.Equal(at(10)) {
		t.Fatalf("post3 edited_at=%v want %v", p3.EditedAt, at(10))
	}
	// post4 replies to post3 (non-root parent), root still = post1.
	if deref64(p4.ReplyToPostID) != p3.ID || deref64(p4.RootPostID) != p1.ID {
		t.Fatalf("post4 (reply-of-reply): reply=%v root=%v want %d/%d", p4.ReplyToPostID, p4.RootPostID, p3.ID, p1.ID)
	}
	if p5.Status != model.PostStatusHidden {
		t.Fatalf("post5 status=%d want hidden(1)", p5.Status)
	}
}

func assertReactions(t *testing.T) {
	t.Helper()
	var n int64
	testDB.Model(&model.CommunityReaction{}).Count(&n)
	if n != 3 {
		t.Fatalf("reactions=%d want 3", n)
	}
	// The like by 1002 on comment 1 must be backdated to at(7).
	var r model.CommunityReaction
	if err := testDB.Where("user_id = 1002").First(&r).Error; err != nil {
		t.Fatalf("load reaction 1002: %v", err)
	}
	if r.Kind != model.ReactionKindLike || !r.CreatedAt.Equal(at(7)) {
		t.Fatalf("reaction backdating: kind=%d created=%v want 0/%v", r.Kind, r.CreatedAt.UTC(), at(7))
	}
}

func assertMapAndCommentCount(t *testing.T) {
	t.Helper()
	var maps []commentMap
	if err := srcDB.Order("old_comment_id").Find(&maps).Error; err != nil {
		t.Fatalf("load map: %v", err)
	}
	if len(maps) != 6 {
		t.Fatalf("map rows=%d want 6", len(maps))
	}
	// Verify comment 3's ledger row points at the right thread + post.
	var th model.CommunityThread
	testDB.Where("site = ? AND anchor_id = ?", e2eSite, "100").First(&th)
	var p3 model.CommunityPost
	testDB.Where("thread_id = ? AND post_number = 3", th.ID).First(&p3)
	for _, mrow := range maps {
		if mrow.OldCommentID == 3 {
			if mrow.ThreadID != th.ID || mrow.PostID != p3.ID || mrow.GalgameID != 100 {
				t.Fatalf("map[3]=%+v want thread=%d post=%d game=100", mrow, th.ID, p3.ID)
			}
		}
	}
	// comment_count reset: game 100 = 4 visible (c5 hidden), game 200 = 1.
	var cc100, cc200 int
	srcDB.Raw("SELECT comment_count FROM galgame WHERE id = 100").Scan(&cc100)
	srcDB.Raw("SELECT comment_count FROM galgame WHERE id = 200").Scan(&cc200)
	if cc100 != 4 || cc200 != 1 {
		t.Fatalf("comment_count: game100=%d (want 4) game200=%d (want 1)", cc100, cc200)
	}
}

func assertTrust(t *testing.T) {
	t.Helper()
	// Pre-existing row (1002) untouched.
	var pre model.CommunityTrust
	if err := testDB.Where("user_id = 1002").First(&pre).Error; err != nil {
		t.Fatalf("load trust 1002: %v", err)
	}
	if pre.Level != 3 || pre.FirstPostsHeldRemaining != 5 {
		t.Fatalf("pre-existing trust MUTATED: level=%d hold=%d want 3/5", pre.Level, pre.FirstPostsHeldRemaining)
	}
	// Seeded author (1001) gets level=1, hold=0.
	var seeded model.CommunityTrust
	if err := testDB.Where("user_id = 1001").First(&seeded).Error; err != nil {
		t.Fatalf("load trust 1001: %v", err)
	}
	if seeded.Level != model.TrustLevelBasic || seeded.FirstPostsHeldRemaining != 0 {
		t.Fatalf("seeded trust: level=%d hold=%d want 1/0", seeded.Level, seeded.FirstPostsHeldRemaining)
	}
	var n int64
	testDB.Model(&model.CommunityTrust{}).Count(&n)
	if n != 5 { // 1001..1005
		t.Fatalf("trust rows=%d want 5", n)
	}
}

func assertRowCounts(t *testing.T, threads, posts, reactions, maps int64) {
	t.Helper()
	var th, po, re, mp int64
	testDB.Model(&model.CommunityThread{}).Count(&th)
	testDB.Model(&model.CommunityPost{}).Count(&po)
	testDB.Model(&model.CommunityReaction{}).Count(&re)
	srcDB.Table("galgame_comment_community_map").Count(&mp)
	if th != threads || po != posts || re != reactions || mp != maps {
		t.Fatalf("row counts: threads=%d posts=%d reactions=%d maps=%d want %d/%d/%d/%d",
			th, po, re, mp, threads, posts, reactions, maps)
	}
}

func loadPosts(t *testing.T, threadID int64) []model.CommunityPost {
	t.Helper()
	var posts []model.CommunityPost
	if err := testDB.Where("thread_id = ?", threadID).Order("post_number").Find(&posts).Error; err != nil {
		t.Fatalf("load posts: %v", err)
	}
	return posts
}

func deref64(p *int64) int64 {
	if p == nil {
		return -1
	}
	return *p
}
