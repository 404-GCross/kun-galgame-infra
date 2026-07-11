package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"api/internal/platform/trust/model"
)

// actionCallbackItem opens an item on a callback-configured kind and actions it,
// returning the pending disposition id.
func actionCallbackItem(t *testing.T, subject, callbackURL, secret string) int64 {
	t.Helper()
	registerKind(t, tSite, tKind, &callbackURL, &secret)
	id := mkOpenItem(t, subject)
	action := model.ActionHide
	dispID, err := NewReviewService(testDB).Decide(context.Background(), DecideParams{
		ID: id, DecidedBy: 5, Decision: "actioned", Action: &action, ReasonCode: "abuse.hide",
	})
	if err != nil {
		t.Fatalf("action decide: %v", err)
	}
	if dispID == nil {
		t.Fatal("expected a disposition id")
	}
	// A callback kind must queue the disposition pending.
	var disp model.TrustDisposition
	testDB.Take(&disp, *dispID)
	if disp.CallbackStatus == nil || *disp.CallbackStatus != model.CallbackStatusPending {
		t.Fatalf("callback kind → disposition must be pending, got %v", disp.CallbackStatus)
	}
	return *dispID
}

func dispStatus(t *testing.T, id int64) (status *int16, attempts int32) {
	t.Helper()
	var d model.TrustDisposition
	if err := testDB.Take(&d, id).Error; err != nil {
		t.Fatalf("load disposition %d: %v", id, err)
	}
	return d.CallbackStatus, d.CallbackAttempts
}

// E8a: a 2xx endpoint → delivered, and the signature verifies with the secret.
func TestCallbackDelivered(t *testing.T) {
	cleanTables(t)
	const secret = "s3cr3t-hmac"
	var sigOK atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ts := r.Header.Get("X-Trust-Timestamp")
		want := SignPayload(secret, ts, body)
		if r.Header.Get("X-Trust-Signature") == want && ts != "" {
			sigOK.Store(true)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	dispID := actionCallbackItem(t, "cbok", srv.URL, secret)

	w := NewCallbackWorker(testDB)
	n, err := w.DeliverDue(context.Background())
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 processed, got %d", n)
	}
	if !sigOK.Load() {
		t.Fatal("callback signature did not verify")
	}
	status, _ := dispStatus(t, dispID)
	if status == nil || *status != model.CallbackStatusDelivered {
		t.Fatalf("disposition must be delivered, got %v", status)
	}
	assertAudit(t, "callback_delivered")
}

// E8b: a persistently-failing endpoint → dead_letter after 5 attempts; then
// redeliver revives it and a now-healthy endpoint delivers.
func TestCallbackDeadLetterAndRedeliver(t *testing.T) {
	cleanTables(t)
	const secret = "s3cr3t-hmac"
	var healthy atomic.Bool // starts false = 500
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if healthy.Load() {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	dispID := actionCallbackItem(t, "cbfail", srv.URL, secret)

	// Zero backoff so next_attempt_at is immediately due between passes.
	w := NewCallbackWorker(testDB)
	w.backoff = []time.Duration{0, 0, 0, 0, 0}

	for i := 0; i < callbackMaxAttempts; i++ {
		if _, err := w.DeliverDue(context.Background()); err != nil {
			t.Fatalf("deliver pass %d: %v", i, err)
		}
	}
	status, attempts := dispStatus(t, dispID)
	if status == nil || *status != model.CallbackStatusDeadLetter {
		t.Fatalf("after %d failures must be dead_letter, got %v", callbackMaxAttempts, status)
	}
	if attempts != int32(callbackMaxAttempts) {
		t.Fatalf("attempts = %d, want %d", attempts, callbackMaxAttempts)
	}
	assertAudit(t, "callback_dead_letter")

	// Redeliver → pending, attempts reset; a healthy endpoint then delivers.
	if err := NewDispositionService(testDB).Redeliver(context.Background(), 9, dispID); err != nil {
		t.Fatalf("redeliver: %v", err)
	}
	status, attempts = dispStatus(t, dispID)
	if status == nil || *status != model.CallbackStatusPending || attempts != 0 {
		t.Fatalf("redeliver must reset to pending/0, got status=%v attempts=%d", status, attempts)
	}
	healthy.Store(true)
	if _, err := w.DeliverDue(context.Background()); err != nil {
		t.Fatalf("deliver after redeliver: %v", err)
	}
	status, _ = dispStatus(t, dispID)
	if status == nil || *status != model.CallbackStatusDelivered {
		t.Fatalf("after redeliver+healthy must be delivered, got %v", status)
	}
}

// TestRedeliverGuards pins the not-found / not-dead-letter guards.
func TestRedeliverGuards(t *testing.T) {
	cleanTables(t)
	svc := NewDispositionService(testDB)
	if err := svc.Redeliver(context.Background(), 1, 999999); err == nil {
		t.Fatal("redeliver absent disposition must error")
	}
}

func assertAudit(t *testing.T, action string) {
	t.Helper()
	var n int64
	if err := testDB.Model(&model.TrustAuditLog{}).Where("action = ?", action).Count(&n).Error; err != nil {
		t.Fatalf("count audit %s: %v", action, err)
	}
	if n == 0 {
		t.Fatalf("expected an audit row with action %q", action)
	}
}
