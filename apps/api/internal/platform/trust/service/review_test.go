package service

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"

	"api/internal/platform/trust/model"
)

// mkOpenItem inserts a fresh pending review item and returns its id.
func mkOpenItem(t *testing.T, subject string) int64 {
	t.Helper()
	it := model.TrustReviewItem{
		Site: tSite, SubjectKind: tKind, SubjectID: subject,
		Source: model.ReviewSourceReports, Priority: 2, Status: model.ReviewStatusPending,
	}
	if err := testDB.Create(&it).Error; err != nil {
		t.Fatalf("insert open item: %v", err)
	}
	return it.ID
}

// mkOpenItemInSite inserts a fresh pending item for an arbitrary site.
func mkOpenItemInSite(t *testing.T, site, subject string) int64 {
	t.Helper()
	it := model.TrustReviewItem{
		Site: site, SubjectKind: tKind, SubjectID: subject,
		Source: model.ReviewSourceReports, Priority: 2, Status: model.ReviewStatusPending,
	}
	if err := testDB.Create(&it).Error; err != nil {
		t.Fatalf("insert open item in %s: %v", site, err)
	}
	return it.ID
}

// Step 04: the site-scope primitive on Get/Claim/Decide/List. A non-empty
// siteScope confines the op to that site; a foreign-site item reads as
// ErrReviewItemNotFound (never AlreadyClaimed), so existence never leaks. An
// empty scope (platform staff) is unrestricted.
func TestReviewSiteScopeEnforcement(t *testing.T) {
	cleanTables(t)
	svc := NewReviewService(testDB)
	ownID := mkOpenItemInSite(t, "kungal", "own1")
	foreignID := mkOpenItemInSite(t, "otokun", "for1")

	// Get: own-site scope and unrestricted both see the item; foreign scope 404.
	if _, _, err := svc.Get(context.Background(), ownID, "kungal"); err != nil {
		t.Fatalf("get own-site scoped: %v", err)
	}
	if _, _, err := svc.Get(context.Background(), foreignID, ""); err != nil {
		t.Fatalf("get foreign item unrestricted: %v", err)
	}
	if _, _, err := svc.Get(context.Background(), foreignID, "kungal"); !errors.Is(err, ErrReviewItemNotFound) {
		t.Fatalf("get foreign-site scoped: want ErrReviewItemNotFound, got %v", err)
	}

	// Claim: a foreign-site item is NotFound (404), not AlreadyClaimed (409).
	if err := svc.Claim(context.Background(), foreignID, 1, "kungal"); !errors.Is(err, ErrReviewItemNotFound) {
		t.Fatalf("claim foreign-site scoped: want ErrReviewItemNotFound, got %v", err)
	}
	if err := svc.Claim(context.Background(), ownID, 1, "kungal"); err != nil {
		t.Fatalf("claim own-site scoped: %v", err)
	}

	// Decide: foreign-site item is NotFound; own-site (now claimed) decides fine.
	if _, err := svc.Decide(context.Background(), DecideParams{
		ID: foreignID, DecidedBy: 1, Decision: "dismissed", SiteScope: "kungal",
	}); !errors.Is(err, ErrReviewItemNotFound) {
		t.Fatalf("decide foreign-site scoped: want ErrReviewItemNotFound, got %v", err)
	}
	if _, err := svc.Decide(context.Background(), DecideParams{
		ID: ownID, DecidedBy: 1, Decision: "dismissed", SiteScope: "kungal",
	}); err != nil {
		t.Fatalf("decide own-site scoped: %v", err)
	}

	// List with a site filter returns only that site's items.
	items, _, err := svc.List(context.Background(), ReviewFilters{Site: "otokun"})
	if err != nil {
		t.Fatalf("list otokun: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("list otokun returned nothing")
	}
	for _, it := range items {
		if it.Site != "otokun" {
			t.Fatalf("site filter leaked site %q", it.Site)
		}
	}
}

// E6: two claimers race one pending item → exactly one wins (SKIP LOCKED).
func TestClaimConcurrency(t *testing.T) {
	cleanTables(t)
	id := mkOpenItem(t, "claimrace")
	svc := NewReviewService(testDB)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, claimer := range []int64{7, 8} {
		wg.Add(1)
		go func(idx int, by int64) {
			defer wg.Done()
			errs[idx] = svc.Claim(context.Background(), id, by, "")
		}(i, claimer)
	}
	wg.Wait()

	wins, conflicts := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, ErrAlreadyClaimed):
			conflicts++
		default:
			t.Fatalf("unexpected claim error: %v", err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("exactly one claim must win: wins=%d conflicts=%d", wins, conflicts)
	}
	if getItem(t, id).Status != model.ReviewStatusClaimed {
		t.Fatalf("claimed item status = %d, want claimed", getItem(t, id).Status)
	}
}

func TestClaimNotFound(t *testing.T) {
	cleanTables(t)
	if err := NewReviewService(testDB).Claim(context.Background(), 999999, 1, ""); !errors.Is(err, ErrReviewItemNotFound) {
		t.Fatalf("claim absent item: want ErrReviewItemNotFound, got %v", err)
	}
}

// E7: the decide state machine + disposition + audit-chain continuity.
func TestDecideStateMachineAndAudit(t *testing.T) {
	cleanTables(t)
	svc := NewReviewService(testDB)

	// actioned on a kind WITHOUT a callback → disposition.callback_status NULL.
	registerKind(t, tSite, tKind, nil, nil)
	id1 := mkOpenItem(t, "d1")
	action := model.ActionRemove
	dispID, err := svc.Decide(context.Background(), DecideParams{
		ID: id1, DecidedBy: 5, Decision: "actioned", Action: &action, ReasonCode: "abuse.remove",
	})
	if err != nil {
		t.Fatalf("actioned decide: %v", err)
	}
	if dispID == nil {
		t.Fatal("actioned decide must return a disposition id")
	}
	if getItem(t, id1).Status != model.ReviewStatusActioned {
		t.Fatalf("item status after action = %d, want actioned", getItem(t, id1).Status)
	}
	var disp model.TrustDisposition
	if err := testDB.Take(&disp, *dispID).Error; err != nil {
		t.Fatalf("load disposition: %v", err)
	}
	if disp.CallbackStatus != nil {
		t.Fatalf("no callback_url → callback_status must be NULL, got %v", *disp.CallbackStatus)
	}

	// Re-deciding a terminal item is an illegal transition.
	if _, err := svc.Decide(context.Background(), DecideParams{
		ID: id1, DecidedBy: 5, Decision: "dismissed",
	}); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("re-decide: want ErrIllegalTransition, got %v", err)
	}

	// A second decision (dismiss) on a fresh item extends the audit chain.
	id2 := mkOpenItem(t, "d2")
	if _, err := svc.Decide(context.Background(), DecideParams{
		ID: id2, DecidedBy: 6, Decision: "dismissed",
	}); err != nil {
		t.Fatalf("dismiss decide: %v", err)
	}

	// Audit chain: row1.prev = 32 zero bytes; each row's prev == the previous
	// row's hash (章程 ruling 10).
	var rows []model.TrustAuditLog
	if err := testDB.Order("id ASC").Find(&rows).Error; err != nil {
		t.Fatalf("load audit rows: %v", err)
	}
	if len(rows) < 2 {
		t.Fatalf("expected at least 2 audit rows, got %d", len(rows))
	}
	if !bytes.Equal(rows[0].PrevHash, make([]byte, 32)) {
		t.Fatalf("genesis prev_hash must be 32 zero bytes, got %x", rows[0].PrevHash)
	}
	for i := 1; i < len(rows); i++ {
		if !bytes.Equal(rows[i].PrevHash, rows[i-1].Hash) {
			t.Fatalf("audit chain broken at row %d: prev != previous hash", i)
		}
	}
}

// TestDecideInvalid pins the malformed-actioned guard.
func TestDecideInvalid(t *testing.T) {
	cleanTables(t)
	id := mkOpenItem(t, "inv")
	svc := NewReviewService(testDB)
	// actioned without action/reason_code.
	if _, err := svc.Decide(context.Background(), DecideParams{ID: id, DecidedBy: 1, Decision: "actioned"}); !errors.Is(err, ErrInvalidDecision) {
		t.Fatalf("actioned without action: want ErrInvalidDecision, got %v", err)
	}
	// unknown decision.
	if _, err := svc.Decide(context.Background(), DecideParams{ID: id, DecidedBy: 1, Decision: "maybe"}); !errors.Is(err, ErrInvalidDecision) {
		t.Fatalf("unknown decision: want ErrInvalidDecision, got %v", err)
	}
}
