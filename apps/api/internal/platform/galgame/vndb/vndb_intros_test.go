package vndb

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestFetchVNDescriptionsBatch(t *testing.T) {
	// One /vn page. v1 has a BBCode description; v2 exists but its description
	// is null (no synopsis) → mapped to ""; v3 is asked for but ABSENT from the
	// response (deleted / merged / fabricated id) → the caller must skip it.
	body := `{"results":[
		{"id":"v1","description":"A [b]bold[/b] tale. [From [url=https://x]shop[/url]]"},
		{"id":"v2","description":null}
	]}`

	c := New(time.Millisecond)
	c.http = &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, "/vn") {
			t.Errorf("unexpected path %q (want .../vn)", r.URL.Path)
		}
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})}

	out, err := c.FetchVNDescriptionsBatch(context.Background(), []string{"v1", "v2", "v3"})
	if err != nil {
		t.Fatal(err)
	}

	// Present with a description → raw BBCode, verbatim (normalization is the
	// caller's job, not the client's).
	if got, ok := out["v1"]; !ok || !strings.Contains(got, "[b]bold[/b]") {
		t.Fatalf("v1 want raw BBCode description, got %q (present=%v)", got, ok)
	}
	// Present but null description → "" (exists, no synopsis).
	if got, ok := out["v2"]; !ok || got != "" {
		t.Fatalf("v2 want present empty string, got %q (present=%v)", got, ok)
	}
	// Absent id (deleted / fabricated) → not in the map at all.
	if _, ok := out["v3"]; ok {
		t.Fatalf("v3 should be absent (VNDB didn't return it), got %q", out["v3"])
	}
}
