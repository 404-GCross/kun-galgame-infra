package upstream

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestConfigured — the decoupling gate: a client is configured only when BOTH
// base URL and token are set. Empty either → degraded (callers must not dial).
func TestConfigured(t *testing.T) {
	cases := []struct {
		base, token string
		want        bool
	}{
		{"", "", false},
		{"http://x/v1", "", false},
		{"", "tok", false},
		{"http://x/v1", "tok", true},
	}
	for _, c := range cases {
		if got := NewClient(c.base, c.token, "m").Configured(); got != c.want {
			t.Errorf("Configured(base=%q token=%q) = %v, want %v", c.base, c.token, got, c.want)
		}
	}
}

// TestChatJSONNormal — a stubbed OpenAI-compatible server: the client posts to
// {base}/chat/completions with a bearer token and parses content + channel +
// usage from the response.
func TestChatJSONNormal(t *testing.T) {
	var gotAuth, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		_, _ = io.WriteString(w, `{
			"model": "deepseek-chat",
			"choices": [{"message": {"role": "assistant", "content": "{\"flagged\": true, \"score\": 0.8}"}}],
			"usage": {"prompt_tokens": 11, "completion_tokens": 5}
		}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "sekret", "deepseek-chat")
	res, err := c.ChatJSON(context.Background(), "system", "user text", 128)
	if err != nil {
		t.Fatalf("ChatJSON: %v", err)
	}
	if gotAuth != "Bearer sekret" {
		t.Errorf("Authorization = %q, want Bearer sekret", gotAuth)
	}
	if gotPath != "/chat/completions" {
		t.Errorf("path = %q, want /chat/completions", gotPath)
	}
	if rf, _ := gotBody["response_format"].(map[string]any); rf["type"] != "json_object" {
		t.Errorf("response_format not json_object: %v", gotBody["response_format"])
	}
	if !strings.Contains(res.Content, `"flagged": true`) {
		t.Errorf("content not passed through: %q", res.Content)
	}
	if res.Channel != "deepseek-chat" || res.PromptTokens != 11 || res.CompletionTokens != 5 {
		t.Errorf("parsed fields wrong: channel=%q prompt=%d completion=%d", res.Channel, res.PromptTokens, res.CompletionTokens)
	}
}

// TestChatJSON5xx — an upstream 5xx surfaces as an error, which the moderation
// service turns into a fail-open degraded allow.
func TestChatJSON5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, `upstream exploded`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "sekret", "deepseek-chat")
	if _, err := c.ChatJSON(context.Background(), "s", "u", 64); err == nil {
		t.Fatal("want error on 5xx, got nil")
	}
}

// TestChatJSONChannelFallback — when the response omits `model`, the requested
// model id is recorded as the channel.
func TestChatJSONChannelFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"{}"}}],"usage":{}}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "sekret", "mimo-7b")
	res, err := c.ChatJSON(context.Background(), "s", "u", 64)
	if err != nil {
		t.Fatalf("ChatJSON: %v", err)
	}
	if res.Channel != "mimo-7b" {
		t.Errorf("channel fallback = %q, want mimo-7b", res.Channel)
	}
}
