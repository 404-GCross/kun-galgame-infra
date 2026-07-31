package main

import (
	"testing"

	catmodel "api/internal/platform/catalog/model"
)

func ptr[T any](v T) *T { return &v }

func TestPlanEventsRecordsEveryClaimExactlyOnce(t *testing.T) {
	rows := []workRow{
		{ID: 1, ClaimState: ptr(catmodel.ClaimStateLive)},
		{ID: 2, ClaimState: ptr(catmodel.ClaimStateDraft)},
		{ID: 3, ClaimState: ptr(catmodel.ClaimStateHidden)},
		{ID: 4, ClaimState: ptr(catmodel.ClaimStatePending)},
		{ID: 5, ClaimState: ptr(catmodel.ClaimStateDeclined)},
	}
	events, nullState, already := planEvents(rows)
	if len(events) != len(rows) || nullState != 0 || already != 0 {
		t.Fatalf("events=%d null=%d already=%d, want one event per claim", len(events), nullState, already)
	}
	for i, e := range events {
		if e.WorkID != rows[i].ID || e.ToState != *rows[i].ClaimState {
			t.Errorf("event %d = %+v, want the claim's own current state", i, e)
		}
	}
}

// A NULL claim_state on a claimed row means `live` — the one definition in
// model.ClaimStateKey. The backfill must record that, not skip the row.
func TestNullClaimStateIsRecordedAsLive(t *testing.T) {
	events, nullState, _ := planEvents([]workRow{{ID: 7, ClaimState: nil}})
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1 — a claim with no stamped state still has a state", len(events))
	}
	if events[0].ToState != catmodel.ClaimStateLive {
		t.Errorf("to_state = %d, want live (%d)", events[0].ToState, catmodel.ClaimStateLive)
	}
	if nullState != 1 {
		t.Errorf("nullState = %d, want 1 — the fill-in must be visible in the ledger", nullState)
	}
}

func TestAlreadyBackfilledClaimsAreSkipped(t *testing.T) {
	events, _, already := planEvents([]workRow{
		{ID: 1, ClaimState: ptr(catmodel.ClaimStateLive), HasEvent: true},
		{ID: 2, ClaimState: ptr(catmodel.ClaimStateLive)},
	})
	if len(events) != 1 || events[0].WorkID != 2 || already != 1 {
		t.Fatalf("events=%+v already=%d, want only the un-backfilled work", events, already)
	}
}

// The verdict text is the report's whole product, so the cases that matter are
// pinned: the forum client must read as already aligned (no binding change is
// part of this migration) and the wiki client's IMAGE scope must read as
// untouchable.
func TestClientVerdicts(t *testing.T) {
	cases := []struct{ catalogSite, imageSiteKey, want string }{
		{toSite, "kungal", "already aligned — inherits the re-sited claims, no change"},
		{"", fromSite, "image scope only — never touched (bytes keep this site forever)"},
		{fromSite, "", "STOP: a client still bound to the retiring site"},
		{"", "moyu", "no catalog binding — cannot claim; unaffected by the re-site"},
		{"letmoe", "letmoe", "other tenant — unaffected"},
	}
	for _, c := range cases {
		if got := clientVerdict(c.catalogSite, c.imageSiteKey); got != c.want {
			t.Errorf("clientVerdict(%q, %q) = %q, want %q", c.catalogSite, c.imageSiteKey, got, c.want)
		}
	}
}
