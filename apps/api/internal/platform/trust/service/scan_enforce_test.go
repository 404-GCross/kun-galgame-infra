package service

import (
	"context"
	"strings"
	"testing"

	"api/internal/platform/trust/model"
)

// liveWorker builds a scan worker in live mode over a gateway that convicts with
// the given score/categories.
func liveWorker(score float32, categories ...string) *ScanWorker {
	return NewScanWorker(testDB, &fakeGateway{
		configured: true,
		verdict: GatewayVerdict{
			Flagged: true, Score: f32(score), Categories: categories, Channel: "test-channel",
		},
	}, stubTier0{}, WithScanMode("live"))
}

func countRows(t *testing.T, dest any, where ...any) int64 {
	t.Helper()
	var n int64
	q := testDB.Model(dest)
	if len(where) > 0 {
		q = q.Where(where[0], where[1:]...)
	}
	if err := q.Count(&n).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// TestScanShadowModeEnforcesNothing pins the invariant wave 07 must not break:
// with the default posture a conviction still records a verdict and NOTHING
// else. This is the regression guard on the whole feature — if live-mode wiring
// ever leaks into the default path, this fails.
func TestScanShadowModeEnforcesNothing(t *testing.T) {
	cleanTables(t)
	registerKind(t, tSite, tKind, strptr("http://product.invalid/cb"), strptr("s3cr3t"))
	seedPending(t, "shadow-1", "convict me")

	// Same gateway, same conviction — only the mode differs from the live tests.
	w := NewScanWorker(testDB, &fakeGateway{
		configured: true,
		verdict:    GatewayVerdict{Flagged: true, Score: f32(0.9), Channel: "test-channel"},
	}, stubTier0{})

	if _, err := w.ScorePending(context.Background()); err != nil {
		t.Fatalf("score pending: %v", err)
	}

	if n := countScanStatus(t, model.ScanStatusScored); n != 1 {
		t.Fatalf("scored rows = %d, want 1", n)
	}
	if n := countRows(t, &model.TrustReviewItem{}); n != 0 {
		t.Fatalf("shadow mode opened %d review items, want 0", n)
	}
	if n := countRows(t, &model.TrustDisposition{}); n != 0 {
		t.Fatalf("shadow mode queued %d dispositions, want 0", n)
	}
}

// TestScanLiveModeOpensItemAndQueuesHide is the P2 happy path: a conviction
// lands one ai_text item carrying the classifier evidence plus one hide
// disposition queued for callback delivery.
func TestScanLiveModeOpensItemAndQueuesHide(t *testing.T) {
	cleanTables(t)
	registerKind(t, tSite, tKind, strptr("http://product.invalid/cb"), strptr("s3cr3t"))
	author := int64(4242)
	r := model.TrustScanResult{
		Site: tSite, SubjectKind: tKind, SubjectID: "live-1", AuthorID: &author,
		ContentText: "something the classifier convicts",
		Status:      model.ScanStatusPending, Mode: model.ScanModeShadow,
	}
	if err := testDB.Create(&r).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := liveWorker(0.87, "harassment").ScorePending(context.Background()); err != nil {
		t.Fatalf("score pending: %v", err)
	}

	// The scored row records the posture that governed it, not the intake default.
	var scored model.TrustScanResult
	if err := testDB.Take(&scored, r.ID).Error; err != nil {
		t.Fatalf("reload scan: %v", err)
	}
	if scored.Mode != model.ScanModeLive {
		t.Fatalf("scored row mode = %d, want live(%d)", scored.Mode, model.ScanModeLive)
	}

	var item model.TrustReviewItem
	if err := testDB.Where("subject_id = ?", "live-1").Take(&item).Error; err != nil {
		t.Fatalf("expected one review item: %v", err)
	}
	if item.Source != model.ReviewSourceAIText {
		t.Fatalf("item source = %d, want ai_text(%d)", item.Source, model.ReviewSourceAIText)
	}
	if item.Status != model.ReviewStatusPending {
		t.Fatalf("item status = %d, want pending", item.Status)
	}
	if item.ClassifierScore == nil || *item.ClassifierScore != 0.87 {
		t.Fatalf("classifier_score = %v, want 0.87", item.ClassifierScore)
	}
	// The note must carry enough for a human to judge without opening the product.
	if item.ContextNote == nil {
		t.Fatal("context_note is nil; the reviewer has no evidence")
	}
	for _, want := range []string{"[ai]", "harassment", "4242", "convicts"} {
		if !strings.Contains(*item.ContextNote, want) {
			t.Fatalf("context_note %q missing %q", *item.ContextNote, want)
		}
	}

	var disp model.TrustDisposition
	if err := testDB.Where("review_item_id = ?", item.ID).Take(&disp).Error; err != nil {
		t.Fatalf("expected one disposition: %v", err)
	}
	if disp.Action != model.ActionHide {
		t.Fatalf("action = %d, want hide(%d)", disp.Action, model.ActionHide)
	}
	if disp.ReasonCode != scanReasonCode {
		t.Fatalf("reason_code = %q, want %q", disp.ReasonCode, scanReasonCode)
	}
	// The kind has a callback_url → the delivery worker must pick this up.
	if disp.CallbackStatus == nil || *disp.CallbackStatus != model.CallbackStatusPending {
		t.Fatalf("callback_status = %v, want pending", disp.CallbackStatus)
	}
	if disp.NextAttemptAt == nil {
		t.Fatal("next_attempt_at is nil; the callback worker would never claim it")
	}
}

// TestScanLiveModeCleanVerdictDoesNothing: live mode is a posture for
// convictions only — an acquittal must stay as silent as it is in shadow.
func TestScanLiveModeCleanVerdictDoesNothing(t *testing.T) {
	cleanTables(t)
	registerKind(t, tSite, tKind, strptr("http://product.invalid/cb"), strptr("s3cr3t"))
	seedPending(t, "clean-1", "a perfectly ordinary galgame comment")

	w := NewScanWorker(testDB, &fakeGateway{
		configured: true,
		verdict:    GatewayVerdict{Flagged: false, Score: f32(0.02), Channel: "test-channel"},
	}, stubTier0{}, WithScanMode("live"))
	if _, err := w.ScorePending(context.Background()); err != nil {
		t.Fatalf("score pending: %v", err)
	}

	if n := countRows(t, &model.TrustReviewItem{}); n != 0 {
		t.Fatalf("clean verdict opened %d review items, want 0", n)
	}
	if n := countRows(t, &model.TrustDisposition{}); n != 0 {
		t.Fatalf("clean verdict queued %d dispositions, want 0", n)
	}
}

// TestScanLiveModeIsIdempotentPerSubject: every edit produces a FRESH scan row
// for the same subject, so re-conviction is the normal case, not an edge one. It
// must raise the recorded score on the one open item and must NOT queue a second
// hide — otherwise an edit war would fan out dispositions.
func TestScanLiveModeIsIdempotentPerSubject(t *testing.T) {
	cleanTables(t)
	registerKind(t, tSite, tKind, strptr("http://product.invalid/cb"), strptr("s3cr3t"))

	seedPending(t, "dup-1", "first version")
	if _, err := liveWorker(0.55).ScorePending(context.Background()); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	seedPending(t, "dup-1", "edited version")
	if _, err := liveWorker(0.91).ScorePending(context.Background()); err != nil {
		t.Fatalf("second pass: %v", err)
	}

	if n := countRows(t, &model.TrustReviewItem{}, "subject_id = ?", "dup-1"); n != 1 {
		t.Fatalf("review items for subject = %d, want 1 (invariant 4)", n)
	}
	if n := countRows(t, &model.TrustDisposition{}); n != 1 {
		t.Fatalf("dispositions = %d, want 1 (no second hide on re-conviction)", n)
	}
	var item model.TrustReviewItem
	if err := testDB.Where("subject_id = ?", "dup-1").Take(&item).Error; err != nil {
		t.Fatalf("reload item: %v", err)
	}
	if item.ClassifierScore == nil || *item.ClassifierScore != 0.91 {
		t.Fatalf("classifier_score = %v, want the raised 0.91", item.ClassifierScore)
	}
	// The first excerpt is the evidence of record — a later scan must not clobber it.
	if item.ContextNote == nil || !strings.Contains(*item.ContextNote, "first version") {
		t.Fatalf("context_note = %v, want the FIRST excerpt preserved", item.ContextNote)
	}
}

// TestScanLiveModeAdoptsExistingOpenItem: when a human report already opened an
// item for the subject, the AI signal merges into it and does NOT auto-enforce.
// A reporter's item is being worked by a person; auto-hiding behind them would
// take the decision away mid-review.
func TestScanLiveModeAdoptsExistingOpenItem(t *testing.T) {
	cleanTables(t)
	registerKind(t, tSite, tKind, strptr("http://product.invalid/cb"), strptr("s3cr3t"))

	existing := model.TrustReviewItem{
		Site: tSite, SubjectKind: tKind, SubjectID: "adopt-1",
		Source: model.ReviewSourceReports, Priority: 1, Status: model.ReviewStatusPending,
	}
	if err := testDB.Create(&existing).Error; err != nil {
		t.Fatalf("seed existing item: %v", err)
	}

	seedPending(t, "adopt-1", "also convicted by the classifier")
	if _, err := liveWorker(0.77).ScorePending(context.Background()); err != nil {
		t.Fatalf("score pending: %v", err)
	}

	if n := countRows(t, &model.TrustReviewItem{}, "subject_id = ?", "adopt-1"); n != 1 {
		t.Fatalf("review items = %d, want 1 (adopted, not forked)", n)
	}
	if n := countRows(t, &model.TrustDisposition{}); n != 0 {
		t.Fatalf("dispositions = %d, want 0 (a human owns this item)", n)
	}
	var reloaded model.TrustReviewItem
	if err := testDB.Take(&reloaded, existing.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Source != model.ReviewSourceReports {
		t.Fatalf("source = %d, want the original reports source preserved", reloaded.Source)
	}
	if reloaded.ClassifierScore == nil || *reloaded.ClassifierScore != 0.77 {
		t.Fatalf("classifier_score = %v, want the AI signal merged in", reloaded.ClassifierScore)
	}
}

// TestScanLiveModeWithoutCallbackURLStaysHumanOnly: a kind with no callback
// endpoint cannot be enforced remotely, so the disposition is recorded with a
// NULL callback_status and simply waits in the inbox — the same rule Decide
// follows. A pending status here would make the delivery worker spin on a
// disposition it can never deliver.
func TestScanLiveModeWithoutCallbackURLStaysHumanOnly(t *testing.T) {
	cleanTables(t)
	registerKind(t, tSite, tKind, nil, nil)
	seedPending(t, "nocb-1", "convict me")

	if _, err := liveWorker(0.8).ScorePending(context.Background()); err != nil {
		t.Fatalf("score pending: %v", err)
	}

	var disp model.TrustDisposition
	if err := testDB.Take(&disp).Error; err != nil {
		t.Fatalf("expected one disposition: %v", err)
	}
	if disp.CallbackStatus != nil {
		t.Fatalf("callback_status = %v, want NULL (no endpoint to call)", disp.CallbackStatus)
	}
	if disp.NextAttemptAt != nil {
		t.Fatalf("next_attempt_at = %v, want NULL", disp.NextAttemptAt)
	}
}

// TestScanModeParsing: an unrecognised KUN_TRUST_SCAN_MODE must degrade to
// shadow. Enforcement is opt-in, so a typo has to fail toward silence.
func TestScanModeParsing(t *testing.T) {
	for _, tc := range []struct {
		name string
		want int16
	}{
		{"live", model.ScanModeLive},
		{"shadow", model.ScanModeShadow},
		{"", model.ScanModeShadow},
		{"LIVE", model.ScanModeShadow},
		{"liv", model.ScanModeShadow},
		{"enforce", model.ScanModeShadow},
	} {
		w := NewScanWorker(testDB, &fakeGateway{}, stubTier0{}, WithScanMode(tc.name))
		if w.mode != tc.want {
			t.Fatalf("WithScanMode(%q) → mode %d, want %d", tc.name, w.mode, tc.want)
		}
	}
}
