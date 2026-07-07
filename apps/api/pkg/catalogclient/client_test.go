package catalogclient

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewUnconfigured(t *testing.T) {
	if New(Config{}) != nil {
		t.Fatal("empty config must yield a nil client (soft-503 signal)")
	}
	if New(Config{BaseURL: "http://x", ClientID: "id"}) != nil {
		t.Fatal("missing secret must yield nil")
	}
}

func TestGetJSONForwardsBasicAuthAndPath(t *testing.T) {
	var gotAuth, gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, ClientID: "cid", ClientSecret: "sec"})
	if c == nil {
		t.Fatal("configured client must be non-nil")
	}
	status, body, err := c.GetJSON(context.Background(), "stats", "q=x&type=names")
	if err != nil {
		t.Fatalf("GetJSON: %v", err)
	}
	if status != 200 || string(body) != `{"ok":true}` {
		t.Fatalf("status=%d body=%s", status, body)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("cid:sec"))
	if gotAuth != want {
		t.Errorf("auth header = %q, want %q", gotAuth, want)
	}
	if gotPath != "/api/v1/catalog/stats" {
		t.Errorf("path = %q", gotPath)
	}
	if gotQuery != "q=x&type=names" {
		t.Errorf("query = %q", gotQuery)
	}
}
