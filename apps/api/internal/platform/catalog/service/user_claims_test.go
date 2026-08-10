package service

import (
	"context"
	"testing"

	"api/internal/platform/catalog/model"
)

func TestClaimsByActorListsWhatTheUserTouched(t *testing.T) {
	s := newLifecycle(t)
	ctx := context.Background()
	const submitter, moderator, stranger = 41, 99, 42

	mine := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "私の投稿")
	product := int64(9001)
	act(t, s, mine.ID, ClaimActionClaim, ClaimActionParams{Site: "kungal", ProductWorkID: &product, ActorUID: submitter})
	act(t, s, mine.ID, ClaimActionSubmit, ClaimActionParams{Site: "kungal", ActorUID: submitter})

	other := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "私の下書き")
	product2 := int64(9002)
	act(t, s, other.ID, ClaimActionClaim, ClaimActionParams{Site: "kungal", ProductWorkID: &product2, ActorUID: submitter})

	act(t, s, mine.ID, ClaimActionDecline, ClaimActionParams{ActorUID: moderator, Reason: "出典なし"})

	theirs := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "他人の投稿")
	product3 := int64(9003)
	act(t, s, theirs.ID, ClaimActionClaim, ClaimActionParams{Site: "kungal", ProductWorkID: &product3, ActorUID: stranger})

	items, total, err := s.ClaimsByActor(ctx, UserClaimQuery{ActorUID: submitter})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("want the user's two works, got total=%d items=%+v", total, items)
	}
	if items[0].WorkID != mine.ID || items[1].WorkID != other.ID {
		t.Fatalf("order: %+v", items)
	}
	head := items[0]
	if head.ClaimState != model.ClaimStateKeyDeclined || head.LastToState != model.ClaimStateKeyDeclined {
		t.Fatalf("current state: %+v", head)
	}
	if head.LastReason == nil || *head.LastReason != "出典なし" || head.LastActorUID != moderator {
		t.Fatalf("the moderator's verdict must ride the row: %+v", head)
	}
	if head.LastFromState == nil || *head.LastFromState != model.ClaimStateKeyPending {
		t.Fatalf("from_state: %+v", head)
	}
	if head.ActedCount != 2 {
		t.Fatalf("acted_count: %+v", head)
	}
	if head.Site != "kungal" || head.ProductWorkID == nil || *head.ProductWorkID != product {
		t.Fatalf("identity: %+v", head)
	}
	if head.FirstActedAt.After(head.LastEventAt) {
		t.Fatalf("first_acted_at must not be after the latest event: %+v", head)
	}

	if items, total, err = s.ClaimsByActor(ctx, UserClaimQuery{ActorUID: 777}); err != nil {
		t.Fatal(err)
	} else if total != 0 || len(items) != 0 {
		t.Fatalf("unknown actor: total=%d %+v", total, items)
	}
}

func TestClaimsByActorFiltersAndCursor(t *testing.T) {
	s := newLifecycle(t)
	ctx := context.Background()
	const uid = 55

	var ids []int64
	for i, name := range []string{"作品A", "作品B", "作品C"} {
		w := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, name)
		product := int64(8100 + i)
		act(t, s, w.ID, ClaimActionClaim, ClaimActionParams{Site: "kungal", ProductWorkID: &product, ActorUID: uid})
		ids = append(ids, w.ID)
	}
	act(t, s, ids[2], ClaimActionPublish, ClaimActionParams{Site: "kungal", ActorUID: uid})

	live, total, err := s.ClaimsByActor(ctx, UserClaimQuery{ActorUID: uid, ClaimStates: []string{model.ClaimStateKeyLive}})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(live) != 1 || live[0].WorkID != ids[2] {
		t.Fatalf("live filter: total=%d %+v", total, live)
	}
	if _, total, err = s.ClaimsByActor(ctx, UserClaimQuery{ActorUID: uid, ClaimStates: []string{model.ClaimStateKeyDraft}}); err != nil {
		t.Fatal(err)
	} else if total != 2 {
		t.Fatalf("draft total: %d", total)
	}
	if _, total, err = s.ClaimsByActor(ctx, UserClaimQuery{ActorUID: uid, Site: "moyu"}); err != nil {
		t.Fatal(err)
	} else if total != 0 {
		t.Fatalf("site filter: %d", total)
	}

	seen := map[int64]bool{}
	before := int64(0)
	for range 4 {
		page, _, err := s.ClaimsByActor(ctx, UserClaimQuery{ActorUID: uid, Limit: 1, Before: before})
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		if seen[page[0].WorkID] {
			t.Fatalf("cursor returned work %d twice", page[0].WorkID)
		}
		seen[page[0].WorkID] = true
		before = page[0].LastEventID
	}
	if len(seen) != 3 {
		t.Fatalf("the cursor must walk all three works, saw %v", seen)
	}
}

func TestEventsSinceFiltersByActor(t *testing.T) {
	s := newLifecycle(t)
	ctx := context.Background()

	w := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "フィード")
	product := int64(8200)
	act(t, s, w.ID, ClaimActionClaim, ClaimActionParams{Site: "kungal", ProductWorkID: &product, ActorUID: 11})
	act(t, s, w.ID, ClaimActionSubmit, ClaimActionParams{Site: "kungal", ActorUID: 11})
	act(t, s, w.ID, ClaimActionApprove, ClaimActionParams{ActorUID: 22})

	all, err := s.EventsSince(ctx, 0, 100, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("unfiltered feed: %+v", all)
	}
	mine, err := s.EventsSince(ctx, 0, 100, "", 11)
	if err != nil {
		t.Fatal(err)
	}
	if len(mine) != 2 {
		t.Fatalf("actor filter: %+v", mine)
	}
	for _, e := range mine {
		if e.ActorUID != 11 {
			t.Fatalf("foreign event leaked: %+v", e)
		}
	}
	tail, err := s.EventsSince(ctx, mine[0].ID, 100, "", 11)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) != 1 || tail[0].ID != mine[1].ID {
		t.Fatalf("cursor + actor: %+v", tail)
	}
}
