package service

import (
	"testing"

	"api/internal/platform/catalog/model"
)

// The tenant gate on the PUBLIC works browse lane (wave 161 P5, 162 §4 ruling ①
// — the other half of the S2S search gate wave 162 shipped).
//
// The pinning that matters is not "site= filters": it is that the predicate is
// inside the LIMIT. A tenant queue built by fetching a page and dropping the
// other tenants' rows afterwards returns short pages and a next_cursor that
// describes a set the caller never saw.
func TestWorksListSiteFilter(t *testing.T) {
	cleanTables(t)
	ctx := t.Context()
	svc := newPublicSvc()

	claimed := func(name, site string, productWorkID int64, state int16) int64 {
		t.Helper()
		w := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, name)
		claimWork(t, w.ID, site, productWorkID)
		setClaimState(t, w.ID, i16(state))
		return w.ID
	}
	// Interleaved by id so a post-filter implementation would visibly return
	// short pages: ours / theirs / ours / theirs / unclaimed.
	mine1 := claimed("うちの一番", "kungal", 9800, model.ClaimStateLive)
	claimed("よその一番", "moyu", 9801, model.ClaimStateLive)
	mine2 := claimed("うちの二番", "kungal", 9802, model.ClaimStatePending)
	claimed("よその二番", "moyu", 9803, model.ClaimStateLive)
	createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "未認領")

	ids := func(f WorksListFilter, limit int) []int64 {
		t.Helper()
		page, err := svc.WorksList(ctx, f, "", limit)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]int64, 0, len(page.Items))
		for _, it := range page.Items {
			out = append(out, it.ID)
		}
		return out
	}

	if got := ids(WorksListFilter{Sort: "id"}, 50); len(got) != 5 {
		t.Fatalf("absent site must not gate: %v", got)
	}
	if got := ids(WorksListFilter{Sort: "id", Site: "kungal"}, 50); len(got) != 2 ||
		got[0] != mine1 || got[1] != mine2 {
		t.Fatalf("site gate: want [%d %d], got %v", mine1, mine2, got)
	}
	// Inside the LIMIT: with two of our works interleaved among five rows, a
	// limit of 2 must still return two of OURS.
	if got := ids(WorksListFilter{Sort: "id", Site: "kungal"}, 2); len(got) != 2 {
		t.Fatalf("site predicate must live inside the LIMIT, got %v", got)
	}
	// AND, not OR, with the claim-state gate — the review-queue shape.
	if got := ids(WorksListFilter{
		Sort: "id", Site: "kungal", ClaimStates: []string{model.ClaimStateKeyPending},
	}, 50); len(got) != 1 || got[0] != mine2 {
		t.Fatalf("site + claim_state must AND: %v", got)
	}
	// An unknown tenant is an empty page, not an error: sites are registered
	// per OAuth client and this face must not double as a tenant directory.
	if got := ids(WorksListFilter{Sort: "id", Site: "nobody"}, 50); len(got) != 0 {
		t.Fatalf("unknown site must match nothing: %v", got)
	}
	// Unclaimed rows are NOT a tenant: site= never returns them.
	if got := ids(WorksListFilter{Sort: "id", Site: ""}, 50); len(got) != 5 {
		t.Fatalf("empty site is the absent gate, not a filter on unclaimed: %v", got)
	}
}
