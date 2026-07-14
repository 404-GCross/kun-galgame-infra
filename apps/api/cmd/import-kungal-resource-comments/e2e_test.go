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
// scratch tables (galgame_*) live in a dedicated `res_import_e2e_src` schema
// reached through a SECOND connection whose search_path is pinned to it — so
// `src` and `tgt` are genuinely two handles (the cross-database code path) and
// the source table names never collide with the sibling importer's scratch
// schema. The suite advisory lock serializes against the community test packages
// that TRUNCATE the same target tables.

const (
	e2eSite      = "kungal_res_e2e"
	e2eSrcSchema = "res_import_e2e_src"
)

var (
	testDB *gorm.DB // target (public)
	srcDB  *gorm.DB // source (search_path = res_import_e2e_src)
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
		// Entity tables carrying comment_count: galgame_rating proves ruling 21
		// (its counter is NEVER touched); galgame_website is the one that resets.
		`CREATE TABLE IF NOT EXISTS galgame_rating (id int PRIMARY KEY, comment_count int NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS galgame_website (id int PRIMARY KEY, comment_count int NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS galgame_rating_comment (
			id int PRIMARY KEY, content varchar(1314) NOT NULL, galgame_rating_id int NOT NULL, user_id int NOT NULL,
			target_user_id int, created timestamptz NOT NULL DEFAULT now(), updated timestamptz NOT NULL DEFAULT now())`,
		`CREATE TABLE IF NOT EXISTS galgame_website_comment (
			id int PRIMARY KEY, content text NOT NULL, edited timestamptz, user_id int NOT NULL,
			website_id int NOT NULL, parent_id int,
			created timestamptz NOT NULL DEFAULT now(), updated timestamptz NOT NULL DEFAULT now())`,
		`CREATE TABLE IF NOT EXISTS galgame_toolset_comment (
			id int PRIMARY KEY, content text NOT NULL, edited timestamptz, user_id int NOT NULL,
			toolset_id int NOT NULL, parent_id int,
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
	for _, tbl := range []string{"galgame_rating_comment", "galgame_website_comment", "galgame_toolset_comment", "galgame_rating", "galgame_website"} {
		if err := srcDB.Exec("TRUNCATE " + tbl + " RESTART IDENTITY CASCADE").Error; err != nil {
			t.Fatalf("truncate %s: %v", tbl, err)
		}
	}
	_ = srcDB.Exec("DROP TABLE IF EXISTS resource_comment_community_map").Error
}

var e2eBase = time.Date(2023, 5, 1, 12, 0, 0, 0, time.UTC)

func at(sec int) time.Time { return e2eBase.Add(time.Duration(sec) * time.Second) }

// seedFixture plants one anchor per source:
//   - rating 100: 3 flat comments, one self-targeted (author == target).
//   - website 200: a depth-4 chain (root <- reply <- reply-of-reply <- reply),
//     with an edited row.
//   - toolset 300: a root with two sibling replies (author 2001 shared with
//     website 200, proving trust dedup across sources).
//
// galgame_rating(100).comment_count starts stale (999) and must survive; only
// galgame_website(200).comment_count is reset. Author 1002 is pre-seeded in
// community_trust to prove insert-if-absent never mutates it.
func seedFixture(t *testing.T) {
	t.Helper()
	srcExec := func(q string, args ...any) {
		if err := srcDB.Exec(q, args...).Error; err != nil {
			t.Fatalf("seed src %q: %v", q, err)
		}
	}
	srcExec(`INSERT INTO galgame_rating (id, comment_count) VALUES (100, 999)`)
	srcExec(`INSERT INTO galgame_website (id, comment_count) VALUES (200, 0)`)

	rc := `INSERT INTO galgame_rating_comment (id, content, galgame_rating_id, user_id, target_user_id, created, updated) VALUES (?,?,?,?,?,?,?)`
	srcExec(rc, 1, "r-one", 100, 1001, 1002, at(1), at(1))
	srcExec(rc, 2, "r-self", 100, 1002, 1002, at(2), at(2)) // self-target
	srcExec(rc, 3, "r-three", 100, 1003, 1001, at(3), at(3))

	wc := `INSERT INTO galgame_website_comment (id, content, edited, user_id, website_id, parent_id, created, updated) VALUES (?,?,?,?,?,?,?,?)`
	srcExec(wc, 1, "w-root", nil, 2001, 200, nil, at(1), at(1))
	srcExec(wc, 2, "w-d2", at(20), 2002, 200, 1, at(2), at(20)) // edited
	srcExec(wc, 3, "w-d3", nil, 2003, 200, 2, at(3), at(3))
	srcExec(wc, 4, "w-d4", nil, 2001, 200, 3, at(4), at(4)) // depth 4

	tc := `INSERT INTO galgame_toolset_comment (id, content, edited, user_id, toolset_id, parent_id, created, updated) VALUES (?,?,?,?,?,?,?,?)`
	srcExec(tc, 1, "t-root", nil, 2001, 300, nil, at(1), at(1))
	srcExec(tc, 2, "t-r1", nil, 3002, 300, 1, at(2), at(2))
	srcExec(tc, 3, "t-r2", nil, 3003, 300, 1, at(3), at(3)) // sibling reply to root

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
	assertDry(t, dry)
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
	assertDry(t, rep) // apply reports the same to-do numbers on a clean target

	assertRating(t)
	assertWebsiteDeepChain(t)
	assertToolset(t)
	assertCountersAndTrust(t)
	assertMap(t)

	// ── idempotent rerun: zero new rows ──
	rep2, err := run(srcDB, testDB, e2eSite, true)
	if err != nil {
		t.Fatalf("rerun: %v", err)
	}
	for _, s := range rep2.Sources {
		if s.ThreadsToCreate != 0 || s.PostsToInsert != 0 || s.MapRowsToWrite != 0 {
			t.Fatalf("rerun %s should be a no-op: %+v", s.Name, s)
		}
	}
	if rep2.TrustSeedAbsent != 0 || rep2.TrustSeedPresent != 8 {
		t.Fatalf("rerun trust: absent=%d present=%d want 0/8", rep2.TrustSeedAbsent, rep2.TrustSeedPresent)
	}
	assertRowCounts(t, 3, 10, 10)

	// ── resume: drop the website ledger tail; posts remain, rerun reconciles ──
	if err := srcDB.Exec(`DELETE FROM resource_comment_community_map WHERE source = 'website' AND old_id IN (3, 4)`).Error; err != nil {
		t.Fatalf("truncate ledger tail: %v", err)
	}
	rep3, err := run(srcDB, testDB, e2eSite, true)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	web := rep3.source("website")
	if web.PostsToInsert != 0 {
		t.Fatalf("resume must NOT re-insert posts, got PostsToInsert=%d", web.PostsToInsert)
	}
	if web.MapRowsToWrite != 2 {
		t.Fatalf("resume should backfill 2 ledger rows, got %d", web.MapRowsToWrite)
	}
	assertRowCounts(t, 3, 10, 10) // still exactly 10 posts, 10 ledger rows
}

func assertDry(t *testing.T, r *Report) {
	t.Helper()
	want := map[string]struct{ entities, threads, posts, maps, dangling, self int }{
		"rating":  {1, 1, 3, 3, 0, 1},
		"website": {1, 1, 4, 4, 0, 0},
		"toolset": {1, 1, 3, 3, 0, 0},
	}
	for _, s := range r.Sources {
		w := want[s.Name]
		if s.EntitiesTotal != w.entities || s.ThreadsToCreate != w.threads || s.PostsToInsert != w.posts ||
			s.MapRowsToWrite != w.maps || s.DanglingParents != w.dangling || s.SelfTargets != w.self {
			t.Fatalf("%s report mismatch: %+v want %+v", s.Name, s, w)
		}
	}
	if r.TrustSeedAbsent != 7 || r.TrustSeedPresent != 1 {
		t.Fatalf("trust: absent=%d present=%d want 7/1", r.TrustSeedAbsent, r.TrustSeedPresent)
	}
}

func assertRating(t *testing.T) {
	t.Helper()
	th := loadThread(t, "rating:100")
	if th.Kind != model.ThreadKindComments || th.AnchorKind != model.AnchorKindSiteResource ||
		th.ContentRating != model.ContentRatingAll || th.Status != model.ThreadStatusOpen || th.Title != nil {
		t.Fatalf("rating thread shape wrong: %+v", th)
	}
	if th.PostsCount != 3 || th.ParticipantsCount != 3 || th.HighestPostNumber != 3 {
		t.Fatalf("rating counters: posts=%d participants=%d highest=%d", th.PostsCount, th.ParticipantsCount, th.HighestPostNumber)
	}
	if th.CreatedBy != 1001 || !th.CreatedAt.Equal(at(1)) || th.LastPostedAt == nil || !th.LastPostedAt.Equal(at(3)) {
		t.Fatalf("rating backdating: createdBy=%d createdAt=%v last=%v", th.CreatedBy, th.CreatedAt.UTC(), th.LastPostedAt)
	}
	byNum := postsByNumber(t, th.ID)
	// Flat: all reply/root NULL; target copied verbatim (self-target kept).
	for n, wantTarget := range map[int32]int64{1: 1002, 2: 1002, 3: 1001} {
		p := byNum[n]
		if p.ReplyToPostID != nil || p.RootPostID != nil {
			t.Fatalf("rating post %d must be flat, got reply=%v root=%v", n, p.ReplyToPostID, p.RootPostID)
		}
		if deref64(p.TargetUserID) != wantTarget {
			t.Fatalf("rating post %d target=%v want %d", n, p.TargetUserID, wantTarget)
		}
		if p.SanitizerVersion != 1 || p.ContentHTML == "" || p.Status != model.PostStatusVisible {
			t.Fatalf("rating post %d cook/status: version=%d html=%q status=%d", n, p.SanitizerVersion, p.ContentHTML, p.Status)
		}
	}
}

func assertWebsiteDeepChain(t *testing.T) {
	t.Helper()
	th := loadThread(t, "website:200")
	byNum := postsByNumber(t, th.ID)
	p1, p2, p3, p4 := byNum[1], byNum[2], byNum[3], byNum[4]

	if p1.ReplyToPostID != nil || p1.RootPostID != nil {
		t.Fatalf("website root: reply=%v root=%v want nil/nil", p1.ReplyToPostID, p1.RootPostID)
	}
	// Every descendant re-roots to p1 (the top ancestor).
	if deref64(p2.RootPostID) != p1.ID || deref64(p3.RootPostID) != p1.ID || deref64(p4.RootPostID) != p1.ID {
		t.Fatalf("website re-root: p2=%v p3=%v p4=%v want all %d", p2.RootPostID, p3.RootPostID, p4.RootPostID, p1.ID)
	}
	// reply_to is the immediate parent; target is the parent's author.
	if deref64(p2.ReplyToPostID) != p1.ID || deref64(p2.TargetUserID) != 2001 {
		t.Fatalf("website p2: reply=%v target=%v want %d/2001", p2.ReplyToPostID, p2.TargetUserID, p1.ID)
	}
	if deref64(p3.ReplyToPostID) != p2.ID || deref64(p3.TargetUserID) != 2002 {
		t.Fatalf("website p3: reply=%v target=%v want %d/2002", p3.ReplyToPostID, p3.TargetUserID, p2.ID)
	}
	if deref64(p4.ReplyToPostID) != p3.ID || deref64(p4.TargetUserID) != 2003 {
		t.Fatalf("website p4 (depth 4): reply=%v target=%v want %d/2003", p4.ReplyToPostID, p4.TargetUserID, p3.ID)
	}
	if p2.EditedAt == nil || !p2.EditedAt.Equal(at(20)) {
		t.Fatalf("website p2 edited_at=%v want %v", p2.EditedAt, at(20))
	}
}

func assertToolset(t *testing.T) {
	t.Helper()
	th := loadThread(t, "toolset:300")
	byNum := postsByNumber(t, th.ID)
	p1, p2, p3 := byNum[1], byNum[2], byNum[3]
	if p1.ReplyToPostID != nil || p1.RootPostID != nil {
		t.Fatalf("toolset root pointers not nil: %+v", p1)
	}
	// Two siblings reply to the same root; both re-root to p1, target its author.
	for _, sib := range []model.CommunityPost{p2, p3} {
		if deref64(sib.ReplyToPostID) != p1.ID || deref64(sib.RootPostID) != p1.ID || deref64(sib.TargetUserID) != 2001 {
			t.Fatalf("toolset sibling reply=%v root=%v target=%v want %d/%d/2001", sib.ReplyToPostID, sib.RootPostID, sib.TargetUserID, p1.ID, p1.ID)
		}
	}
}

func assertCountersAndTrust(t *testing.T) {
	t.Helper()
	// Only galgame_website reset; galgame_rating left stale (ruling 21).
	var ccWebsite, ccRating int
	srcDB.Raw("SELECT comment_count FROM galgame_website WHERE id = 200").Scan(&ccWebsite)
	srcDB.Raw("SELECT comment_count FROM galgame_rating WHERE id = 100").Scan(&ccRating)
	if ccWebsite != 4 {
		t.Fatalf("galgame_website.comment_count=%d want 4", ccWebsite)
	}
	if ccRating != 999 {
		t.Fatalf("galgame_rating.comment_count=%d want 999 (must NOT be touched)", ccRating)
	}

	// Pre-existing trust row (1002) untouched.
	var pre model.CommunityTrust
	if err := testDB.Where("user_id = 1002").First(&pre).Error; err != nil {
		t.Fatalf("load trust 1002: %v", err)
	}
	if pre.Level != 3 || pre.FirstPostsHeldRemaining != 5 {
		t.Fatalf("pre-existing trust MUTATED: level=%d hold=%d want 3/5", pre.Level, pre.FirstPostsHeldRemaining)
	}
	// Seeded author (2001, shared across website+toolset) gets level=1, hold=0 once.
	var seeded model.CommunityTrust
	if err := testDB.Where("user_id = 2001").First(&seeded).Error; err != nil {
		t.Fatalf("load trust 2001: %v", err)
	}
	if seeded.Level != model.TrustLevelBasic || seeded.FirstPostsHeldRemaining != 0 {
		t.Fatalf("seeded trust: level=%d hold=%d want 1/0", seeded.Level, seeded.FirstPostsHeldRemaining)
	}
	var n int64
	testDB.Model(&model.CommunityTrust{}).Count(&n)
	if n != 8 { // 1001,1002,1003,2001,2002,2003,3002,3003
		t.Fatalf("trust rows=%d want 8", n)
	}
}

func assertMap(t *testing.T) {
	t.Helper()
	for _, tc := range []struct {
		source string
		want   int64
	}{{"rating", 3}, {"website", 4}, {"toolset", 3}} {
		var n int64
		srcDB.Table("resource_comment_community_map").Where("source = ?", tc.source).Count(&n)
		if n != tc.want {
			t.Fatalf("map[%s]=%d want %d", tc.source, n, tc.want)
		}
	}
	// A specific website ledger row points at the right thread + post.
	th := loadThread(t, "website:200")
	byNum := postsByNumber(t, th.ID)
	var mrow resourceMap
	if err := srcDB.Where("source = 'website' AND old_id = 4").First(&mrow).Error; err != nil {
		t.Fatalf("load map[website,4]: %v", err)
	}
	if mrow.ThreadID != th.ID || mrow.PostID != byNum[4].ID {
		t.Fatalf("map[website,4]=%+v want thread=%d post=%d", mrow, th.ID, byNum[4].ID)
	}
}

func assertRowCounts(t *testing.T, threads, posts, maps int64) {
	t.Helper()
	var th, po, mp int64
	testDB.Model(&model.CommunityThread{}).Where("site = ?", e2eSite).Count(&th)
	testDB.Model(&model.CommunityPost{}).Count(&po)
	srcDB.Table("resource_comment_community_map").Count(&mp)
	if th != threads || po != posts || mp != maps {
		t.Fatalf("row counts: threads=%d posts=%d maps=%d want %d/%d/%d", th, po, mp, threads, posts, maps)
	}
}

func loadThread(t *testing.T, anchorID string) model.CommunityThread {
	t.Helper()
	var th model.CommunityThread
	if err := testDB.Where("site = ? AND anchor_kind = ? AND anchor_id = ?",
		e2eSite, model.AnchorKindSiteResource, anchorID).First(&th).Error; err != nil {
		t.Fatalf("load thread %s: %v", anchorID, err)
	}
	return th
}

func postsByNumber(t *testing.T, threadID int64) map[int32]model.CommunityPost {
	t.Helper()
	var posts []model.CommunityPost
	if err := testDB.Where("thread_id = ?", threadID).Order("post_number").Find(&posts).Error; err != nil {
		t.Fatalf("load posts: %v", err)
	}
	byNum := make(map[int32]model.CommunityPost, len(posts))
	for _, p := range posts {
		byNum[p.PostNumber] = p
	}
	return byNum
}
