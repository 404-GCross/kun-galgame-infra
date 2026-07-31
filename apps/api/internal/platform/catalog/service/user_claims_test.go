package service

import (
	"context"
	"testing"

	"api/internal/platform/catalog/model"
)

// The per-user read face (wave 157). Every case drives the REAL action service,
// so what is being pinned is the aggregate over the events those actions
// actually wrote — not a fixture's idea of them.

// TestClaimsByActorListsWhatTheUserTouched: the list is scoped to the acting
// user, ordered by most recent activity, and each row carries the WORK's latest
// verdict rather than the user's own last move — the decline reason a submitter
// needs is an event someone else caused.
func TestClaimsByActorListsWhatTheUserTouched(t *testing.T) {
	s := newLifecycle(t)
	ctx := context.Background()
	const submitter, moderator, stranger = 41, 99, 42

	mine := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "私の投稿")
	product := int64(9001)
	act(t, s, mine.ID, ClaimActionClaim, ClaimActionParams{Site: "kungal", ProductWorkID: &product, ActorUID: submitter})
	act(t, s, mine.ID, ClaimActionSubmit, ClaimActionParams{Site: "kungal", ActorUID: submitter})

	// A second work of the same user, whose activity then falls behind.
	other := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "私の下書き")
	product2 := int64(9002)
	act(t, s, other.ID, ClaimActionClaim, ClaimActionParams{Site: "kungal", ProductWorkID: &product2, ActorUID: submitter})

	// The verdict lands last, so the declined submission is the most recent
	// activity — and it was caused by somebody other than the submitter.
	act(t, s, mine.ID, ClaimActionDecline, ClaimActionParams{ActorUID: moderator, Reason: "出典なし"})

	// Somebody else's work must never appear.
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
	// Most recent activity first: the declined submission outranks the draft.
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
	// acted_count is the USER's own moves (claim + submit), not the work's three.
	if head.ActedCount != 2 {
		t.Fatalf("acted_count: %+v", head)
	}
	if head.Site != "kungal" || head.ProductWorkID == nil || *head.ProductWorkID != product {
		t.Fatalf("identity: %+v", head)
	}
	if head.FirstActedAt.After(head.LastEventAt) {
		t.Fatalf("first_acted_at must not be after the latest event: %+v", head)
	}

	// A user who never acted gets an empty page, not everybody's claims.
	if items, total, err = s.ClaimsByActor(ctx, UserClaimQuery{ActorUID: 777}); err != nil {
		t.Fatal(err)
	} else if total != 0 || len(items) != 0 {
		t.Fatalf("unknown actor: total=%d %+v", total, items)
	}
}

// TestClaimsByActorFiltersAndCursor: the claim_state filter is the same
// vocabulary every other face speaks (so `total` under a filter is the
// per-user statistic downstream would otherwise need an endpoint for), and the
// cursor walks the list exactly once.
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
	// Publish the last one: two drafts and one live claim.
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
	// A tenant this user never acted for yields nothing.
	if _, total, err = s.ClaimsByActor(ctx, UserClaimQuery{ActorUID: uid, Site: "moyu"}); err != nil {
		t.Fatal(err)
	} else if total != 0 {
		t.Fatalf("site filter: %d", total)
	}

	// Page one work at a time and pin that the walk covers each work once.
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

// TestEventsSinceFiltersByActor: the same feed, narrowed to one user — the
// cheap half of the per-user need (wave 157), and it must not disturb the
// unfiltered stream.
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
	// The filter composes with the cursor rather than replacing it.
	tail, err := s.EventsSince(ctx, mine[0].ID, 100, "", 11)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) != 1 || tail[0].ID != mine[1].ID {
		t.Fatalf("cursor + actor: %+v", tail)
	}
}
