package service

import (
	"context"
	"testing"

	"api/internal/platform/community/model"
)

// visibleReply seeds the author as an established TL1 (no holds) and replies, so
// the post is visible (not sandbox-held) — the usual subject of a flag test.
func visibleReply(t *testing.T, ps *PostService, threadID, author int64) *model.CommunityPost {
	t.Helper()
	seedTrust(t, author, model.TrustLevelBasic, 0)
	p, err := ps.Reply(context.Background(), ReplyParams{ThreadID: threadID, AuthorID: author, BodyRaw: "content"})
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	return p
}

func pendingReviewForPost(t *testing.T, postID int64) *model.CommunityReviewItem {
	t.Helper()
	var it model.CommunityReviewItem
	if err := testDB.Where("post_id = ? AND status = ?", postID, model.ReviewStatusPending).First(&it).Error; err != nil {
		return nil
	}
	return &it
}

func TestFlag_ThresholdHide(t *testing.T) {
	cleanTables(t)
	sink := &recordingSink{}
	ts := NewThreadService(testDB, NoopSink{})
	ps := NewPostService(testDB, NoopSink{})
	fs := NewFlagService(testDB, sink)
	th := openTopic(t, ts, "letmoe", 100, "b1", "opening")
	post := visibleReply(t, ps, th.ID, 200)
	ctx := context.Background()

	// Three fresh TL0 reporters (weight 1.0 each) → threshold 3.0 on the third.
	if err := fs.Submit(ctx, post.ID, 300, nil, nil); err != nil {
		t.Fatalf("flag: %v", err)
	}
	if getPost(t, post.ID).Status != model.PostStatusVisible {
		t.Fatal("one report should not hide")
	}
	fs.Submit(ctx, post.ID, 301, nil, nil)
	if getPost(t, post.ID).Status != model.PostStatusVisible {
		t.Fatal("two reports should not hide")
	}
	fs.Submit(ctx, post.ID, 302, nil, nil)
	if getPost(t, post.ID).Status != model.PostStatusHidden {
		t.Fatal("three reports should hide")
	}
	if pendingReviewForPost(t, post.ID) == nil {
		t.Fatal("a hidden post should be enqueued")
	}
	if sink.count(EventFlagThreshold) != 1 {
		t.Fatalf("expected 1 flag.threshold event, got %d", sink.count(EventFlagThreshold))
	}

	// A further report on the now-hidden post records the flag but does not
	// re-enqueue.
	fs.Submit(ctx, post.ID, 303, nil, nil)
	var items int64
	testDB.Model(&model.CommunityReviewItem{}).Where("post_id = ?", post.ID).Count(&items)
	if items != 1 {
		t.Fatalf("hidden-post re-report should not re-enqueue: %d items", items)
	}
}

func TestFlag_TL3SingleVoteOnTL0(t *testing.T) {
	cleanTables(t)
	ts := NewThreadService(testDB, NoopSink{})
	ps := NewPostService(testDB, NoopSink{})
	fs := NewFlagService(testDB, &recordingSink{})
	th := openTopic(t, ts, "letmoe", 100, "b1", "opening")

	// A visible post by a TL0 author (holds spent, so not sandbox-held).
	seedTrust(t, 400, model.TrustLevelNew, 0)
	post, err := ps.Reply(context.Background(), ReplyParams{ThreadID: th.ID, AuthorID: 400, BodyRaw: "x"})
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	if post.Status != model.PostStatusVisible {
		t.Fatalf("expected visible, got %d", post.Status)
	}

	// A single TL3 report hides a TL0 author's post immediately.
	seedTrust(t, 500, model.TrustLevelRegular, 0)
	fs.Submit(context.Background(), post.ID, 500, nil, nil)
	if getPost(t, post.ID).Status != model.PostStatusHidden {
		t.Fatal("a TL3 report on a TL0 author should hide on a single vote")
	}
}

func TestReview_DecisionBackfill(t *testing.T) {
	cleanTables(t)
	ts := NewThreadService(testDB, NoopSink{})
	ps := NewPostService(testDB, NoopSink{})
	fs := NewFlagService(testDB, &recordingSink{})
	rs := NewReviewService(testDB)
	th := openTopic(t, ts, "letmoe", 100, "b1", "opening")
	ctx := context.Background()

	// Hide a post via three reports, then REJECT (content removed) → reporters
	// were right → flags_agreed++.
	post := visibleReply(t, ps, th.ID, 200)
	for _, r := range []int64{300, 301, 302} {
		fs.Submit(ctx, post.ID, r, nil, nil)
	}
	item := pendingReviewForPost(t, post.ID)
	if item == nil {
		t.Fatal("no queue item")
	}
	if err := rs.Reject(ctx, item.ID, 999); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if getPost(t, post.ID).Status != model.PostStatusDeleted {
		t.Fatal("reject should tombstone the post")
	}
	if a := getTrust(t, 300).FlagsAgreed; a == nil || *a != 1 {
		t.Fatalf("reporter accuracy not backfilled (agreed): %v", a)
	}

	// A second post: hide, then APPROVE (content kept) → reporters were wrong →
	// flags_disagreed++.
	post2 := visibleReply(t, ps, th.ID, 201)
	for _, r := range []int64{310, 311, 312} {
		fs.Submit(ctx, post2.ID, r, nil, nil)
	}
	item2 := pendingReviewForPost(t, post2.ID)
	if err := rs.Approve(ctx, item2.ID, 999); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if getPost(t, post2.ID).Status != model.PostStatusVisible {
		t.Fatal("approve should restore the post")
	}
	if d := getTrust(t, 310).FlagsDisagreed; d == nil || *d != 1 {
		t.Fatalf("reporter accuracy not backfilled (disagreed): %v", d)
	}
}

func TestReview_HoldRelease(t *testing.T) {
	cleanTables(t)
	ts := NewThreadService(testDB, NoopSink{})
	ps := NewPostService(testDB, NoopSink{})
	rs := NewReviewService(testDB)
	th := openTopic(t, ts, "letmoe", 100, "b1", "opening")

	// A fresh TL0 author's first reply is held + enqueued (first_post_hold).
	post, err := ps.Reply(context.Background(), ReplyParams{ThreadID: th.ID, AuthorID: 600, BodyRaw: "hi"})
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	if post.Status != model.PostStatusHidden {
		t.Fatalf("first post should be held: %d", post.Status)
	}
	item := pendingReviewForPost(t, post.ID)
	if item == nil || item.Source == nil || *item.Source != model.ReviewSourceFirstPostHold {
		t.Fatalf("held post should enqueue a first_post_hold item: %+v", item)
	}

	// Approve releases the post; the hold counter is NOT refunded (one-way).
	if err := rs.Approve(context.Background(), item.ID, 999); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if getPost(t, post.ID).Status != model.PostStatusVisible {
		t.Fatal("approve should release the held post")
	}
	if r := getTrust(t, 600).FirstPostsHeldRemaining; r != 1 {
		t.Fatalf("hold must not be refunded: remaining=%d", r)
	}
}
