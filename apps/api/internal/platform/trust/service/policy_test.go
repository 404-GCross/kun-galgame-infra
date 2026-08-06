package service

import (
	"context"
	"testing"

	"api/internal/platform/trust/model"
)

// testDefaults is a platform baseline deliberately UNLIKE the zero value on
// every field, so a test that accidentally reads a zero cannot pass by looking
// like the default.
func testDefaults() PlatformDefaults {
	return PlatformDefaults{
		ScanMode:           model.ScanModeLive,
		SampleRate:         0.01,
		AggregateThreshold: 3.0,
		AutoHideEnabled:    true,
	}
}

func i16(v int16) *int16     { return &v }
func f64(v float64) *float64 { return &v }
func boolp(v bool) *bool     { return &v }

// TestPolicyAbsentRowIsPlatformDefault is the claim that makes this table safe
// to ship: adding it changes nothing. Every site that has not spoken resolves to
// exactly the posture it had before the table existed.
func TestPolicyAbsentRowIsPlatformDefault(t *testing.T) {
	cleanTables(t)
	svc := NewPolicyService(testDB, testDefaults())

	got := svc.Resolve("a-site-that-has-no-row")
	want := testDefaults()
	if got.ScanMode != want.ScanMode || got.SampleRate != want.SampleRate ||
		got.AggregateThreshold != want.AggregateThreshold || got.AutoHideEnabled != want.AutoHideEnabled {
		t.Fatalf("absent row resolved to %+v, want the platform defaults %+v", got, want)
	}
	if got.FlagThreshold != nil {
		t.Errorf("absent row set a flag threshold (%v); it must defer to the gateway", *got.FlagThreshold)
	}
}

// TestPolicyNullColumnIsNoOpinion covers the case an absent row does not: a site
// that HAS a row but has expressed an opinion on only one knob. Every other
// column must keep following the platform default, which is what lets a default
// be changed later without silently dragging along sites that never chose.
func TestPolicyNullColumnIsNoOpinion(t *testing.T) {
	cleanTables(t)
	svc := NewPolicyService(testDB, testDefaults())

	if err := svc.Upsert(&model.TrustSitePolicy{
		Site: "letmoe", ScanMode: i16(model.ScanModeShadow),
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got := svc.Resolve("letmoe")
	if got.ScanMode != model.ScanModeShadow {
		t.Errorf("scan_mode = %d, want shadow (the one override set)", got.ScanMode)
	}
	if got.SampleRate != testDefaults().SampleRate {
		t.Errorf("sample_rate = %v, want the platform default %v — a NULL is no opinion, not zero",
			got.SampleRate, testDefaults().SampleRate)
	}
	if got.AggregateThreshold != testDefaults().AggregateThreshold {
		t.Errorf("aggregate_threshold = %v, want the platform default", got.AggregateThreshold)
	}
	if !got.AutoHideEnabled {
		t.Error("auto_hide_enabled fell to false on a NULL; a NULL must inherit the default")
	}
}

// TestPolicyUpsertClearsOverride: backing a site out of a posture is the
// operation an operator needs most when a setting is not working, so "clear it"
// has to be expressible. A partial update would make it impossible.
func TestPolicyUpsertClearsOverride(t *testing.T) {
	cleanTables(t)
	svc := NewPolicyService(testDB, testDefaults())

	if err := svc.Upsert(&model.TrustSitePolicy{
		Site: "moyu", ScanMode: i16(model.ScanModeShadow), SampleRate: f64(0.05),
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if got := svc.Resolve("moyu"); got.SampleRate != 0.05 {
		t.Fatalf("sample_rate = %v, want 0.05 before clearing", got.SampleRate)
	}

	// Same site, sample rate omitted → the override is cleared, not preserved.
	if err := svc.Upsert(&model.TrustSitePolicy{
		Site: "moyu", ScanMode: i16(model.ScanModeShadow),
	}); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	if got := svc.Resolve("moyu"); got.SampleRate != testDefaults().SampleRate {
		t.Errorf("sample_rate = %v after clearing, want the platform default %v",
			got.SampleRate, testDefaults().SampleRate)
	}
	if n := countRows(t, &model.TrustSitePolicy{}); n != 1 {
		t.Errorf("upserting the same site produced %d rows, want 1", n)
	}
}

// TestPolicyWriteInvalidatesCache: a posture is cached for a minute, but an
// operator who has just flipped a site should not have to wonder whether it took
// effect. The write path invalidates in-process.
func TestPolicyWriteInvalidatesCache(t *testing.T) {
	cleanTables(t)
	svc := NewPolicyService(testDB, testDefaults())

	// Prime the cache with the "no row" answer.
	if got := svc.Resolve("kungal"); got.ScanMode != model.ScanModeLive {
		t.Fatalf("primed resolve = %d, want the live default", got.ScanMode)
	}
	if err := svc.Upsert(&model.TrustSitePolicy{Site: "kungal", ScanMode: i16(model.ScanModeShadow)}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if got := svc.Resolve("kungal"); got.ScanMode != model.ScanModeShadow {
		t.Fatalf("resolve after write = %d, want shadow — the cache was not invalidated", got.ScanMode)
	}
}

// TestScanWorkerHonoursSiteShadow is the whole point of M0: the platform runs
// live, and a single site can still sit in shadow. Without this, onboarding a
// second site means imposing kungal's enforcement posture on it from day one.
func TestScanWorkerHonoursSiteShadow(t *testing.T) {
	cleanTables(t)
	registerKind(t, tSite, tKind, strptr("http://product.invalid/cb"), strptr("s3cr3t"))
	seedPending(t, "site-shadow-1", "convict me")

	svc := NewPolicyService(testDB, PlatformDefaults{ScanMode: model.ScanModeLive, AutoHideEnabled: true})
	if err := svc.Upsert(&model.TrustSitePolicy{Site: tSite, ScanMode: i16(model.ScanModeShadow)}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	w := NewScanWorker(testDB, &fakeGateway{
		configured: true,
		verdict:    GatewayVerdict{Flagged: true, Score: f32(0.9), Channel: "test-channel"},
	}, stubTier0{}, WithScanMode("live"), WithPolicy(svc))
	if _, err := w.ScorePending(context.Background()); err != nil {
		t.Fatalf("score pending: %v", err)
	}

	if n := countRows(t, &model.TrustReviewItem{}); n != 0 {
		t.Fatalf("a site in shadow opened %d review items while the platform was live, want 0", n)
	}
	if n := countRows(t, &model.TrustDisposition{}); n != 0 {
		t.Fatalf("a site in shadow queued %d dispositions, want 0", n)
	}
	// The row must also RECORD the posture that actually governed it, or the
	// corpus stops being self-describing the moment two sites differ.
	var mode int16
	if err := testDB.Raw(`SELECT mode FROM trust_scan_result WHERE subject_id = ?`, "site-shadow-1").
		Scan(&mode).Error; err != nil {
		t.Fatalf("read mode: %v", err)
	}
	if mode != model.ScanModeShadow {
		t.Errorf("row stamped mode=%d, want the site's shadow posture", mode)
	}
}

// TestScanWorkerAutoHideDisabled: live for VISIBILITY without granting the
// classifier takedown power. The item opens (a human will see it) and nothing is
// hidden — the posture a site should normally hold when it first leaves shadow.
func TestScanWorkerAutoHideDisabled(t *testing.T) {
	cleanTables(t)
	registerKind(t, tSite, tKind, strptr("http://product.invalid/cb"), strptr("s3cr3t"))
	seedPending(t, "no-autohide-1", "convict me")

	svc := NewPolicyService(testDB, PlatformDefaults{ScanMode: model.ScanModeLive, AutoHideEnabled: true})
	if err := svc.Upsert(&model.TrustSitePolicy{Site: tSite, AutoHideEnabled: boolp(false)}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	w := NewScanWorker(testDB, &fakeGateway{
		configured: true,
		verdict:    GatewayVerdict{Flagged: true, Score: f32(0.9), Channel: "test-channel"},
	}, stubTier0{}, WithScanMode("live"), WithPolicy(svc))
	if _, err := w.ScorePending(context.Background()); err != nil {
		t.Fatalf("score pending: %v", err)
	}

	if n := countRows(t, &model.TrustReviewItem{}); n != 1 {
		t.Fatalf("review items = %d, want 1 — the human must still see it", n)
	}
	if n := countRows(t, &model.TrustDisposition{}); n != 0 {
		t.Fatalf("auto-hide disabled still queued %d dispositions, want 0", n)
	}
}

// TestGatewayFlaggedRecorded pins the column that keeps calibration samples
// comparable across the M0-B threshold change. It is written on the scored path
// today so that rows already exist when flagged stops meaning "what the gateway
// said".
func TestGatewayFlaggedRecorded(t *testing.T) {
	cleanTables(t)
	seedPending(t, "gwflag-1", "convict me")

	w := NewScanWorker(testDB, &fakeGateway{
		configured: true,
		verdict:    GatewayVerdict{Flagged: true, Score: f32(0.9), Channel: "test-channel"},
	}, stubTier0{})
	if _, err := w.ScorePending(context.Background()); err != nil {
		t.Fatalf("score pending: %v", err)
	}

	var gw *bool
	if err := testDB.Raw(`SELECT gateway_flagged FROM trust_scan_result WHERE subject_id = ?`, "gwflag-1").
		Scan(&gw).Error; err != nil {
		t.Fatalf("read gateway_flagged: %v", err)
	}
	if gw == nil || !*gw {
		t.Fatalf("gateway_flagged = %v, want true", gw)
	}
}

// TestReportAggregateThresholdPerSite: how many complaints mean something is a
// property of a community's size, not of the platform. A site that lowers its
// threshold must open an item on fewer reports than the default would.
func TestReportAggregateThresholdPerSite(t *testing.T) {
	cleanTables(t)
	registerKind(t, tSite, tKind, nil, nil)

	pol := NewPolicyService(testDB, PlatformDefaults{AggregateThreshold: 3.0})
	if err := pol.Upsert(&model.TrustSitePolicy{Site: tSite, AggregateThreshold: f32(1.0)}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	w := newWeigher()
	svc := NewReportService(testDB, w, WithReportPolicy(pol))
	res, err := svc.Submit(context.Background(), ReportParams{
		Site: tSite, SubjectKind: tKind, SubjectID: "thresh-1",
		ReasonKey: "abuse", ReporterID: 1001,
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	// One ordinary report weighs 1.0 — below the platform's 3.0, at this site's 1.0.
	if res.ReviewItemID == nil {
		t.Fatal("a single report opened no item; the site's lowered threshold was ignored")
	}
}
