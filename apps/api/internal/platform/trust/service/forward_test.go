package service

import (
	"context"
	"errors"
	"testing"

	"api/internal/platform/trust/model"
)

const fwdClient = "letmoe-community"

func allowFwd() map[string]bool { return map[string]bool{fwdClient: true} }

func fwdParams(subject string) ForwardParams {
	note := "[flags] post #7 by user 42: spam spam spam"
	w := float32(3.0)
	return ForwardParams{
		CallerClientID: fwdClient, Site: tSite, SubjectKind: "community_post", SubjectID: subject,
		WeightSum: &w, ContextNote: &note,
	}
}

func TestForwardAllowlist(t *testing.T) {
	cleanTables(t)
	registerKind(t, tSite, "community_post", nil, nil)

	closed := NewForwardService(testDB, nil)
	if _, err := closed.Forward(context.Background(), fwdParams("a1")); !errors.Is(err, ErrForwarderNotAllowed) {
		t.Fatalf("empty allowlist: want ErrForwarderNotAllowed, got %v", err)
	}

	svc := NewForwardService(testDB, allowFwd())
	p := fwdParams("a2")
	p.CallerClientID = "stranger"
	if _, err := svc.Forward(context.Background(), p); !errors.Is(err, ErrForwarderNotAllowed) {
		t.Fatalf("stranger: want ErrForwarderNotAllowed, got %v", err)
	}
	if _, err := svc.Forward(context.Background(), fwdParams("a3")); err != nil {
		t.Fatalf("listed client forward: %v", err)
	}
}

func TestForwardUnregisteredKind(t *testing.T) {
	cleanTables(t)
	svc := NewForwardService(testDB, allowFwd())
	if _, err := svc.Forward(context.Background(), fwdParams("u1")); !errors.Is(err, ErrSubjectKindNotRegistered) {
		t.Fatalf("unregistered kind: want ErrSubjectKindNotRegistered, got %v", err)
	}
}

func TestForwardCreateAndIdempotent(t *testing.T) {
	cleanTables(t)
	registerKind(t, tSite, "community_post", nil, nil)
	svc := NewForwardService(testDB, allowFwd())

	res, err := svc.Forward(context.Background(), fwdParams("s1"))
	if err != nil {
		t.Fatalf("forward create: %v", err)
	}
	if !res.Created {
		t.Fatal("first forward must create")
	}
	it := getItem(t, res.ReviewItemID)
	if it.Source != model.ReviewSourceCommunityForward {
		t.Fatalf("source = %d, want community_forward", it.Source)
	}
	if it.ContextNote == nil || *it.ContextNote == "" {
		t.Fatal("context_note not stored")
	}
	if it.ReportWeightSum == nil || *it.ReportWeightSum != 3.0 {
		t.Fatalf("weight_sum = %v, want 3.0", it.ReportWeightSum)
	}
	firstNote := *it.ContextNote

	p := fwdParams("s1")
	w := float32(5.0)
	p.WeightSum = &w
	other := "different note"
	p.ContextNote = &other
	res2, err := svc.Forward(context.Background(), p)
	if err != nil {
		t.Fatalf("forward idempotent: %v", err)
	}
	if res2.Created || res2.ReviewItemID != res.ReviewItemID {
		t.Fatalf("second forward must adopt the open item: %+v", res2)
	}
	it2 := getItem(t, res.ReviewItemID)
	if it2.ReportWeightSum == nil || *it2.ReportWeightSum != 5.0 {
		t.Fatalf("weight_sum after update = %v, want 5.0 (max)", it2.ReportWeightSum)
	}
	if it2.ContextNote == nil || *it2.ContextNote != firstNote {
		t.Fatalf("context_note must not be clobbered: got %v, want %q", it2.ContextNote, firstNote)
	}
}

func TestForwardResolve(t *testing.T) {
	cleanTables(t)
	registerKind(t, tSite, "community_post", nil, nil)
	svc := NewForwardService(testDB, allowFwd())

	r1, _ := svc.Forward(context.Background(), fwdParams("r1"))
	actor := "moderator-9"
	res, err := svc.Resolve(context.Background(), ResolveParams{
		CallerClientID: fwdClient, ReviewItemID: r1.ReviewItemID, Outcome: "approved", ActorRef: &actor,
	})
	if err != nil || !res.Closed {
		t.Fatalf("resolve approved: err=%v closed=%v", err, res.Closed)
	}
	if getItem(t, r1.ReviewItemID).Status != model.ReviewStatusDismissed {
		t.Fatalf("approved → status %d, want dismissed", getItem(t, r1.ReviewItemID).Status)
	}

	r2, _ := svc.Forward(context.Background(), fwdParams("r2"))
	if res, err := svc.Resolve(context.Background(), ResolveParams{
		CallerClientID: fwdClient, ReviewItemID: r2.ReviewItemID, Outcome: "rejected",
	}); err != nil || !res.Closed {
		t.Fatalf("resolve rejected: err=%v closed=%v", err, res.Closed)
	}
	if getItem(t, r2.ReviewItemID).Status != model.ReviewStatusActioned {
		t.Fatalf("rejected → status %d, want actioned", getItem(t, r2.ReviewItemID).Status)
	}

	var disp int64
	testDB.Model(&model.TrustDisposition{}).Count(&disp)
	if disp != 0 {
		t.Fatalf("resolve must not create dispositions, found %d", disp)
	}
	var audits []model.TrustAuditLog
	testDB.Where("action = ?", "review_resolved_by_site").Order("id ASC").Find(&audits)
	if len(audits) != 2 {
		t.Fatalf("expected 2 resolve audit rows, got %d", len(audits))
	}
	if audits[0].PolicyRef == nil || *audits[0].PolicyRef != actor {
		t.Fatalf("actor_ref not recorded in policy_ref: %v", audits[0].PolicyRef)
	}

	res, err = svc.Resolve(context.Background(), ResolveParams{
		CallerClientID: fwdClient, ReviewItemID: r1.ReviewItemID, Outcome: "rejected",
	})
	if err != nil {
		t.Fatalf("resolve terminal: %v", err)
	}
	if res.Closed {
		t.Fatal("resolving a terminal item must report closed:false")
	}
}

func TestForwardResolveGuards(t *testing.T) {
	cleanTables(t)
	svc := NewForwardService(testDB, allowFwd())
	if _, err := svc.Resolve(context.Background(), ResolveParams{CallerClientID: "x", ReviewItemID: 1, Outcome: "approved"}); !errors.Is(err, ErrForwarderNotAllowed) {
		t.Fatalf("resolve not allowed: %v", err)
	}
	if _, err := svc.Resolve(context.Background(), ResolveParams{CallerClientID: fwdClient, ReviewItemID: 1, Outcome: "maybe"}); !errors.Is(err, ErrInvalidOutcome) {
		t.Fatalf("resolve bad outcome: %v", err)
	}
	if _, err := svc.Resolve(context.Background(), ResolveParams{CallerClientID: fwdClient, ReviewItemID: 999999, Outcome: "approved"}); !errors.Is(err, ErrReviewItemNotFound) {
		t.Fatalf("resolve absent: %v", err)
	}
}

func TestDecideDismissNotify(t *testing.T) {
	cleanTables(t)
	url := "http://community:9282/trust/callback"
	sec := "secret"

	kindOn := model.TrustSubjectKind{Site: tSite, Key: "community_post", CallbackURL: &url, CallbackSecret: &sec, NotifyOnDismiss: true}
	if err := testDB.Create(&kindOn).Error; err != nil {
		t.Fatalf("create kind: %v", err)
	}
	item := model.TrustReviewItem{Site: tSite, SubjectKind: "community_post", SubjectID: "n1",
		Source: model.ReviewSourceCommunityForward, Priority: 1, Status: model.ReviewStatusPending}
	testDB.Create(&item)

	dispID, err := NewReviewService(testDB).Decide(context.Background(), DecideParams{
		ID: item.ID, DecidedBy: 3, Decision: "dismissed",
	})
	if err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	if dispID == nil {
		t.Fatal("notify_on_dismiss kind must produce a disposition on dismiss")
	}
	var disp model.TrustDisposition
	testDB.Take(&disp, *dispID)
	if disp.Action != model.ActionNone || disp.ReasonCode != "review_dismissed" {
		t.Fatalf("dismiss disposition = action %d reason %q, want 0/review_dismissed", disp.Action, disp.ReasonCode)
	}
	if disp.CallbackStatus == nil || *disp.CallbackStatus != model.CallbackStatusPending {
		t.Fatalf("dismiss disposition callback not pending: %v", disp.CallbackStatus)
	}

	cleanTables(t)
	kindOff := model.TrustSubjectKind{Site: tSite, Key: "community_post", CallbackURL: &url, CallbackSecret: &sec, NotifyOnDismiss: false}
	testDB.Create(&kindOff)
	item2 := model.TrustReviewItem{Site: tSite, SubjectKind: "community_post", SubjectID: "n2",
		Source: model.ReviewSourceCommunityForward, Priority: 1, Status: model.ReviewStatusPending}
	testDB.Create(&item2)
	dispID2, err := NewReviewService(testDB).Decide(context.Background(), DecideParams{
		ID: item2.ID, DecidedBy: 3, Decision: "dismissed",
	})
	if err != nil {
		t.Fatalf("dismiss (off): %v", err)
	}
	if dispID2 != nil {
		t.Fatal("kind without notify_on_dismiss must not produce a disposition")
	}
}
