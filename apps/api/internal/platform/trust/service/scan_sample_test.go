package service

import (
	"context"
	"strings"
	"testing"

	"api/internal/platform/trust/model"
)

func cleanWorker(sampleRate float64, draw float64, opts ...ScanWorkerOption) *ScanWorker {
	all := append([]ScanWorkerOption{
		WithSampleRate(sampleRate),
		func(w *ScanWorker) { w.rand = func() float64 { return draw } },
	}, opts...)
	return NewScanWorker(testDB, &fakeGateway{
		configured: true,
		verdict:    GatewayVerdict{Flagged: false, Score: f32(0.03), Channel: "test-channel"},
	}, stubTier0{}, all...)
}

func TestSampleRateParsing(t *testing.T) {
	for _, tc := range []struct {
		in   float64
		want float64
	}{
		{0.005, 0.005}, {maxScanSampleRate, maxScanSampleRate},
		{0, 0}, {-1, 0}, {0.5, 0}, {1, 0}, {100, 0},
	} {
		w := NewScanWorker(testDB, &fakeGateway{}, stubTier0{}, WithSampleRate(tc.in))
		if w.sampleRate != tc.want {
			t.Errorf("WithSampleRate(%v) → %v, want %v", tc.in, w.sampleRate, tc.want)
		}
	}
}

func TestSamplingOffByDefault(t *testing.T) {
	cleanTables(t)
	seedPending(t, "nosample-1", "a perfectly ordinary galgame comment")

	w := NewScanWorker(testDB, &fakeGateway{
		configured: true,
		verdict:    GatewayVerdict{Flagged: false, Score: f32(0.01), Channel: "test-channel"},
	}, stubTier0{})
	if _, err := w.ScorePending(context.Background()); err != nil {
		t.Fatalf("score pending: %v", err)
	}
	if n := countRows(t, &model.TrustReviewItem{}); n != 0 {
		t.Fatalf("default worker opened %d review items, want 0", n)
	}
}

func TestSampleOpensCalibrationItemWithoutEnforcing(t *testing.T) {
	cleanTables(t)
	registerKind(t, tSite, tKind, strptr("http://product.invalid/cb"), strptr("s3cr3t"))
	seedPending(t, "sample-1", "an ordinary comment about a visual novel")

	if _, err := cleanWorker(0.01, 0).ScorePending(context.Background()); err != nil {
		t.Fatalf("score pending: %v", err)
	}

	var item model.TrustReviewItem
	if err := testDB.Where("subject_id = ?", "sample-1").Take(&item).Error; err != nil {
		t.Fatalf("expected one calibration item: %v", err)
	}
	if item.Source != model.ReviewSourceAISample {
		t.Fatalf("source = %d, want ai_sample(%d)", item.Source, model.ReviewSourceAISample)
	}
	if n := countRows(t, &model.TrustDisposition{}); n != 0 {
		t.Fatalf("a calibration sample queued %d dispositions, want 0", n)
	}
	if item.Priority != scanSamplePriority {
		t.Fatalf("priority = %v, want the parked %v", item.Priority, scanSamplePriority)
	}
	if item.Priority >= rankPriority(1, nil) {
		t.Fatalf("sample priority %v is not below the lowest real signal", item.Priority)
	}
	if item.ContextNote == nil {
		t.Fatal("context_note is nil; the reviewer cannot tell this is a calibration draw")
	}
	for _, want := range []string{"[calibration]", "cleared", "at random", "visual novel"} {
		if !strings.Contains(*item.ContextNote, want) {
			t.Fatalf("context_note %q missing %q", *item.ContextNote, want)
		}
	}
}

func TestSampleNotDrawnWhenRollMisses(t *testing.T) {
	cleanTables(t)
	seedPending(t, "miss-1", "ordinary text")

	if _, err := cleanWorker(0.01, 0.99).ScorePending(context.Background()); err != nil {
		t.Fatalf("score pending: %v", err)
	}
	if n := countRows(t, &model.TrustReviewItem{}); n != 0 {
		t.Fatalf("a missed roll opened %d items, want 0", n)
	}
}

func TestSampleSkipsSubjectAlreadyUnderReview(t *testing.T) {
	cleanTables(t)
	existing := model.TrustReviewItem{
		Site: tSite, SubjectKind: tKind, SubjectID: "busy-1",
		Source: model.ReviewSourceReports, Priority: 3, Status: model.ReviewStatusPending,
	}
	if err := testDB.Create(&existing).Error; err != nil {
		t.Fatalf("seed existing item: %v", err)
	}
	seedPending(t, "busy-1", "the same subject, re-scanned clean")

	if _, err := cleanWorker(maxScanSampleRate, 0).ScorePending(context.Background()); err != nil {
		t.Fatalf("score pending: %v", err)
	}

	if n := countRows(t, &model.TrustReviewItem{}, "subject_id = ?", "busy-1"); n != 1 {
		t.Fatalf("items for subject = %d, want 1 (the sample must not fork one)", n)
	}
	var reloaded model.TrustReviewItem
	if err := testDB.Take(&reloaded, existing.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Source != model.ReviewSourceReports || reloaded.Priority != 3 {
		t.Fatalf("the sample mutated a live case: source=%d priority=%v",
			reloaded.Source, reloaded.Priority)
	}
}

func TestSamplingIsIndependentOfScanMode(t *testing.T) {
	for _, mode := range []string{"shadow", "live"} {
		cleanTables(t)
		registerKind(t, tSite, tKind, strptr("http://product.invalid/cb"), strptr("s3cr3t"))
		seedPending(t, "mode-1", "ordinary text")

		w := cleanWorker(maxScanSampleRate, 0, WithScanMode(mode))
		if _, err := w.ScorePending(context.Background()); err != nil {
			t.Fatalf("%s: score pending: %v", mode, err)
		}
		if n := countRows(t, &model.TrustReviewItem{}, "source = ?", model.ReviewSourceAISample); n != 1 {
			t.Fatalf("%s: calibration items = %d, want 1", mode, n)
		}
		if n := countRows(t, &model.TrustDisposition{}); n != 0 {
			t.Fatalf("%s: sampling queued %d dispositions, want 0", mode, n)
		}
	}
}

func TestFlaggedVerdictIsNeverSampled(t *testing.T) {
	cleanTables(t)
	registerKind(t, tSite, tKind, strptr("http://product.invalid/cb"), strptr("s3cr3t"))
	seedPending(t, "flag-1", "convict me")

	w := NewScanWorker(testDB, &fakeGateway{
		configured: true,
		verdict:    GatewayVerdict{Flagged: true, Score: f32(0.9), Channel: "test-channel"},
	}, stubTier0{}, WithScanMode("live"), WithSampleRate(maxScanSampleRate),
		func(w *ScanWorker) { w.rand = func() float64 { return 0 } })
	if _, err := w.ScorePending(context.Background()); err != nil {
		t.Fatalf("score pending: %v", err)
	}

	if n := countRows(t, &model.TrustReviewItem{}, "source = ?", model.ReviewSourceAISample); n != 0 {
		t.Fatalf("a conviction produced %d calibration items, want 0", n)
	}
	if n := countRows(t, &model.TrustReviewItem{}, "source = ?", model.ReviewSourceAIText); n != 1 {
		t.Fatalf("ai_text items = %d, want 1", n)
	}
}
