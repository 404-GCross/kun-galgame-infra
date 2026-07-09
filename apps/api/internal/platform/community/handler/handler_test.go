package handler

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"api/internal/platform/community/dbtest"
	"api/internal/platform/community/dto"
	"api/internal/platform/community/migrate"
	"api/internal/platform/community/service"
	siteModel "api/internal/platform/site/model"

	"github.com/gofiber/fiber/v3"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

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
	// Serialize against the sibling community test packages sharing this DB.
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

// TestSpecExport is the spec smoke: Setup with nil deps (the gen-openapi path)
// must produce a valid OpenAPI document with the embed face operations. This is
// exactly what cmd/gen-openapi -community serializes.
func TestSpecExport(t *testing.T) {
	api := Setup(fiber.New(), nil, nil, nil, nil, nil, nil, nil)
	b, err := api.OpenAPI().YAML()
	if err != nil {
		t.Fatalf("marshal spec: %v", err)
	}
	spec := string(b)
	for _, want := range []string{
		"/api/v1/community/comments/resolve",
		"/api/v1/community/threads",
		"/api/v1/community/topics",
		"/api/v1/community/trust/activity",
		"/api/v1/community/review",
		"operationId: reply",
		"operationId: editPost",
		"operationId: deletePost",
		"operationId: submitFlag",
		"operationId: recordActivity",
		"operationId: approveReview",
		"name: anchor_id",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("spec missing %q", want)
		}
	}
}

// clientCtx returns a context carrying an authenticated client bound to a site,
// as S2SBridge would set it — so the handler methods can be exercised directly
// without booting the Fiber/auth stack.
func clientCtx(site string) context.Context {
	return context.WithValue(context.Background(), ctxKeyClient, &siteModel.OAuthClient{ID: site, CatalogSite: site})
}

// TestHandlerFlow exercises the handler → service → DB path and the envelope
// mapping for the core embed capabilities.
func TestHandlerFlow(t *testing.T) {
	cleanTables(t)
	sink := service.NoopSink{}
	s := &Server{
		threads:   service.NewThreadService(testDB, sink),
		posts:     service.NewPostService(testDB, sink),
		reactions: service.NewReactionService(testDB),
		feedback:  service.NewFeedbackService(testDB, sink),
		flags:     service.NewFlagService(testDB, sink),
		trust:     service.NewTrustService(testDB),
		review:    service.NewReviewService(testDB),
	}
	ctx := clientCtx("letmoe")

	// Open a topic.
	topicOut, err := s.openTopic(ctx, &openTopicInput{Body: dto.OpenTopicRequest{
		AuthorID: 100, AnchorID: "1", Title: "hello", ContentRating: 0, Body: "opening **post**",
	}})
	if err != nil {
		t.Fatalf("openTopic: %v", err)
	}
	if topicOut.Body.Code != 0 || topicOut.Body.Data.Thread.ID == 0 || topicOut.Body.Data.Post == nil || topicOut.Body.Data.Post.PostNumber != 1 {
		t.Fatalf("unexpected openTopic envelope: %+v", topicOut.Body)
	}
	threadID := topicOut.Body.Data.Thread.ID

	// Reply, then read the thread back with its posts.
	if _, err := s.reply(ctx, &replyInput{ID: threadID, Body: dto.ReplyRequest{AuthorID: 200, Body: "a reply"}}); err != nil {
		t.Fatalf("reply: %v", err)
	}
	detail, err := s.getThread(ctx, &threadPostsInput{ID: threadID})
	if err != nil {
		t.Fatalf("getThread: %v", err)
	}
	if detail.Body.Data.Thread.PostsCount != 2 || len(detail.Body.Data.Posts) != 2 {
		t.Fatalf("thread should have 2 posts: count=%d len=%d", detail.Body.Data.Thread.PostsCount, len(detail.Body.Data.Posts))
	}
	// Cooked HTML is what the embed renders; raw markdown is preserved.
	if !strings.Contains(detail.Body.Data.Posts[0].ContentHTML, "<strong>post</strong>") {
		t.Fatalf("opening post should be cooked: %s", detail.Body.Data.Posts[0].ContentHTML)
	}

	// Comments resolve is idempotent per anchor.
	c1, err := s.resolveComments(ctx, &resolveCommentsInput{Body: dto.CommentsResolveRequest{AnchorKind: 1, AnchorID: "g9"}})
	if err != nil {
		t.Fatalf("resolveComments: %v", err)
	}
	c2, _ := s.resolveComments(ctx, &resolveCommentsInput{Body: dto.CommentsResolveRequest{AnchorKind: 1, AnchorID: "g9"}})
	if c1.Body.Data.Thread.ID != c2.Body.Data.Thread.ID {
		t.Fatalf("comments resolve not idempotent: %d vs %d", c1.Body.Data.Thread.ID, c2.Body.Data.Thread.ID)
	}

	// The topic shows up in the per-site list.
	list, err := s.listThreads(ctx, &listThreadsInput{Kind: 0})
	if err != nil {
		t.Fatalf("listThreads: %v", err)
	}
	if len(list.Body.Data.Threads) != 1 || list.Body.Data.Threads[0].ID != threadID {
		t.Fatalf("expected the topic in the site list, got %+v", list.Body.Data.Threads)
	}

	// A client with no site binding is refused on a write.
	if _, err := s.openTopic(context.Background(), &openTopicInput{Body: dto.OpenTopicRequest{AuthorID: 1, AnchorID: "1", Body: "x"}}); err == nil {
		t.Fatal("unbound client should be refused")
	}
}

// TestHandlerGapfill exercises the step-05c ops through the handler → service →
// DB path: author edit, author self-delete, and the feedback anchor-filter read.
func TestHandlerGapfill(t *testing.T) {
	cleanTables(t)
	sink := service.NoopSink{}
	s := &Server{
		threads:  service.NewThreadService(testDB, sink),
		posts:    service.NewPostService(testDB, sink),
		feedback: service.NewFeedbackService(testDB, sink),
		trust:    service.NewTrustService(testDB),
	}
	ctx := clientCtx("letmoe")

	// Open a topic, reply as an established author.
	topicOut, err := s.openTopic(ctx, &openTopicInput{Body: dto.OpenTopicRequest{AuthorID: 100, AnchorID: "1", Title: "t", Body: "opening"}})
	if err != nil {
		t.Fatalf("openTopic: %v", err)
	}
	threadID := topicOut.Body.Data.Thread.ID
	// Establish the reply author (TL1, no holds) so the reply is visible and thus
	// editable — a fresh author's first posts are held (default hold budget 2).
	if err := testDB.Exec(
		`INSERT INTO community_trust (user_id, level, first_posts_held_remaining) VALUES (200, 1, 0)
		 ON CONFLICT (user_id) DO UPDATE SET level = 1, first_posts_held_remaining = 0`,
	).Error; err != nil {
		t.Fatalf("seed trust: %v", err)
	}
	replyOut, err := s.reply(ctx, &replyInput{ID: threadID, Body: dto.ReplyRequest{AuthorID: 200, Body: "first"}})
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	postID := replyOut.Body.Data.Post.ID

	// Author edits it → cooked HTML updated, edited_at present.
	editOut, err := s.editPost(ctx, &editPostInput{ID: postID, Body: dto.EditPostRequest{AuthorID: 200, Body: "edited **body**"}})
	if err != nil {
		t.Fatalf("editPost: %v", err)
	}
	if editOut.Body.Data.Post.EditedAt == nil || !strings.Contains(editOut.Body.Data.Post.ContentHTML, "<strong>body</strong>") {
		t.Fatalf("edit not applied: %+v", editOut.Body.Data.Post)
	}

	// A non-author edit is refused (envelope error).
	if _, err := s.editPost(ctx, &editPostInput{ID: postID, Body: dto.EditPostRequest{AuthorID: 999, Body: "hijack"}}); err == nil {
		t.Fatal("a non-author edit should be refused")
	}

	// Author self-deletes it via the query-param actor.
	if _, err := s.deletePost(ctx, &deletePostInput{ID: postID, AuthorID: 200}); err != nil {
		t.Fatalf("deletePost: %v", err)
	}
	detail, _ := s.getThread(ctx, &threadPostsInput{ID: threadID})
	// Both posts still listed (tombstone preserved, no collapse); the reply is now
	// tombstoned.
	if detail.Body.Data.Thread.PostsCount != 2 {
		t.Fatalf("posts_count must not decrement on self-delete: %d", detail.Body.Data.Thread.PostsCount)
	}

	// Feedback anchor-filter read: two anchors, filter returns only the match.
	if _, err := s.openFeedback(ctx, &openFeedbackInput{Body: dto.OpenFeedbackRequest{AuthorID: 100, AnchorKind: 2, AnchorID: "r1", Title: "fb1", Body: "x"}}); err != nil {
		t.Fatalf("openFeedback r1: %v", err)
	}
	if _, err := s.openFeedback(ctx, &openFeedbackInput{Body: dto.OpenFeedbackRequest{AuthorID: 100, AnchorKind: 2, AnchorID: "r2", Title: "fb2", Body: "y"}}); err != nil {
		t.Fatalf("openFeedback r2: %v", err)
	}
	hit, err := s.listThreads(ctx, &listThreadsInput{Kind: 2, AnchorKind: 2, AnchorID: "r1"})
	if err != nil {
		t.Fatalf("listThreads anchor: %v", err)
	}
	if len(hit.Body.Data.Threads) != 1 || hit.Body.Data.Threads[0].AnchorID != "r1" {
		t.Fatalf("anchor filter should return only r1, got %+v", hit.Body.Data.Threads)
	}
	miss, _ := s.listThreads(ctx, &listThreadsInput{Kind: 2, AnchorKind: 2, AnchorID: "r404"})
	if len(miss.Body.Data.Threads) != 0 {
		t.Fatalf("a non-existent anchor should be empty, got %d", len(miss.Body.Data.Threads))
	}
}
