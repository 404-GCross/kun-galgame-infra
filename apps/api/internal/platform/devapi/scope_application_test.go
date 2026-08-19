package devapi

import (
	"context"
	"strings"
	"testing"
)

func cleanupScopeApps(t *testing.T) {
	t.Helper()
	if err := testDB.Exec(`TRUNCATE devapi_scope_applications RESTART IDENTITY`).Error; err != nil {
		t.Fatalf("truncate scope applications: %v", err)
	}
}

func TestScopeApplicationGatesMint(t *testing.T) {
	cleanupSelf(t)
	cleanupScopeApps(t)
	svc, admin, _, _ := newSelfService(t)
	ctx := context.Background()
	const owner, reviewer = uint(1), uint(9)

	app, err := svc.CreateApp(ctx, owner, "news reader", "", nil)
	if err != nil {
		t.Fatalf("create app: %v", err)
	}

	if _, _, err := svc.MintKey(ctx, owner, app.ID, MintKeyInput{
		Name: "no application", Scopes: []string{ScopeNewsRead},
	}); err != ErrScopeNeedsGrant {
		t.Fatalf("mint without an application = %v, want ErrScopeNeedsGrant", err)
	}

	filed, err := svc.ApplyForScope(ctx, owner, ScopeNewsRead, "aggregating release news for a fan wiki")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if filed.Status != ScopeAppPending {
		t.Fatalf("filed status = %q, want %q", filed.Status, ScopeAppPending)
	}
	if _, _, err := svc.MintKey(ctx, owner, app.ID, MintKeyInput{
		Name: "pending", Scopes: []string{ScopeNewsRead},
	}); err != ErrScopeNeedsGrant {
		t.Fatalf("mint while pending = %v, want ErrScopeNeedsGrant", err)
	}

	declined, err := admin.DeclineScopeApplication(ctx, filed.ID, reviewer, "no partner coverage for that use")
	if err != nil {
		t.Fatalf("decline: %v", err)
	}
	if declined.Status != ScopeAppDeclined || declined.DeclineReason == "" {
		t.Fatalf("declined row = %+v, want declined with a reason", declined)
	}
	if _, _, err := svc.MintKey(ctx, owner, app.ID, MintKeyInput{
		Name: "declined", Scopes: []string{ScopeNewsRead},
	}); err != ErrScopeNeedsGrant {
		t.Fatalf("mint while declined = %v, want ErrScopeNeedsGrant", err)
	}

	// A declined applicant re-applies into the SAME row (the (user, scope)
	// unique index leaves no second row to file), and the review fields must be
	// wiped or the reviewer sees a stale verdict on a fresh request.
	reapplied, err := svc.ApplyForScope(ctx, owner, ScopeNewsRead, "narrowed to two partners, weekly poll")
	if err != nil {
		t.Fatalf("re-apply after decline: %v", err)
	}
	if reapplied.ID != filed.ID {
		t.Errorf("re-apply created row %d, want the original %d", reapplied.ID, filed.ID)
	}
	if reapplied.Status != ScopeAppPending || reapplied.DeclineReason != "" ||
		reapplied.ReviewerID != nil || reapplied.ReviewedAt != nil {
		t.Errorf("re-applied row = %+v, want pending with the review fields cleared", reapplied)
	}

	approved, err := admin.ApproveScopeApplication(ctx, filed.ID, reviewer)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if approved.Status != ScopeAppApproved || approved.ReviewerID == nil || *approved.ReviewerID != reviewer {
		t.Fatalf("approved row = %+v, want approved by %d", approved, reviewer)
	}

	key, _, err := svc.MintKey(ctx, owner, app.ID, MintKeyInput{
		Name: "granted", Scopes: []string{ScopeCatalogRead, ScopeNewsRead},
	})
	if err != nil {
		t.Fatalf("mint after approval: %v", err)
	}
	if key == nil {
		t.Fatal("mint after approval returned no key")
	}

	// The grant is per user, not per platform: a second owner with their own app
	// is still refused, and the refusal must be the grant error rather than the
	// ownership 404 that would mask it.
	const other = uint(2)
	otherApp, err := svc.CreateApp(ctx, other, "another reader", "", nil)
	if err != nil {
		t.Fatalf("create other app: %v", err)
	}
	if _, _, err := svc.MintKey(ctx, other, otherApp.ID, MintKeyInput{
		Name: "someone else", Scopes: []string{ScopeNewsRead},
	}); err != ErrScopeNeedsGrant {
		t.Errorf("another owner minting on this grant = %v, want ErrScopeNeedsGrant", err)
	}
}

func TestScopeApplicationValidation(t *testing.T) {
	cleanupSelf(t)
	cleanupScopeApps(t)
	svc, _, _, _ := newSelfService(t)
	ctx := context.Background()
	const owner = uint(1)

	for _, scope := range []string{ScopeCatalogRead, ScopeGalgameRead, ScopeGalgameNSFW, "nonsense"} {
		if _, err := svc.ApplyForScope(ctx, owner, scope, "please"); err != ErrScopeNotGrantable {
			t.Errorf("apply for %q = %v, want ErrScopeNotGrantable", scope, err)
		}
	}
	if _, err := svc.ApplyForScope(ctx, owner, ScopeNewsRead, "   "); err != ErrScopeAppMessage {
		t.Errorf("blank message = %v, want ErrScopeAppMessage", err)
	}
	if _, err := svc.ApplyForScope(ctx, owner, ScopeNewsRead, strings.Repeat("x", maxScopeAppMessageLen+1)); err != ErrScopeAppMsgTooLong {
		t.Errorf("over-long ASCII message = %v, want ErrScopeAppMsgTooLong", err)
	}

	// The cap is runes, not bytes. A byte cap would reject this at ~666
	// characters, which is what a Chinese-language applicant would actually hit
	// while the UI and the error text both say 2000.
	full := strings.Repeat("说", maxScopeAppMessageLen)
	if len(full) <= maxScopeAppMessageLen {
		t.Fatalf("fixture is not multi-byte: %d bytes for %d runes", len(full), maxScopeAppMessageLen)
	}
	if _, err := svc.ApplyForScope(ctx, owner, ScopeNewsRead, strings.Repeat("说", maxScopeAppMessageLen+1)); err != ErrScopeAppMsgTooLong {
		t.Errorf("2001 Chinese characters = %v, want ErrScopeAppMsgTooLong", err)
	}
	if _, err := svc.ApplyForScope(ctx, owner, ScopeNewsRead, full); err != nil {
		t.Errorf("exactly 2000 Chinese characters (%d bytes) = %v, want accepted", len(full), err)
	}
}

func TestScopeApplicationOnePerUserAndScope(t *testing.T) {
	cleanupSelf(t)
	cleanupScopeApps(t)
	svc, admin, _, _ := newSelfService(t)
	ctx := context.Background()
	const owner, reviewer = uint(1), uint(9)

	filed, err := svc.ApplyForScope(ctx, owner, ScopeNewsRead, "first ask")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := svc.ApplyForScope(ctx, owner, ScopeNewsRead, "second ask"); err != ErrScopeAppPending {
		t.Fatalf("re-apply while pending = %v, want ErrScopeAppPending", err)
	}

	if _, err := admin.ApproveScopeApplication(ctx, filed.ID, reviewer); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := svc.ApplyForScope(ctx, owner, ScopeNewsRead, "again"); err != ErrScopeAppApproved {
		t.Fatalf("re-apply while approved = %v, want ErrScopeAppApproved", err)
	}
	if _, err := admin.ApproveScopeApplication(ctx, filed.ID, reviewer); err != ErrScopeAppNotPending {
		t.Fatalf("re-review a decided application = %v, want ErrScopeAppNotPending", err)
	}
	if _, err := admin.DeclineScopeApplication(ctx, filed.ID, reviewer, ""); err != ErrScopeAppNeedsReason {
		t.Fatalf("decline without a reason = %v, want ErrScopeAppNeedsReason", err)
	}
	// Same rune-not-byte cap as the applicant's message.
	if _, err := admin.DeclineScopeApplication(ctx, filed.ID, reviewer, strings.Repeat("说", maxScopeAppMessageLen+1)); err != ErrScopeAppMsgTooLong {
		t.Fatalf("over-long decline reason = %v, want ErrScopeAppMsgTooLong", err)
	}

	mine, err := svc.ListScopeApplications(ctx, owner)
	if err != nil {
		t.Fatalf("list mine: %v", err)
	}
	if len(mine) != 1 || mine[0].Scope != ScopeNewsRead || mine[0].Status != ScopeAppApproved {
		t.Fatalf("owner list = %+v, want one approved news:read row", mine)
	}
	if other, _ := svc.ListScopeApplications(ctx, uint(2)); len(other) != 0 {
		t.Errorf("another user sees %d applications, want 0", len(other))
	}

	pending, err := admin.ListScopeApplications(ctx, ScopeAppPending)
	if err != nil {
		t.Fatalf("admin list pending: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("pending queue = %+v, want empty after the approval", pending)
	}
	all, err := admin.ListScopeApplications(ctx, "")
	if err != nil {
		t.Fatalf("admin list all: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("admin list all = %d rows, want 1", len(all))
	}
}
