package service

import (
	"context"
	"errors"
	"testing"

	"api/internal/platform/catalog/model"
)

// The claim lifecycle (wave 155 W2/W3). Every case runs the REAL transaction —
// state write, event append and touch — because their atomicity is the property
// worth pinning: an event a consumer can see whose state is not yet visible on
// the work is the exact drift that made the wiki's message table untrustworthy.

func newLifecycle(t *testing.T) *ClaimLifecycleService {
	t.Helper()
	cleanTables(t)
	if err := testDB.Exec("TRUNCATE catalog_claim_event RESTART IDENTITY").Error; err != nil {
		t.Fatalf("truncate events: %v", err)
	}
	return NewClaimLifecycleService(testDB)
}

// act runs one action and fails the test on error.
func act(t *testing.T, s *ClaimLifecycleService, workID int64, a ClaimAction, p ClaimActionParams) *ClaimActionResult {
	t.Helper()
	p.WorkID, p.Action = workID, a
	res, err := s.Act(context.Background(), p)
	if err != nil {
		t.Fatalf("%s: %v", a, err)
	}
	return res
}

func claimStateOfWork(t *testing.T, workID int64) *int16 {
	t.Helper()
	var got *int16
	if err := testDB.Raw(`SELECT claim_state FROM catalog_work WHERE id = ?`, workID).Scan(&got).Error; err != nil {
		t.Fatal(err)
	}
	return got
}

// TestClaimLifecycleHappyPath walks a submission end to end and pins that every
// step left exactly one event row, in order, with the right signatures.
func TestClaimLifecycleHappyPath(t *testing.T) {
	s := newLifecycle(t)
	work := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "投稿作品")

	product := int64(4242)
	born := act(t, s, work.ID, ClaimActionClaim, ClaimActionParams{Site: "kungal", ProductWorkID: &product, ActorUID: 7})
	if born.From != nil || born.To != model.ClaimStateKeyDraft {
		t.Fatalf("claim: %+v", born)
	}
	act(t, s, work.ID, ClaimActionSubmit, ClaimActionParams{Site: "kungal", ActorUID: 7})
	if got := claimStateOfWork(t, work.ID); got == nil || *got != model.ClaimStatePending {
		t.Fatalf("after submit: %v", got)
	}
	approved := act(t, s, work.ID, ClaimActionApprove, ClaimActionParams{ActorUID: 99})
	if approved.From == nil || *approved.From != model.ClaimStateKeyPending || approved.To != model.ClaimStateKeyLive {
		t.Fatalf("approve: %+v", approved)
	}

	events, err := s.EventsSince(context.Background(), 0, 100, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("events: %+v", events)
	}
	// from_state is NULL exactly once — the birth of the claim.
	if events[0].FromState != nil {
		t.Fatalf("birth event must carry a null from_state: %+v", events[0])
	}
	want := []string{model.ClaimStateKeyDraft, model.ClaimStateKeyPending, model.ClaimStateKeyLive}
	for i, w := range want {
		if events[i].ToState != w {
			t.Fatalf("event %d: to=%s want %s", i, events[i].ToState, w)
		}
		if events[i].WorkID != work.ID || events[i].Site != "kungal" {
			t.Fatalf("event %d: %+v", i, events[i])
		}
	}
	// The claim's identity rides the feed row, so a consumer routes without a
	// second call.
	if events[2].ProductWorkID == nil || *events[2].ProductWorkID != product {
		t.Fatalf("feed snapshot: %+v", events[2])
	}
	if events[2].ActorUID != 99 {
		t.Fatalf("actor: %+v", events[2])
	}
}

// TestClaimLifecycleIllegalTransition pins the 409 payload: the action is
// refused, the current state is reported, and NOTHING was written.
func TestClaimLifecycleIllegalTransition(t *testing.T) {
	s := newLifecycle(t)
	work := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "公開作品")
	claimWork(t, work.ID, "kungal", 5150)
	setClaimState(t, work.ID, i16(model.ClaimStateLive))

	// approve is a pending-only move.
	_, err := s.Act(context.Background(), ClaimActionParams{WorkID: work.ID, Action: ClaimActionApprove, ActorUID: 1})
	var conflict *ClaimTransitionError
	if !errors.As(err, &conflict) {
		t.Fatalf("want ClaimTransitionError, got %v", err)
	}
	if conflict.Current != model.ClaimStateKeyLive {
		t.Fatalf("current state echoed as %q", conflict.Current)
	}
	events, err := s.EventsSince(context.Background(), 0, 10, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("a refused action must write nothing: %+v", events)
	}

	// An unclaimed work cannot be submitted either — `none` is only `claim`'s
	// starting point.
	free := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "未claim")
	if _, err := s.Act(context.Background(), ClaimActionParams{WorkID: free.ID, Action: ClaimActionSubmit, Site: "kungal"}); !errors.As(err, &conflict) {
		t.Fatalf("submit on an unclaimed work: %v", err)
	}
}

// TestClaimLifecycleDeclineNeedsAReason: a decline the submitter cannot act on
// is the moderation habit this vocabulary retires.
func TestClaimLifecycleDeclineNeedsAReason(t *testing.T) {
	s := newLifecycle(t)
	work := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "審査待ち")
	claimWork(t, work.ID, "kungal", 616)
	setClaimState(t, work.ID, i16(model.ClaimStatePending))

	if _, err := s.Act(context.Background(), ClaimActionParams{
		WorkID: work.ID, Action: ClaimActionDecline, ActorUID: 5, Reason: "   ",
	}); !errors.Is(err, ErrClaimReasonRequired) {
		t.Fatalf("blank reason: %v", err)
	}
	res := act(t, s, work.ID, ClaimActionDecline, ClaimActionParams{ActorUID: 5, Reason: "出典なし"})
	if res.To != model.ClaimStateKeyDeclined {
		t.Fatalf("decline: %+v", res)
	}
	events, err := s.EventsSince(context.Background(), 0, 10, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Reason == nil || *events[0].Reason != "出典なし" {
		t.Fatalf("reason must ride the event: %+v", events)
	}
	// A declined submission can be revised and resubmitted.
	act(t, s, work.ID, ClaimActionSubmit, ClaimActionParams{Site: "kungal", ActorUID: 7})
}

// TestClaimLifecycleUnbanDerivesPriorState pins ruling 4: the prior state comes
// from the event log, so no prior_state column is needed — and a work banned
// before any event existed unbans to live.
func TestClaimLifecycleUnbanDerivesPriorState(t *testing.T) {
	s := newLifecycle(t)

	// Banned from draft → unban returns to draft.
	fromDraft := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "下書き")
	claimWork(t, fromDraft.ID, "kungal", 7001)
	setClaimState(t, fromDraft.ID, i16(model.ClaimStateDraft))
	act(t, s, fromDraft.ID, ClaimActionBan, ClaimActionParams{ActorUID: 9, Reason: "spam"})
	if res := act(t, s, fromDraft.ID, ClaimActionUnban, ClaimActionParams{ActorUID: 9}); res.To != model.ClaimStateKeyDraft {
		t.Fatalf("unban after a draft ban: %+v", res)
	}

	// Banned from live → unban returns to live.
	fromLive := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "公開")
	claimWork(t, fromLive.ID, "kungal", 7002)
	setClaimState(t, fromLive.ID, i16(model.ClaimStateLive))
	act(t, s, fromLive.ID, ClaimActionBan, ClaimActionParams{ActorUID: 9, Reason: "dmca"})
	if res := act(t, s, fromLive.ID, ClaimActionUnban, ClaimActionParams{ActorUID: 9}); res.To != model.ClaimStateKeyLive {
		t.Fatalf("unban after a live ban: %+v", res)
	}

	// Hidden with NO ban event in the log (the pre-wave population) → live.
	legacy := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "旧封禁")
	claimWork(t, legacy.ID, "kungal", 7003)
	setClaimState(t, legacy.ID, i16(model.ClaimStateHidden))
	if res := act(t, s, legacy.ID, ClaimActionUnban, ClaimActionParams{ActorUID: 9}); res.To != model.ClaimStateKeyLive {
		t.Fatalf("unban with no history: %+v", res)
	}
}

// TestClaimLifecycleTenancy: a site moves its own claims and no others.
func TestClaimLifecycleTenancy(t *testing.T) {
	s := newLifecycle(t)
	work := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "他所の作品")
	claimWork(t, work.ID, "kungal", 8001)
	setClaimState(t, work.ID, i16(model.ClaimStateDraft))

	var owner *ClaimOwnershipError
	if _, err := s.Act(context.Background(), ClaimActionParams{
		WorkID: work.ID, Action: ClaimActionPublish, Site: "moyu", ActorUID: 3,
	}); !errors.As(err, &owner) {
		t.Fatalf("cross-tenant publish: %v", err)
	}
	// A curator carries no site and is not bound by that check.
	act(t, s, work.ID, ClaimActionBan, ClaimActionParams{ActorUID: 3, Reason: "policy"})
}

// TestClaimEventFeedCursor pins the feed contract: exclusive since, ascending,
// limit-bounded, and a next_since that never rewinds.
func TestClaimEventFeedCursor(t *testing.T) {
	s := newLifecycle(t)

	empty, err := s.EventsSince(context.Background(), 0, 10, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("empty feed: %+v", empty)
	}

	work := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "連番")
	product := int64(9100)
	act(t, s, work.ID, ClaimActionClaim, ClaimActionParams{Site: "kungal", ProductWorkID: &product, ActorUID: 1})
	act(t, s, work.ID, ClaimActionSubmit, ClaimActionParams{Site: "kungal", ActorUID: 1})
	act(t, s, work.ID, ClaimActionApprove, ClaimActionParams{ActorUID: 2})

	page, err := s.EventsSince(context.Background(), 0, 2, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 || page[0].ID >= page[1].ID {
		t.Fatalf("first page: %+v", page)
	}
	rest, err := s.EventsSince(context.Background(), page[1].ID, 10, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 1 || rest[0].ID <= page[1].ID {
		t.Fatalf("second page: %+v", rest)
	}
	// The site filter is the tenant's own lane.
	other, err := s.EventsSince(context.Background(), 0, 10, "moyu", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Fatalf("site filter: %+v", other)
	}
}

// TestPendingClaimsQueue: 03 定案 §3 says the review queue is
// "claim_state = pending", not a dedicated table. This is that query.
func TestPendingClaimsQueue(t *testing.T) {
	s := newLifecycle(t)
	states := map[int16]string{
		model.ClaimStatePending:  "審査待ち",
		model.ClaimStateDraft:    "下書き",
		model.ClaimStateLive:     "公開",
		model.ClaimStateDeclined: "却下",
		model.ClaimStateHidden:   "封禁",
	}
	var pendingID int64
	for state, name := range states {
		w := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, name)
		claimWork(t, w.ID, "kungal", 9500+int64(state))
		setClaimState(t, w.ID, i16(state))
		if state == model.ClaimStatePending {
			pendingID = w.ID
		}
	}
	items, total, err := s.PendingClaims(context.Background(), "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(items) != 1 || items[0].WorkID != pendingID {
		t.Fatalf("queue: total=%d items=%+v", total, items)
	}
}

// TestSearchWorksBBucketSupply pins the W4 supply (03 定案 §8-1) on the S2S
// picker: the B bucket — everything a submitter's own view must see — is ONE
// query, and an absent parameter is still the ungated wire every existing
// caller depends on. Supply first, switch consumers later.
func TestSearchWorksBBucketSupply(t *testing.T) {
	cleanTables(t)

	states := []int16{
		model.ClaimStateLive, model.ClaimStateDraft, model.ClaimStatePending,
		model.ClaimStateDeclined, model.ClaimStateHidden,
	}
	inBucket := map[int64]bool{}
	for i, st := range states {
		w := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "供給テスト")
		if err := testDB.Create(&model.CatalogWorkTitle{
			WorkID: w.ID, Lang: "ja", Title: "供給テスト", Kind: model.WorkTitleKindOfficial,
		}).Error; err != nil {
			t.Fatal(err)
		}
		claimWork(t, w.ID, "kungal", int64(9600+i))
		setClaimState(t, w.ID, i16(st))
		if st == model.ClaimStateLive || st == model.ClaimStateDraft || st == model.ClaimStatePending {
			inBucket[w.ID] = true
		}
	}
	// An unclaimed work: visible ungated, outside the bucket.
	free := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "供給テスト")
	if err := testDB.Create(&model.CatalogWorkTitle{
		WorkID: free.ID, Lang: "ja", Title: "供給テスト", Kind: model.WorkTitleKindOfficial,
	}).Error; err != nil {
		t.Fatal(err)
	}

	ungated, err := NewReadService(testDB).SearchWorks(context.Background(), "供給テスト", -1, 50, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ungated) != len(states)+1 {
		t.Fatalf("absent parameter must not gate: %d hits", len(ungated))
	}

	bucket, err := NewReadService(testDB).SearchWorks(context.Background(), "供給テスト", -1, 50,
		[]string{model.ClaimStateKeyLive, model.ClaimStateKeyDraft, model.ClaimStateKeyPending}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(bucket) != len(inBucket) {
		t.Fatalf("B bucket: %d hits, want %d", len(bucket), len(inBucket))
	}
	for _, h := range bucket {
		if !inBucket[h.WorkID] {
			t.Fatalf("B bucket leaked work %d", h.WorkID)
		}
	}
}

// ---- RequireOwner (wave 179) -----------------------------------------------

// ownerOfWork reads the stamped creator, which is the fact RequireOwner checks.
func ownerOfWork(t *testing.T, workID int64) *int64 {
	t.Helper()
	var got *int64
	if err := testDB.Raw(`SELECT owner_user_id FROM catalog_work WHERE id = ?`, workID).Scan(&got).Error; err != nil {
		t.Fatal(err)
	}
	return got
}

// TestClaimRequireOwner pins the user-face tooth: with RequireOwner set, the
// three owner actions are refused unless the caller IS the entry's stamped
// owner — and the S2S posture (the flag unset) is untouched by any of it.
func TestClaimRequireOwner(t *testing.T) {
	s := newLifecycle(t)
	work := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "所有者テスト")
	product := int64(4179)

	// `claim` never meets the check: it is the birth of the ownership, so a
	// caller with RequireOwner set claims a work nobody owns yet — and comes out
	// of it as the owner.
	act(t, s, work.ID, ClaimActionClaim, ClaimActionParams{
		Site: "kungal", ProductWorkID: &product, ActorUID: 71, RequireOwner: true,
	})
	owner := ownerOfWork(t, work.ID)
	if owner == nil || *owner != 71 {
		t.Fatalf("claim must stamp the claimant as owner: %v", owner)
	}

	// A different user, same tenant: the tenancy check passes and this one does
	// not. Nothing moved.
	_, err := s.Act(context.Background(), ClaimActionParams{
		WorkID: work.ID, Action: ClaimActionSubmit, Site: "kungal", ActorUID: 72, RequireOwner: true,
	})
	var notOwned *ClaimNotOwnedError
	if !errors.As(err, &notOwned) {
		t.Fatalf("a stranger's submit: %v", err)
	}
	if got := claimStateOfWork(t, work.ID); got == nil || *got != model.ClaimStateDraft {
		t.Fatalf("the refusal moved the claim: %v", got)
	}

	// The owner's own submit lands.
	act(t, s, work.ID, ClaimActionSubmit, ClaimActionParams{
		Site: "kungal", ActorUID: 71, RequireOwner: true,
	})
	if got := claimStateOfWork(t, work.ID); got == nil || *got != model.ClaimStatePending {
		t.Fatalf("the owner's submit: %v", got)
	}

	// The S2S plane is unchanged: without the flag, the very caller refused
	// above moves the same claim, because there the uid is asserted by a backend
	// that authenticated it and only the tenant is the registry's business.
	act(t, s, work.ID, ClaimActionWithdraw, ClaimActionParams{Site: "kungal", ActorUID: 72})
	if got := claimStateOfWork(t, work.ID); got == nil || *got != model.ClaimStateDraft {
		t.Fatalf("the S2S withdraw: %v", got)
	}

	// A work with NO owner refuses every owner action on the user face: an entry
	// nobody is recorded as having created is nobody's to move there.
	orphan := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "無主テスト")
	claimWork(t, orphan.ID, "kungal", 4180)
	if err := testDB.Exec(`UPDATE catalog_work SET claim_state = ? WHERE id = ?`,
		model.ClaimStateDraft, orphan.ID).Error; err != nil {
		t.Fatal(err)
	}
	_, err = s.Act(context.Background(), ClaimActionParams{
		WorkID: orphan.ID, Action: ClaimActionPublish, Site: "kungal", ActorUID: 71, RequireOwner: true,
	})
	if !errors.As(err, &notOwned) {
		t.Fatalf("an unowned work's publish: %v", err)
	}

	// …and the review actions ignore the flag entirely: their authority was
	// settled by the permission check at the face, not by ownership.
	act(t, s, work.ID, ClaimActionBan, ClaimActionParams{Site: "kungal", ActorUID: 99, RequireOwner: true})
	if got := claimStateOfWork(t, work.ID); got == nil || *got != model.ClaimStateHidden {
		t.Fatalf("ban with RequireOwner: %v", got)
	}
}
