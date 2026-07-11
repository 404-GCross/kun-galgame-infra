package service

import (
	"context"
	"testing"

	"api/internal/platform/community/model"
)

func TestPromotion_Levels(t *testing.T) {
	cleanTables(t)
	ts := NewTrustService(testDB)
	ctx := context.Background()
	u := int64(1)

	// TL0 → TL1: entered 5 topics / read 30 posts / read 10 minutes.
	tr, err := ts.RecordActivity(ctx, ActivityReceipt{UserID: u, TopicsEntered: 5, PostsRead: 30, ReadTimeS: 600})
	if err != nil {
		t.Fatalf("receipt 1: %v", err)
	}
	if tr.Level != model.TrustLevelBasic {
		t.Fatalf("TL1 not reached: level=%d", tr.Level)
	}

	// TL1 → TL2: 15 days / gave+received a like / 100 posts read.
	setLikes(t, u, 1, 1)
	tr, err = ts.RecordActivity(ctx, ActivityReceipt{UserID: u, PostsRead: 70, DaysVisited: 15})
	if err != nil {
		t.Fatalf("receipt 2: %v", err)
	}
	if tr.Level != model.TrustLevelMember {
		t.Fatalf("TL2 not reached: level=%d", tr.Level)
	}

	// TL2 → TL3: rolling-window activity supplied by the receipt (≥50 of 100).
	win := int32(60)
	tr, _ = ts.RecordActivity(ctx, ActivityReceipt{UserID: u, WindowActiveDays: &win})
	if tr.Level != model.TrustLevelRegular {
		t.Fatalf("TL3 not reached: level=%d", tr.Level)
	}

	// TL3 demote: a later window below the bar drops back to the earned level.
	low := int32(30)
	tr, _ = ts.RecordActivity(ctx, ActivityReceipt{UserID: u, WindowActiveDays: &low})
	if tr.Level != model.TrustLevelMember {
		t.Fatalf("TL3 should demote to TL2 on a low window: level=%d", tr.Level)
	}

	// Idempotent: a zero receipt re-evaluates to the same level (no over-promote).
	tr, _ = ts.RecordActivity(ctx, ActivityReceipt{UserID: u})
	if tr.Level != model.TrustLevelMember {
		t.Fatalf("empty receipt changed the level: %d", tr.Level)
	}
}

func TestStarterBoost(t *testing.T) {
	cleanTables(t)
	ts := NewTrustService(testDB)
	ctx := context.Background()

	// creator → floor TL1.
	tr, err := ts.SetBoost(ctx, 10, model.GrantedBoostCreator)
	if err != nil {
		t.Fatalf("boost creator: %v", err)
	}
	if tr.Level != model.TrustLevelBasic || tr.GrantedBoost == nil || *tr.GrantedBoost != model.GrantedBoostCreator {
		t.Fatalf("creator boost: level=%d boost=%v", tr.Level, tr.GrantedBoost)
	}

	// staff → floor TL3.
	tr, _ = ts.SetBoost(ctx, 11, model.GrantedBoostStaff)
	if tr.Level != model.TrustLevelRegular {
		t.Fatalf("staff boost should floor at TL3: level=%d", tr.Level)
	}

	// A boost is a floor, never a demotion: a TL2 user given veteran stays TL2.
	seedTrust(t, 12, model.TrustLevelMember, 0)
	tr, _ = ts.SetBoost(ctx, 12, model.GrantedBoostVeteran)
	if tr.Level != model.TrustLevelMember {
		t.Fatalf("boost must not demote a TL2 user: level=%d", tr.Level)
	}

	// The staff floor protects TL3 from a rolling-window demotion.
	ts.SetBoost(ctx, 13, model.GrantedBoostStaff) // level 3
	low := int32(5)
	tr, _ = ts.RecordActivity(ctx, ActivityReceipt{UserID: 13, WindowActiveDays: &low})
	if tr.Level != model.TrustLevelRegular {
		t.Fatalf("staff-boosted TL3 must not demote: level=%d", tr.Level)
	}
}

func TestStaffBoost_ClearsFirstPostHolds(t *testing.T) {
	cleanTables(t)
	ts := NewTrustService(testDB)
	ctx := context.Background()

	// A fresh user's trust row is born with 2 holds; the staff boost zeroes the
	// budget outright (docs/proj/17 decision 4: staff content is never held).
	tr, err := ts.SetBoost(ctx, 20, model.GrantedBoostStaff)
	if err != nil {
		t.Fatalf("staff boost: %v", err)
	}
	if tr.FirstPostsHeldRemaining != 0 {
		t.Fatalf("staff boost must zero the hold budget: got %d", tr.FirstPostsHeldRemaining)
	}

	// The freshly-boosted staffer's first post lands VISIBLE (not held) — the
	// end-to-end effect the decision asks for.
	threads := NewThreadService(testDB, NoopSink{})
	_, opening, err := threads.OpenTopic(ctx, OpenThreadParams{
		Site: "letmoe", AuthorID: 20, AnchorKind: model.AnchorKindBoard, AnchorID: "b1",
		Title: "staff first topic", ContentRating: model.ContentRatingAll, BodyRaw: "hello",
	})
	if err != nil {
		t.Fatalf("staff first topic: %v", err)
	}
	if opening.Status != model.PostStatusVisible {
		t.Fatalf("a boosted staffer's first post must be visible, got status=%d", opening.Status)
	}

	// A creator boost does NOT touch the hold budget (only staff is review-exempt).
	tr, err = ts.SetBoost(ctx, 21, model.GrantedBoostCreator)
	if err != nil {
		t.Fatalf("creator boost: %v", err)
	}
	if tr.FirstPostsHeldRemaining != 2 {
		t.Fatalf("creator boost must keep the hold budget: got %d", tr.FirstPostsHeldRemaining)
	}
}
