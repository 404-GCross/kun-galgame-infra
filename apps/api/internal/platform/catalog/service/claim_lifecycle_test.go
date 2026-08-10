package service

import (
	"context"
	"errors"
	"testing"

	"api/internal/platform/catalog/model"
)

func newLifecycle(t *testing.T) *ClaimLifecycleService {
	t.Helper()
	cleanTables(t)
	if err := testDB.Exec("TRUNCATE catalog_claim_event RESTART IDENTITY").Error; err != nil {
		t.Fatalf("truncate events: %v", err)
	}
	return NewClaimLifecycleService(testDB)
}

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
	if events[2].ProductWorkID == nil || *events[2].ProductWorkID != product {
		t.Fatalf("feed snapshot: %+v", events[2])
	}
	if events[2].ActorUID != 99 {
		t.Fatalf("actor: %+v", events[2])
	}
}

func TestClaimLifecycleIllegalTransition(t *testing.T) {
	s := newLifecycle(t)
	work := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "公開作品")
	claimWork(t, work.ID, "kungal", 5150)
	setClaimState(t, work.ID, i16(model.ClaimStateLive))

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

	free := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "未claim")
	if _, err := s.Act(context.Background(), ClaimActionParams{WorkID: free.ID, Action: ClaimActionSubmit, Site: "kungal"}); !errors.As(err, &conflict) {
		t.Fatalf("submit on an unclaimed work: %v", err)
	}
}

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
	act(t, s, work.ID, ClaimActionSubmit, ClaimActionParams{Site: "kungal", ActorUID: 7})
}

func TestClaimLifecycleUnbanDerivesPriorState(t *testing.T) {
	s := newLifecycle(t)

	fromDraft := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "下書き")
	claimWork(t, fromDraft.ID, "kungal", 7001)
	setClaimState(t, fromDraft.ID, i16(model.ClaimStateDraft))
	act(t, s, fromDraft.ID, ClaimActionBan, ClaimActionParams{ActorUID: 9, Reason: "spam"})
	if res := act(t, s, fromDraft.ID, ClaimActionUnban, ClaimActionParams{ActorUID: 9}); res.To != model.ClaimStateKeyDraft {
		t.Fatalf("unban after a draft ban: %+v", res)
	}

	fromLive := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "公開")
	claimWork(t, fromLive.ID, "kungal", 7002)
	setClaimState(t, fromLive.ID, i16(model.ClaimStateLive))
	act(t, s, fromLive.ID, ClaimActionBan, ClaimActionParams{ActorUID: 9, Reason: "dmca"})
	if res := act(t, s, fromLive.ID, ClaimActionUnban, ClaimActionParams{ActorUID: 9}); res.To != model.ClaimStateKeyLive {
		t.Fatalf("unban after a live ban: %+v", res)
	}

	legacy := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "旧封禁")
	claimWork(t, legacy.ID, "kungal", 7003)
	setClaimState(t, legacy.ID, i16(model.ClaimStateHidden))
	if res := act(t, s, legacy.ID, ClaimActionUnban, ClaimActionParams{ActorUID: 9}); res.To != model.ClaimStateKeyLive {
		t.Fatalf("unban with no history: %+v", res)
	}
}

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
	act(t, s, work.ID, ClaimActionBan, ClaimActionParams{ActorUID: 3, Reason: "policy"})
}

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
	other, err := s.EventsSince(context.Background(), 0, 10, "moyu", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Fatalf("site filter: %+v", other)
	}
}

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

func ownerOfWork(t *testing.T, workID int64) *int64 {
	t.Helper()
	var got *int64
	if err := testDB.Raw(`SELECT owner_user_id FROM catalog_work WHERE id = ?`, workID).Scan(&got).Error; err != nil {
		t.Fatal(err)
	}
	return got
}

func TestClaimRequireOwner(t *testing.T) {
	s := newLifecycle(t)
	work := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "所有者テスト")
	product := int64(4179)

	act(t, s, work.ID, ClaimActionClaim, ClaimActionParams{
		Site: "kungal", ProductWorkID: &product, ActorUID: 71, RequireOwner: true,
	})
	owner := ownerOfWork(t, work.ID)
	if owner == nil || *owner != 71 {
		t.Fatalf("claim must stamp the claimant as owner: %v", owner)
	}

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

	act(t, s, work.ID, ClaimActionSubmit, ClaimActionParams{
		Site: "kungal", ActorUID: 71, RequireOwner: true,
	})
	if got := claimStateOfWork(t, work.ID); got == nil || *got != model.ClaimStatePending {
		t.Fatalf("the owner's submit: %v", got)
	}

	act(t, s, work.ID, ClaimActionWithdraw, ClaimActionParams{Site: "kungal", ActorUID: 72})
	if got := claimStateOfWork(t, work.ID); got == nil || *got != model.ClaimStateDraft {
		t.Fatalf("the S2S withdraw: %v", got)
	}

	orphan := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "無主テスト")
	claimWork(t, orphan.ID, "kungal", 4180)
	if err := testDB.Exec(`UPDATE catalog_work SET claim_state = ? WHERE id = ?`,
		model.ClaimStateDraft, orphan.ID).Error; err != nil {
		t.Fatal(err)
	}
	if ownerOfWork(t, orphan.ID) != nil {
		t.Fatal("the fixture must start ownerless")
	}
	act(t, s, orphan.ID, ClaimActionPublish, ClaimActionParams{
		Site: "kungal", ActorUID: 73, RequireOwner: true,
	})
	if got := claimStateOfWork(t, orphan.ID); got == nil || *got != model.ClaimStateLive {
		t.Fatalf("the first claimant's publish: %v", got)
	}
	adopted := ownerOfWork(t, orphan.ID)
	if adopted == nil || *adopted != 73 {
		t.Fatalf("publishing a free claim must adopt it: %v", adopted)
	}

	_, err = s.Act(context.Background(), ClaimActionParams{
		WorkID: orphan.ID, Action: ClaimActionWithdraw, Site: "kungal", ActorUID: 74, RequireOwner: true,
	})
	if !errors.As(err, &notOwned) {
		t.Fatalf("a second claimant after adoption: %v", err)
	}
	if got := claimStateOfWork(t, orphan.ID); got == nil || *got != model.ClaimStateLive {
		t.Fatalf("the refusal moved the adopted claim: %v", got)
	}

	free := createWorkX(t, galgameMediumID, model.ContentRatingAllAges, model.WorkStatusLive, "S2S無主")
	claimWork(t, free.ID, "kungal", 4181)
	if err := testDB.Exec(`UPDATE catalog_work SET claim_state = ? WHERE id = ?`,
		model.ClaimStateDraft, free.ID).Error; err != nil {
		t.Fatal(err)
	}
	act(t, s, free.ID, ClaimActionPublish, ClaimActionParams{Site: "kungal", ActorUID: 75})
	if owner := ownerOfWork(t, free.ID); owner != nil {
		t.Fatalf("the S2S path must not stamp an owner: %v", *owner)
	}

	act(t, s, work.ID, ClaimActionBan, ClaimActionParams{Site: "kungal", ActorUID: 99, RequireOwner: true})
	if got := claimStateOfWork(t, work.ID); got == nil || *got != model.ClaimStateHidden {
		t.Fatalf("ban with RequireOwner: %v", got)
	}
}
