package handler

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"api/internal/platform/community/service"

	"github.com/gofiber/fiber/v3"
)

func hmacHex(secret, ts string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestTrustCallbackHTTP(t *testing.T) {
	const secret = "wire-secret"
	svc := service.NewCallbackService(testDB)
	app := fiber.New()
	app.Post("/trust/callback", TrustCallback(secret, svc))

	body := []byte(`{"disposition_id":1,"subject_kind":"community_post","subject_id":"999999","action":1,"reason_code":"x"}`)
	ts := strconv.FormatInt(time.Now().Unix(), 10)

	bad := httptest.NewRequest("POST", "/trust/callback", bytes.NewReader(body))
	bad.Header.Set("X-Trust-Timestamp", ts)
	bad.Header.Set("X-Trust-Signature", "deadbeef")
	resp, err := app.Test(bad)
	if err != nil {
		t.Fatalf("bad-sig request: %v", err)
	}
	if resp.StatusCode != 401 {
		t.Fatalf("bad signature → status %d, want 401", resp.StatusCode)
	}

	ok := httptest.NewRequest("POST", "/trust/callback", bytes.NewReader(body))
	ok.Header.Set("X-Trust-Timestamp", ts)
	ok.Header.Set("X-Trust-Signature", hmacHex(secret, ts, body))
	resp2, err := app.Test(ok)
	if err != nil {
		t.Fatalf("valid request: %v", err)
	}
	if resp2.StatusCode != 200 {
		t.Fatalf("valid signature → status %d, want 200", resp2.StatusCode)
	}
}
