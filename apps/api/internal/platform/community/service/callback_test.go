package service

import (
	"context"
	"strconv"
	"testing"
	"time"

	"api/internal/platform/community/model"
)

const cbSecret = "trust-callback-secret"

// D⑩ (signature): a valid HMAC within the window passes; bad signature, stale
// timestamp, empty secret, and missing headers all fail closed.
func TestVerifyTrustSignature(t *testing.T) {
	body := []byte(`{"disposition_id":1,"action":1}`)
	now := time.Now()
	ts := strconv.FormatInt(now.Unix(), 10)
	sig := signTrustPayload(cbSecret, ts, body)

	if !VerifyTrustSignature(cbSecret, ts, sig, body, now) {
		t.Fatal("a valid signature must verify")
	}
	if VerifyTrustSignature(cbSecret, ts, "deadbeef", body, now) {
		t.Fatal("a bad signature must fail")
	}
	// A timestamp more than the window in the past → reject (replay guard).
	stale := strconv.FormatInt(now.Add(-10*time.Minute).Unix(), 10)
	if VerifyTrustSignature(cbSecret, stale, signTrustPayload(cbSecret, stale, body), body, now) {
		t.Fatal("a stale timestamp must fail")
	}
	if VerifyTrustSignature("", ts, sig, body, now) {
		t.Fatal("an empty secret must fail closed")
	}
	if VerifyTrustSignature(cbSecret, "", "", body, now) {
		t.Fatal("missing headers must fail")
	}
}

// enqueuePending inserts a pending review item for a post (a prior forward).
func enqueuePending(t *testing.T, site string, postID int64) int64 {
	t.Helper()
	source := model.ReviewSourceFlags
	it := model.CommunityReviewItem{Site: &site, PostID: &postID, Source: &source, Status: model.ReviewStatusPending}
	if err := testDB.Create(&it).Error; err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	return it.ID
}

func callback(postID int64, action int16) TrustCallback {
	return TrustCallback{
		DispositionID: 1, SubjectKind: "community_post",
		SubjectID: strconv.FormatInt(postID, 10), Action: action, ReasonCode: "x",
	}
}

// D⑩ (mapping): hide/remove/none enforce correctly, none never resurrects an
// author-deleted post, unsupported actions are flagged, and the local item is
// closed alongside — all idempotent on replay.
func TestCallbackEnforcement(t *testing.T) {
	ctx := context.Background()
	svc := NewCallbackService(testDB)

	// hide: a visible post → hidden; the local item closes rejected.
	t.Run("hide", func(t *testing.T) {
		cleanTables(t)
		ts := NewThreadService(testDB, NoopSink{})
		ps := NewPostService(testDB, NoopSink{})
		th := openTopic(t, ts, "letmoe", 100, "b1", "opening")
		post := visibleReply(t, ps, th.ID, 200)
		itemID := enqueuePending(t, "letmoe", post.ID)

		res, err := svc.Handle(ctx, callback(post.ID, trustActionHide))
		if err != nil || res != CallbackEnforced {
			t.Fatalf("hide: res=%v err=%v", res, err)
		}
		if getPost(t, post.ID).Status != model.PostStatusHidden {
			t.Fatal("hide must set the post hidden")
		}
		if reloadItem(t, itemID).Status != model.ReviewStatusRejected {
			t.Fatal("hide must close the local item rejected")
		}
		// Replay is idempotent.
		if _, err := svc.Handle(ctx, callback(post.ID, trustActionHide)); err != nil {
			t.Fatalf("replay: %v", err)
		}
		if getPost(t, post.ID).Status != model.PostStatusHidden {
			t.Fatal("replay must keep the post hidden")
		}
	})

	// remove: any non-deleted post → tombstoned.
	t.Run("remove", func(t *testing.T) {
		cleanTables(t)
		ts := NewThreadService(testDB, NoopSink{})
		ps := NewPostService(testDB, NoopSink{})
		th := openTopic(t, ts, "letmoe", 100, "b1", "opening")
		_, item := heldReply(t, ps, th.ID, 700, "held")
		post := *item.PostID
		if _, err := svc.Handle(ctx, callback(post, trustActionRemove)); err != nil {
			t.Fatalf("remove: %v", err)
		}
		if getPost(t, post).Status != model.PostStatusDeleted {
			t.Fatal("remove must tombstone the post")
		}
		if reloadItem(t, item.ID).Status != model.ReviewStatusRejected {
			t.Fatal("remove must close the local item rejected")
		}
	})

	// none: a mod-hidden/held post → restored; the item closes approved.
	t.Run("none_restores_held", func(t *testing.T) {
		cleanTables(t)
		ts := NewThreadService(testDB, NoopSink{})
		ps := NewPostService(testDB, NoopSink{})
		th := openTopic(t, ts, "letmoe", 100, "b1", "opening")
		_, item := heldReply(t, ps, th.ID, 700, "held")
		post := *item.PostID
		if _, err := svc.Handle(ctx, callback(post, trustActionNone)); err != nil {
			t.Fatalf("none: %v", err)
		}
		if getPost(t, post).Status != model.PostStatusVisible {
			t.Fatal("none must restore a held post")
		}
		if reloadItem(t, item.ID).Status != model.ReviewStatusApproved {
			t.Fatal("none must close the local item approved")
		}
	})

	// none: an author-deleted post must NEVER be resurrected.
	t.Run("none_keeps_author_deleted", func(t *testing.T) {
		cleanTables(t)
		ts := NewThreadService(testDB, NoopSink{})
		ps := NewPostService(testDB, NoopSink{})
		th := openTopic(t, ts, "letmoe", 100, "b1", "opening")
		post := visibleReply(t, ps, th.ID, 200)
		if err := ps.Delete(ctx, post.ID, 200, false); err != nil {
			t.Fatalf("author delete: %v", err)
		}
		enqueuePending(t, "letmoe", post.ID)
		if _, err := svc.Handle(ctx, callback(post.ID, trustActionNone)); err != nil {
			t.Fatalf("none: %v", err)
		}
		if getPost(t, post.ID).Status != model.PostStatusDeleted {
			t.Fatal("none must NOT resurrect an author-deleted post")
		}
	})

	// unsupported actions (3/4/5) are flagged for manual handling, no change.
	t.Run("unsupported", func(t *testing.T) {
		cleanTables(t)
		ts := NewThreadService(testDB, NoopSink{})
		ps := NewPostService(testDB, NoopSink{})
		th := openTopic(t, ts, "letmoe", 100, "b1", "opening")
		post := visibleReply(t, ps, th.ID, 200)
		res, err := svc.Handle(ctx, callback(post.ID, 3))
		if err != nil || res != CallbackUnsupported {
			t.Fatalf("unsupported: res=%v err=%v", res, err)
		}
		if getPost(t, post.ID).Status != model.PostStatusVisible {
			t.Fatal("unsupported action must not change the post")
		}
	})
}
