package vndb

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestFetchVNScoresBatch(t *testing.T) {
	// One /vn page. v1 has votes (normal rating); v2 has zero votes so VNDB
	// returns rating:null; v3 is asked for but ABSENT from the response
	// (deleted / merged / fabricated id) → the caller must skip it.
	body := `{"results":[
		{"id":"v1","rating":82.34,"votecount":1234},
		{"id":"v2","rating":null,"votecount":0}
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

	out, err := c.FetchVNScoresBatch(context.Background(), []string{"v1", "v2", "v3"})
	if err != nil {
		t.Fatal(err)
	}

	// Normal rating.
	v1, ok := out["v1"]
	if !ok || v1.Rating == nil || *v1.Rating != 82.34 || v1.VoteCount != 1234 {
		t.Fatalf("v1 want rating 82.34 / 1234 votes, got %+v", v1)
	}
	// Zero votes → rating null (never a fake zero).
	v2, ok := out["v2"]
	if !ok || v2.Rating != nil || v2.VoteCount != 0 {
		t.Fatalf("v2 want nil rating + 0 votes, got %+v", v2)
	}
	// Absent id (deleted / fabricated) → not in the map at all.
	if _, ok := out["v3"]; ok {
		t.Fatalf("v3 should be absent (VNDB didn't return it), got %+v", out["v3"])
	}
}
