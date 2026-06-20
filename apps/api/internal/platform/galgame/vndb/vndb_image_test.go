package vndb

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFetchVNImagesBatch(t *testing.T) {
	// v1 has a cover; v2's image is null; v3 has no image key — only v1 survives.
	const body = `{"results":[
		{"id":"v1","image":{"id":"cv101","url":"https://t.vndb.org/cv/01/101.jpg","sexual":0.4,"violence":1.6}},
		{"id":"v2","image":null},
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

	out, err := c.FetchVNImagesBatch(context.Background(), []string{"v1", "v2", "v3", "v1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("want 1 cover (v2 null + v3 absent dropped), got %d: %v", len(out), out)
	}
	got, ok := out["v1"]
	if !ok {
		t.Fatal("v1 missing from result")
	}
	if got.ID != "cv101" || got.URL == "" || got.Sexual != 0.4 || got.Violence != 1.6 {
		t.Fatalf("decoded image wrong: %+v", got)
	}
}

func TestFetchVNImagesBatch_Empty(t *testing.T) {
	c := New(time.Millisecond)
	c.http = &http.Client{Transport: rtFunc(func(*http.Request) (*http.Response, error) {
		t.Error("no HTTP call expected for an empty id set")
		return nil, nil
	})}
	out, err := c.FetchVNImagesBatch(context.Background(), nil)
	if err != nil || len(out) != 0 {
		t.Fatalf("want empty result, no call; got %v err=%v", out, err)
	}
}
