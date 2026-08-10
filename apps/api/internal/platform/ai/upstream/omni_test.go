package upstream

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOmniConfigured(t *testing.T) {
	cases := []struct {
		base, token string
		want        bool
	}{
		{"", "", false},
		{"https://api.openai.com", "", false},
		{"", "tok", false},
		{"https://api.openai.com", "tok", true},
	}
	for _, c := range cases {
		if got := NewOmniClient(c.base, c.token, "m").Configured(); got != c.want {
			t.Errorf("Configured(base=%q token=%q) = %v, want %v", c.base, c.token, got, c.want)
		}
	}
}

func TestOmniModerateNormal(t *testing.T) {
	var gotAuth, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		_, _ = io.WriteString(w, `{
			"id": "modr-1",
			"model": "omni-moderation-latest",
			"results": [{
				"flagged": true,
				"categories": {"violence": true, "sexual": false, "sexual/minors": true},
				"category_scores": {"violence": 0.71, "sexual": 0.99, "sexual/minors": 0.83}
			}]
		}`)
	}))
	defer srv.Close()

	c := NewOmniClient(srv.URL, "sekret", "omni-moderation-latest")
	res, err := c.Moderate(context.Background(), "some text")
	if err != nil {
		t.Fatalf("Moderate: %v", err)
	}
	if gotAuth != "Bearer sekret" {
		t.Errorf("Authorization = %q, want Bearer sekret", gotAuth)
	}
	if gotPath != "/v1/moderations" {
		t.Errorf("path = %q, want /v1/moderations", gotPath)
	}
	if gotBody["model"] != "omni-moderation-latest" || gotBody["input"] != "some text" {
		t.Errorf("request body = %v, want model+input", gotBody)
	}
	if !res.Flagged {
		t.Errorf("flagged = %v, want true", res.Flagged)
	}
	if !res.Categories["violence"] || res.Categories["sexual"] || !res.Categories["sexual/minors"] {
		t.Errorf("categories not parsed: %v", res.Categories)
	}
	if res.CategoryScores["violence"] != 0.71 || res.CategoryScores["sexual/minors"] != 0.83 {
		t.Errorf("category_scores not parsed: %v", res.CategoryScores)
	}
	if res.Channel != "omni-moderation-latest" {
		t.Errorf("channel = %q, want omni-moderation-latest", res.Channel)
	}
}

func TestOmniModerate5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, `moderations exploded`)
	}))
	defer srv.Close()

	c := NewOmniClient(srv.URL, "sekret", "omni-moderation-latest")
	if _, err := c.Moderate(context.Background(), "x"); err == nil {
		t.Fatal("want error on 5xx, got nil")
	}
}

func TestOmniModerateChannelFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"results":[{"flagged":false,"categories":{},"category_scores":{}}]}`)
	}))
	defer srv.Close()

	c := NewOmniClient(srv.URL, "sekret", "omni-moderation-2024")
	res, err := c.Moderate(context.Background(), "x")
	if err != nil {
		t.Fatalf("Moderate: %v", err)
	}
	if res.Channel != "omni-moderation-2024" {
		t.Errorf("channel fallback = %q, want omni-moderation-2024", res.Channel)
	}
}

func TestOmniModerateNoResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"model":"omni-moderation-latest","results":[]}`)
	}))
	defer srv.Close()

	c := NewOmniClient(srv.URL, "sekret", "omni-moderation-latest")
	if _, err := c.Moderate(context.Background(), "x"); err == nil {
		t.Fatal("want error on empty results, got nil")
	}
}
