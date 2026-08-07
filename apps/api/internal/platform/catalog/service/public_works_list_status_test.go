package service

import (
	"testing"

	"api/internal/platform/catalog/model"
)

// The registry status axis as a FILTER (wave 186a). Two things are pinned:
// the default is still {live} — the frozen public population, unchanged for
// every caller who names nothing — and naming the wider set is what the
// moderator queue view rides on.
func TestWorksListStatusAxis(t *testing.T) {
	cleanTables(t)
	ctx := t.Context()
	svc := newPublicSvc()

	live := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "公開済み")
	stub := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusStub, "バー未満")
	merged := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusMerged, "統合済み")

	ids := func(f WorksListFilter) []int64 {
		t.Helper()
		page, err := svc.WorksList(ctx, f, "", 50)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]int64, 0, len(page.Items))
		for _, it := range page.Items {
			out = append(out, it.ID)
		}
		return out
	}

	// Absent is NOT "no gate" — it is the live-only default, exactly as before.
	if got := ids(WorksListFilter{Sort: "id"}); len(got) != 1 || got[0] != live.ID {
		t.Fatalf("empty Statuses must mean {live}: want [%d], got %v", live.ID, got)
	}
	// Naming live explicitly is the same query.
	if got := ids(WorksListFilter{Sort: "id", Statuses: []int16{model.WorkStatusLive}}); len(got) != 1 || got[0] != live.ID {
		t.Fatalf("Statuses={live} must equal the default: %v", got)
	}
	// The queue view's set: live ∪ stub, never the merged tombstone.
	got := ids(WorksListFilter{Sort: "id", Statuses: []int16{model.WorkStatusLive, model.WorkStatusStub}})
	if len(got) != 2 || got[0] != live.ID || got[1] != stub.ID {
		t.Fatalf("Statuses={live,stub}: want [%d %d], got %v", live.ID, stub.ID, got)
	}
	for _, id := range got {
		if id == merged.ID {
			t.Fatal("a merged work must never reach the works list: status=2 is a 404 tombstone")
		}
	}
}

// The queue view's whole predicate, assembled the way the handler assembles it:
// the widened status axis AND claim_state=pending AND the tenant pin. The point
// is that the three AND together inside the LIMIT — a queue built by filtering
// a fetched page would return short pages and a lying cursor.
func TestWorksListPendingQueueShape(t *testing.T) {
	cleanTables(t)
	ctx := t.Context()
	svc := newPublicSvc()

	claimed := func(name, site string, productWorkID int64, status, state int16) int64 {
		t.Helper()
		w := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, status, name)
		claimWork(t, w.ID, site, productWorkID)
		setClaimState(t, w.ID, i16(state))
		return w.ID
	}
	// Interleaved by id — ours / theirs / ours / ours-but-live / unclaimed — so a
	// post-filter implementation would visibly return short pages.
	minePending := claimed("うちの投稿", "kungal", 9810, model.WorkStatusLive, model.ClaimStatePending)
	claimed("よその投稿", "moyu", 9813, model.WorkStatusLive, model.ClaimStatePending)
	minePendingStub := claimed("うちの投稿(バー未満)", "kungal", 9811, model.WorkStatusStub, model.ClaimStatePending)
	claimed("うちの公開済み", "kungal", 9812, model.WorkStatusLive, model.ClaimStateLive)
	createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "未認領")

	queue := WorksListFilter{
		Sort:        "id",
		Site:        "kungal",
		ClaimStates: []string{model.ClaimStateKeyPending},
		Statuses:    []int16{model.WorkStatusLive, model.WorkStatusStub},
	}
	page, err := svc.WorksList(ctx, queue, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].ID != minePending || page.Items[1].ID != minePendingStub {
		t.Fatalf("queue: want [%d %d], got %v", minePending, minePendingStub, page.Items)
	}

	// Inside the LIMIT: another tenant's pending row sits between ours in id
	// order, so a limit of 2 must still return two of OURS.
	page, err = svc.WorksList(ctx, queue, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("the queue predicate must live inside the LIMIT, got %d rows", len(page.Items))
	}
}
