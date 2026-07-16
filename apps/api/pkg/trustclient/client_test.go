package trustclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestScanWire verifies the Scan client posts to /api/v1/trust/scan with the
// site + author on the wire and decodes the {code,message,data} envelope.
func TestScanWire(t *testing.T) {
	var gotPath string
	var gotBody ScanRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":0,"message":"ok","data":{"scan_id":77,"truncated":true}}`)
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, ClientID: "cid", ClientSecret: "secret"})
	if c == nil {
		t.Fatal("client must be configured")
	}
	author := int64(42)
	scanID, truncated, err := c.Scan(context.Background(), ScanRequest{
		Site: "letmoe", SubjectKind: "community_post", SubjectID: "9", Text: "hi", AuthorID: &author,
	})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if gotPath != "/api/v1/trust/scan" {
		t.Fatalf("path = %q, want /api/v1/trust/scan", gotPath)
	}
	if gotBody.Site != "letmoe" || gotBody.SubjectID != "9" || gotBody.AuthorID == nil || *gotBody.AuthorID != 42 {
		t.Fatalf("wire body = %+v", gotBody)
	}
	if scanID != 77 || !truncated {
		t.Fatalf("decoded scan_id=%d truncated=%v, want 77/true", scanID, truncated)
	}
}

// TestCheckWire verifies the Check client posts to /api/v1/trust/check with the
// site + author on the wire and decodes {decision, matched} from the envelope.
func TestCheckWire(t *testing.T) {
	var gotPath string
	var gotBody CheckRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":0,"message":"ok","data":{"decision":"deny","matched":["badword"]}}`)
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, ClientID: "cid", ClientSecret: "secret"})
	if c == nil {
		t.Fatal("client must be configured")
	}
	author := int64(42)
	decision, matched, err := c.Check(context.Background(), CheckRequest{
		Site: "letmoe", Text: "some text", AuthorID: &author,
	})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if gotPath != "/api/v1/trust/check" {
		t.Fatalf("path = %q, want /api/v1/trust/check", gotPath)
	}
	if gotBody.Site != "letmoe" || gotBody.Text != "some text" || gotBody.AuthorID == nil || *gotBody.AuthorID != 42 {
		t.Fatalf("wire body = %+v", gotBody)
	}
	if decision != "deny" || len(matched) != 1 || matched[0] != "badword" {
		t.Fatalf("decoded decision=%q matched=%v, want deny/[badword]", decision, matched)
	}
}
