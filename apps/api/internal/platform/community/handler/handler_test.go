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
	api := Setup(fiber.New(), nil, nil, nil, nil, nil)
	b, err := api.OpenAPI().YAML()
	if err != nil {
		t.Fatalf("marshal spec: %v", err)
	}
	spec := string(b)
	for _, want := range []string{
		"/api/v1/community/comments/resolve",
		"/api/v1/community/threads",
		"/api/v1/community/topics",
		"operationId: reply",
		"operationId: submitFlag",
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
		flags:     service.NewFlagService(testDB),
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
