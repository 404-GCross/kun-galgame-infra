package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHTTPTranslatorWire pins what actually goes on the wire: temperature 0,
// the pinned system prompt, and a user message carrying the three
// disambiguation inputs (name, group, plain-text description).
func TestHTTPTranslatorWire(t *testing.T) {
	var got struct {
		Model       string  `json:"model"`
		Temperature float64 `json:"temperature"`
		Messages    []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/chat/completions", r.URL.Path)
		assert.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
		body, _ := io.ReadAll(r.Body)
		require.NoError(t, json.Unmarshal(body, &got))
		_, _ = w.Write([]byte(`{"model":"glm-5.2","choices":[{"message":{"content":"“呆毛”\n(an explanation the prompt asked for but did not get)"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	tr := newHTTPTranslator(srv.URL, "tok", "glm-5.2", 256)
	require.True(t, tr.Configured())

	zh, model, err := tr.Translate(context.Background(), mtCandidate{
		Name: "Ahoge", GroupName: "Hair",
		Description: "A single strand of [url=/i1]hair[/url] that sticks up.",
	})
	require.NoError(t, err)
	assert.Equal(t, "呆毛", zh, "quotes and the trailing explanation are trimmed off")
	assert.Equal(t, "glm-5.2", model)

	assert.Equal(t, float64(0), got.Temperature)
	require.Len(t, got.Messages, 2)
	assert.Equal(t, TranslateSystemPrompt, got.Messages[0].Content)
	assert.Contains(t, got.Messages[1].Content, "标签名(英文): Ahoge")
	assert.Contains(t, got.Messages[1].Content, "所属分类: Hair")
	assert.Contains(t, got.Messages[1].Content, "英文释义: A single strand of hair that sticks up.")
}

// TestHTTPTranslatorRefusesPartialOutput: a truncated generation is an error,
// never a half-name silently written into the review sheet.
func TestHTTPTranslatorRefusesPartialOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"呆"},"finish_reason":"length"}]}`))
	}))
	defer srv.Close()

	_, _, err := newHTTPTranslator(srv.URL, "tok", "m", 8).Translate(context.Background(), mtCandidate{Name: "Ahoge"})
	require.ErrorContains(t, err, "finish_reason")
}

func TestCleanProposal(t *testing.T) {
	for in, want := range map[string]string{
		"呆毛":        "呆毛",
		" \"呆毛\" ":  "呆毛",
		"译名: 呆毛":    "呆毛",
		"呆毛。":       "呆毛",
		"呆毛\n解释一大段": "呆毛",
		"「呆毛」":      "呆毛",
	} {
		assert.Equal(t, want, cleanProposal(in), "input %q", in)
	}
}
