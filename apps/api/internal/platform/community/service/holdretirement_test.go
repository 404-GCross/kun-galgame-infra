package service

import (
	"context"
	"testing"

	"api/internal/platform/community/model"
)

// TestNewcomerPostsStraightThrough is wave 07's headline: a brand-new author —
// no trust row at all until this call creates one — publishes visibly and does
// NOT enter the review queue.
//
// The retired behaviour held a newcomer's first TWO posts. Measured over 208
// production hold items it caught zero harmful posts (94.5% approved, and all 11
// rejections were benign galgame comments, one a legitimate bug report), so the
// hold was pure cost: newcomers posting into the void until someone released
// them by hand.
func TestNewcomerPostsStraightThrough(t *testing.T) {
	cleanTables(t)
	ts := NewThreadService(testDB, NoopSink{})
	ps := NewPostService(testDB, NoopSink{})
	th := openTopic(t, ts, "letmoe", 100, "b1", "opening")

	const newcomer int64 = 777
	for i, body := range []string{"first ever post", "second ever post"} {
		post, err := ps.Reply(context.Background(), ReplyParams{ThreadID: th.ID, AuthorID: newcomer, BodyRaw: body})
		if err != nil {
			t.Fatalf("reply %d: %v", i+1, err)
		}
		if post.Status != model.PostStatusVisible {
			t.Fatalf("post %d status = %d, want visible — the newcomer hold is retired", i+1, post.Status)
		}
		if item := pendingReviewForPost(t, post.ID); item != nil {
			t.Fatalf("post %d enqueued a review item (%+v); nothing should hold a clean newcomer", i+1, item)
		}
	}

	// The counter the retirement turns off: an auto-created trust row must carry
	// 0, written explicitly. If the model ever regains a `default:2` tag, GORM
	// omits the zero from the INSERT, the DDL default wins, and this catches it.
	if r := getTrust(t, newcomer).FirstPostsHeldRemaining; r != 0 {
		t.Fatalf("fresh trust row hold budget = %d, want 0", r)
	}
}

// TestTier0HoldStillEnqueues is the counterweight regression: retiring the
// blanket hold must not disarm the gate that actually works. The synchronous
// Tier0 word list accounted for every real hold in production (15/15 of its
// matches reached the queue), and it must keep publishing-then-enqueueing.
func TestTier0HoldStillEnqueues(t *testing.T) {
	cleanTables(t)
	ts := NewThreadService(testDB, NoopSink{})
	ps := NewPostService(testDB, NoopSink{}, WithPostChecker(NewCheckService(&fakeChecker{decision: checkHold})))
	th := openTopic(t, ts, "letmoe", 100, "b1", "opening")

	post, err := ps.Reply(context.Background(), ReplyParams{ThreadID: th.ID, AuthorID: 778, BodyRaw: "suspect words here"})
	if err != nil {
		t.Fatalf("reply: %v", err)
	}
	// "enqueue, don't block": a suspect post stays visible and is reviewed after.
	if post.Status != model.PostStatusVisible {
		t.Fatalf("suspect post status = %d, want visible", post.Status)
	}
	item := pendingReviewForPost(t, post.ID)
	if item == nil || item.Source == nil || *item.Source != model.ReviewSourceSuspectWords {
		t.Fatalf("suspect post should enqueue a suspect_words item, got %+v", item)
	}
}
