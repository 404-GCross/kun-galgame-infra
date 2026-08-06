package service

import (
	"context"
	"fmt"
	"testing"

	"api/internal/platform/community/model"
)

func TestSandbox_ContentLimits(t *testing.T) {
	cleanTables(t)
	ts := NewThreadService(testDB, NoopSink{})
	ps := NewPostService(testDB, NoopSink{})
	th := openTopic(t, ts, "letmoe", 100, "b1", "opening")

	// Fresh TL0 author (no trust row → TL0). Over the link cap → rejected.
	tooManyLinks := "[a](https://a.com) [b](https://b.com) [c](https://c.com)"
	if _, err := ps.Reply(context.Background(), ReplyParams{ThreadID: th.ID, AuthorID: 201, BodyRaw: tooManyLinks}); !isSandbox(err) {
		t.Fatalf("TL0 over link cap should be a SandboxError, got %v", err)
	}
	// Within the cap → accepted.
	if _, err := ps.Reply(context.Background(), ReplyParams{ThreadID: th.ID, AuthorID: 201, BodyRaw: "[a](https://a.com) [b](https://b.com)"}); err != nil {
		t.Fatalf("TL0 within link cap should pass: %v", err)
	}

	// TL1 author is exempt from the content caps.
	seedTrust(t, 202, model.TrustLevelBasic, 0)
	if _, err := ps.Reply(context.Background(), ReplyParams{ThreadID: th.ID, AuthorID: 202, BodyRaw: tooManyLinks}); err != nil {
		t.Fatalf("TL1 should be exempt from the link cap: %v", err)
	}
}

func TestSandbox_FirstPostHold(t *testing.T) {
	cleanTables(t)
	ts := NewThreadService(testDB, NoopSink{})
	ps := NewPostService(testDB, NoopSink{})
	th := openTopic(t, ts, "letmoe", 100, "b1", "opening")

	// A TL0 user explicitly put on hold for two posts: the first two are held, the
	// third is visible. Wave 07 retired the blanket newcomer hold, so the budget is
	// seeded rather than inherited — the spend-down mechanism under test is
	// unchanged.
	author := int64(500)
	seedTrust(t, author, model.TrustLevelNew, 2)
	p1, err := ps.Reply(context.Background(), ReplyParams{ThreadID: th.ID, AuthorID: author, BodyRaw: "one"})
	if err != nil {
		t.Fatalf("p1: %v", err)
	}
	p2, _ := ps.Reply(context.Background(), ReplyParams{ThreadID: th.ID, AuthorID: author, BodyRaw: "two"})
	p3, _ := ps.Reply(context.Background(), ReplyParams{ThreadID: th.ID, AuthorID: author, BodyRaw: "three"})
	if p1.Status != model.PostStatusHidden || p2.Status != model.PostStatusHidden {
		t.Fatalf("first two posts should be held: p1=%d p2=%d", p1.Status, p2.Status)
	}
	if p3.Status != model.PostStatusVisible {
		t.Fatalf("third post should be visible: p3=%d", p3.Status)
	}
	var holds int32
	testDB.Model(&model.CommunityTrust{}).Where("user_id = ?", author).Select("first_posts_held_remaining").Scan(&holds)
	if holds != 0 {
		t.Fatalf("holds should be spent to 0, got %d", holds)
	}
}

func TestSandbox_DailyLimits(t *testing.T) {
	cleanTables(t)
	ts := NewThreadService(testDB, NoopSink{})
	ps := NewPostService(testDB, NoopSink{})

	// Topic cap: a TL0 user opens 3 topics, the 4th is rejected. (Holds set to 0
	// so the flow is not blocked on the hold path; the cap is what we test.)
	author := int64(600)
	seedTrust(t, author, model.TrustLevelNew, 0)
	for i := range tl0MaxTopicsPerDay {
		if _, _, err := ts.OpenTopic(context.Background(), OpenThreadParams{
			Site: "letmoe", AuthorID: author, AnchorKind: model.AnchorKindBoard, AnchorID: fmt.Sprintf("b%d", i),
			Title: "t", ContentRating: model.ContentRatingAll, BodyRaw: "x",
		}); err != nil {
			t.Fatalf("topic %d should pass: %v", i, err)
		}
	}
	if _, _, err := ts.OpenTopic(context.Background(), OpenThreadParams{
		Site: "letmoe", AuthorID: author, AnchorKind: model.AnchorKindBoard, AnchorID: "over",
		Title: "t", ContentRating: model.ContentRatingAll, BodyRaw: "x",
	}); !isSandbox(err) {
		t.Fatalf("4th topic should hit the daily cap, got %v", err)
	}

	// Reply cap: a TL0 user replies 10 times, the 11th is rejected.
	th := openTopic(t, ts, "letmoe", 100, "host", "opening")
	replier := int64(700)
	seedTrust(t, replier, model.TrustLevelNew, 0)
	for i := range tl0MaxRepliesPerDay {
		if _, err := ps.Reply(context.Background(), ReplyParams{ThreadID: th.ID, AuthorID: replier, BodyRaw: "r"}); err != nil {
			t.Fatalf("reply %d should pass: %v", i, err)
		}
	}
	if _, err := ps.Reply(context.Background(), ReplyParams{ThreadID: th.ID, AuthorID: replier, BodyRaw: "r"}); !isSandbox(err) {
		t.Fatalf("11th reply should hit the daily cap, got %v", err)
	}
}

func isSandbox(err error) bool {
	_, ok := err.(*SandboxError)
	return ok
}
