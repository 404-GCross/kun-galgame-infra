package mcpface

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// expectedTools is the frozen tool surface (design §4; the M1 five that survive
// + catalog_name_get + the canonical-W1 trio + the eight A2 read ops + the
// wave-189 trio + the wave-196 pair). If this list changes, the design doc and
// portal docs page must change with it.
//
// galgame_search / galgame_get left the surface at wave 146 (2026-07-30) with
// the /v1/galgame face they proxied; this list doubles as the assertion that
// they are really unregistered, since TestToolRegistry pins it exactly.
var expectedTools = []string{
	"catalog_calendar",
	"catalog_calendar_pending",
	"catalog_calendar_tba",
	"catalog_changes",
	"catalog_character_get",
	"catalog_engine_get",
	"catalog_engines_list",
	"catalog_label_get",
	"catalog_label_relation_graph",
	"catalog_labels_list",
	"catalog_lookup_external",
	"catalog_name_get",
	"catalog_releases",
	"catalog_search",
	"catalog_series_get",
	"catalog_series_list",
	"catalog_stats",
	"catalog_tag_get",
	"catalog_tags_list",
	"catalog_work_get",
	"catalog_works_list",
	"catalog_works_search",
}

// TestToolRegistry drives the built server through an in-memory MCP client and
// asserts the full tool surface is present with a non-empty input schema each.
func TestToolRegistry(t *testing.T) {
	ctx := context.Background()
	server := NewServer(NewUpstream("http://127.0.0.1:0"))

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer ss.Close()
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer cs.Close()

	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}

	got := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		got = append(got, tool.Name)
		if tool.InputSchema == nil {
			t.Errorf("tool %q has a nil input schema", tool.Name)
		}
		if tool.Description == "" {
			t.Errorf("tool %q has an empty description", tool.Name)
		}
	}
	sort.Strings(got)

	if len(got) != len(expectedTools) {
		t.Fatalf("tool count = %d, want %d (%v)", len(got), len(expectedTools), got)
	}
	for i, name := range expectedTools {
		if got[i] != name {
			t.Errorf("tool[%d] = %q, want %q", i, got[i], name)
		}
	}
}

// TestBearerTokenFormCheck covers the three states the design calls out: missing
// key, malformed prefix, and a well-formed key passed through untouched.
func TestBearerTokenFormCheck(t *testing.T) {
	tests := []struct {
		name      string
		header    http.Header
		wantOK    bool
		wantToken string
	}{
		{"missing", http.Header{}, false, ""},
		{"nil header", nil, false, ""},
		{"non-bearer scheme", http.Header{"Authorization": {"Basic nm_live_x"}}, false, ""},
		{"bearer non-nm token (e.g. a JWT)", http.Header{"Authorization": {"Bearer eyJhbGci.foo.bar"}}, false, ""},
		{"bearer live key", http.Header{"Authorization": {"Bearer nm_live_abc123"}}, true, "nm_live_abc123"},
		{"bearer test key with padding", http.Header{"Authorization": {"Bearer   nm_test_xyz  "}}, true, "nm_test_xyz"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			token, ok := bearerToken(tc.header)
			if ok != tc.wantOK || token != tc.wantToken {
				t.Errorf("bearerToken() = (%q, %v), want (%q, %v)", token, ok, tc.wantToken, tc.wantOK)
			}
		})
	}
}

// TestUpstreamGetPassthrough asserts the client forwards the path, query and
// Authorization header verbatim and returns the upstream status + body.
func TestUpstreamGetPassthrough(t *testing.T) {
	var gotPath, gotQuery, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
	}))
	defer srv.Close()

	up := NewUpstream(srv.URL)
	q := newQuery()
	q.Set("q", "スモーク")
	status, body, err := up.Get(context.Background(), "/v1/catalog/search", q, "Bearer nm_live_k")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if status != http.StatusOK {
		t.Errorf("status = %d, want 200", status)
	}
	if string(body) != `{"data":{"ok":true}}` {
		t.Errorf("body = %s", body)
	}
	if gotPath != "/v1/catalog/search" {
		t.Errorf("upstream path = %q", gotPath)
	}
	if gotQuery != "q=%E3%82%B9%E3%83%A2%E3%83%BC%E3%82%AF" {
		t.Errorf("upstream query = %q", gotQuery)
	}
	if gotAuth != "Bearer nm_live_k" {
		t.Errorf("forwarded auth = %q", gotAuth)
	}
}

// TestMapUpstream covers the 200 passthrough plus the 404/429 error mappings.
func TestMapUpstream(t *testing.T) {
	if r := mapUpstream(200, []byte(`{"x":1}`)); r.IsError {
		t.Error("200 must not be an error result")
	} else if textOf(t, r) != `{"x":1}` {
		t.Errorf("200 body = %q", textOf(t, r))
	}

	for _, status := range []int{401, 403, 404, 429, 500} {
		r := mapUpstream(status, []byte(`{"error":"nope"}`))
		if !r.IsError {
			t.Errorf("status %d must be an error result", status)
		}
		if txt := textOf(t, r); !contains(txt, "nope") {
			t.Errorf("status %d result should carry the upstream body, got %q", status, txt)
		}
	}
	// The rate-limit hint must mention the portal so the caller can act.
	if txt := textOf(t, mapUpstream(429, nil)); !contains(txt, devPortalURL) {
		t.Errorf("429 hint missing portal pointer: %q", txt)
	}
}

// TestUpstreamGetTimeout asserts a slow upstream surfaces as a transport error
// (mapped to a tool error by the handler) rather than hanging.
func TestUpstreamGetTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	up := NewUpstream(srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, _, err := up.Get(ctx, "/v1/catalog/search", newQuery(), "Bearer nm_live_k"); err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
}

// TestHandlerEndToEnd exercises a full tool handler: the auth form check, the
// pass-through GET (query params serialized), and the success mapping — plus the
// missing-key rejection path.
func TestHandlerEndToEnd(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		_, _ = w.Write([]byte(`{"data":{"items":[]}}`))
	}))
	defer srv.Close()

	tl := &tools{up: NewUpstream(srv.URL)}
	ctx := context.Background()

	// Happy path: a well-formed key + query params.
	req := &mcp.CallToolRequest{Extra: &mcp.RequestExtra{
		Header: http.Header{"Authorization": {"Bearer nm_test_smoke"}},
	}}
	res, _, err := tl.catalogSearch(ctx, req, catalogSearchInput{Type: "works", Q: "fate", Limit: 10})
	if err != nil {
		t.Fatalf("handler err: %v", err)
	}
	if res.IsError {
		t.Fatalf("handler returned an error result: %q", textOf(t, res))
	}
	if gotPath != "/v1/catalog/search" {
		t.Errorf("path = %q", gotPath)
	}
	if !contains(gotQuery, "q=fate") || !contains(gotQuery, "type=works") || !contains(gotQuery, "limit=10") {
		t.Errorf("query = %q (want type=works, q=fate and limit=10)", gotQuery)
	}

	// Missing-key path: no Authorization → the form check rejects before any
	// upstream call, returning a tool error pointing at the portal.
	noKey := &mcp.CallToolRequest{Extra: &mcp.RequestExtra{Header: http.Header{}}}
	res2, _, err := tl.catalogWorkGet(ctx, noKey, catalogWorkGetInput{ID: 1})
	if err != nil {
		t.Fatalf("handler err: %v", err)
	}
	if !res2.IsError {
		t.Fatal("missing key must yield an error result")
	}
	if !contains(textOf(t, res2), devPortalURL) {
		t.Errorf("auth error should point at the portal: %q", textOf(t, res2))
	}
}

// textOf extracts the first TextContent from a tool result.
func textOf(t *testing.T, r *mcp.CallToolResult) string {
	t.Helper()
	if len(r.Content) == 0 {
		t.Fatal("result has no content")
	}
	tc, ok := r.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] is %T, want *mcp.TextContent", r.Content[0])
	}
	return tc.Text
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
