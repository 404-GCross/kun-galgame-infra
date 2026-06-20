package vndb

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestFetchVNReleaseCoversBatch(t *testing.T) {
	// Two pages (first has more:true). Covers are aggregated per vn across releases;
	// an image repeated across releases is returned raw (the caller dedups).
	page1 := `{"more":true,"results":[
		{"vns":[{"id":"v1"}],"images":[{"id":"cv10","type":"pkgfront","sexual":0,"violence":0},{"id":"cv11","type":"pkgback","sexual":1,"violence":0}]},
		{"vns":[{"id":"v2"}],"images":[{"id":"cv20","type":"dig","sexual":2,"violence":1}]}
	]}`
	page2 := `{"more":false,"results":[
		{"vns":[{"id":"v1"}],"images":[{"id":"cv12","type":"dig","sexual":0,"violence":0}]}
	]}`

	call := 0
	c := New(time.Millisecond)
	c.http = &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, "/release") {
			t.Errorf("unexpected path %q (want .../release)", r.URL.Path)
		}
		call++
		body := page1
		if call >= 2 {
			body = page2
		}
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})}

	out, err := c.FetchVNReleaseCoversBatch(context.Background(), []string{"v1", "v2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out["v1"]) != 3 { // cv10, cv11 (page1) + cv12 (page2)
		t.Fatalf("v1 want 3 covers, got %d: %+v", len(out["v1"]), out["v1"])
	}
	if len(out["v2"]) != 1 {
		t.Fatalf("v2 want 1 cover, got %d", len(out["v2"]))
	}
	if out["v1"][0].ID != "cv10" || out["v1"][0].Type != "pkgfront" {
		t.Fatalf("v1 cover[0] wrong: %+v", out["v1"][0])
	}
	if out["v2"][0].Sexual != 2 || out["v2"][0].Violence != 1 {
		t.Fatalf("v2 cover ratings wrong: %+v", out["v2"][0])
	}
}
