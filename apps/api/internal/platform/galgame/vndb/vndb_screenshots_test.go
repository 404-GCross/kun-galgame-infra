package vndb

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestFetchVNScreenshotsBatch(t *testing.T) {
	// v1 has 2 shots (order preserved); v2 has an empty array; v3 has no key.
	const body = `{"results":[
		{"id":"v1","screenshots":[
			{"id":"sf10","url":"https://t.vndb.org/sf/10/10.jpg","sexual":0.0,"violence":0.4},
			{"id":"sf11","url":"https://t.vndb.org/sf/11/11.jpg","sexual":1.6,"violence":0.0}
		]},
		{"id":"v2","screenshots":[]},
		{"id":"v3"}
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

	out, err := c.FetchVNScreenshotsBatch(context.Background(), []string{"v1", "v2", "v3"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out["v1"]) != 2 || out["v1"][0].ID != "sf10" || out["v1"][1].ID != "sf11" {
		t.Fatalf("v1 shots wrong (want sf10,sf11 in order): %+v", out["v1"])
	}
	if out["v1"][1].Sexual != 1.6 {
		t.Fatalf("v1 shot[1] sexual = %v, want 1.6", out["v1"][1].Sexual)
	}
	if _, ok := out["v2"]; ok {
		t.Error("v2 (empty screenshots) should be absent")
	}
	if _, ok := out["v3"]; ok {
		t.Error("v3 (no screenshots key) should be absent")
	}
}
